package tvm

import (
	"encoding/binary"
	"math"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// TRON's multisig precompiles: batchvalidatesign (0x09) and validatemultisign (0x0a). Both
// are availability-gated on allowTvmSolidity059 (block version >= 9) and resolved in
// lookupPrecompile, not the static map. CONSENSUS-CRITICAL; faithful to java-tron
// PrecompiledContracts.BatchValidateSign / ValidateMultiSign.
//
// SCOPE: this implements the pre-VERSION_4_8_1 behaviour (allowTvmSelfdestructRestriction ==
// false), which is what a from-genesis chain runs — that proposal (chain-parameter #94,
// requires block version 34) is not processed by go-tron yet. Under the restriction the
// signature array is read as fixed 65-byte slots (extractSigArray) with an early MAX_SIZE
// precheck; without it (here) signatures are read as a normal ABI bytes[] (extractBytesArray)
// and the size is checked after extraction. For well-formed 65-byte signatures the two paths
// are identical; they diverge only on malformed calldata. The restricted path activates with
// proposal processing.

const (
	wordSize      = 32
	energyPerSign = 1500 // ENGERYPERSIGN: half of ecrecover's 3000
	batchMaxSize  = 16   // BatchValidateSign.MAX_SIZE
	multiSignMax  = 5    // ValidateMultiSign.MAX_SIZE
)

// PermissionKey is one weighted signer in an account permission (java-tron protocol.Key).
type PermissionKey struct {
	Address []byte // 21-byte 0x41 TRON address
	Weight  int64
}

// AccountPermissionReader resolves an account's multisig permission for the validatemultisign
// precompile (0x0a) — the go-tron analog of AccountCapsule.getPermissionById over the node's
// account store. ok=false means the account or the permission id does not exist, in which case
// the precompile returns false. The node adapter (internal/actuator) implements it; when no
// reader is wired (e.g. a bare EVM in a unit test) 0x0a also returns false.
type AccountPermissionReader interface {
	PermissionById(addr []byte, id int) (threshold int64, keys []PermissionKey, ok bool)
}

// ---- shared ABI / signature helpers (java-tron DataWord + PrecompiledContracts) ----

// parseWords splits data into 32-byte words, dropping any partial trailing word — matching
// DataWord.parseArray (len = data.length / WORD_SIZE, floor).
func parseWords(data []byte) [][]byte {
	n := len(data) / wordSize
	words := make([][]byte, n)
	for i := 0; i < n; i++ {
		words[i] = data[i*wordSize : (i+1)*wordSize]
	}
	return words
}

// intValueSafe mirrors DataWord.intValueSafe: the low 4 bytes as a signed int, or
// Integer.MAX_VALUE when the word occupies more than 4 bytes or the low int is negative.
func intValueSafe(w []byte) int {
	occupied := 0
	for i := 0; i < wordSize; i++ {
		if w[i] != 0 {
			occupied = wordSize - i
			break
		}
	}
	iv := int32(binary.BigEndian.Uint32(w[wordSize-4 : wordSize]))
	if occupied > 4 || iv < 0 {
		return math.MaxInt32
	}
	return int(iv)
}

// toTronAddress mirrors DataWord.toTronAddress: 0x41 prefix + the low 20 bytes of the word.
func toTronAddress(w []byte) []byte {
	addr := make([]byte, 21)
	addr[0] = addrPrefix
	copy(addr[1:], w[wordSize-20:wordSize])
	return addr
}

// equalAddressBytes mirrors DataWord.equalAddressByteArray: compare the last 20 bytes of each
// (so a 21-byte 0x41 address matches a 32-byte word that carries the address right-aligned).
func equalAddressBytes(a, b []byte) bool {
	if len(a) < 20 || len(b) < 20 {
		return false
	}
	return string(a[len(a)-20:]) == string(b[len(b)-20:])
}

// recoverTronAddr recovers the 21-byte 0x41 TRON address that signed hash, or nil on any
// malformed/invalid signature — matching PrecompiledContracts.recoverAddrBySign (sign =
// r||s||v, v normalised via +27 when < 27; empty result on failure).
func recoverTronAddr(sign, hash []byte) []byte {
	if len(sign) < 65 {
		return nil
	}
	v := sign[64]
	if v < 27 {
		v += 27
	}
	if v != 27 && v != 28 {
		return nil
	}
	compact := make([]byte, 65)
	compact[0] = v
	copy(compact[1:33], sign[0:32])   // r
	copy(compact[33:65], sign[32:64]) // s
	pub, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil || pub == nil {
		return nil
	}
	uncompressed := pub.SerializeUncompressed() // 0x04 || X(32) || Y(32)
	digest := crypto.Keccak256(uncompressed[1:])
	addr := make([]byte, 21)
	addr[0] = addrPrefix
	copy(addr[1:], digest[12:32])
	return addr
}

// extractBytes32Array mirrors PrecompiledContracts.extractBytes32Array: length word at
// offset, then that many 32-byte words.
func extractBytes32Array(words [][]byte, offset int) [][]byte {
	n := intValueSafe(words[offset])
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = words[offset+i+1]
	}
	return out
}

// extractBytesArray mirrors PrecompiledContracts.extractBytesArray (the
// allowTvmSelfdestructRestriction==false path): length word at offset, then per element an
// offset word pointing at a (length, data) pair.
func extractBytesArray(words [][]byte, offset int, data []byte) [][]byte {
	if offset > len(words)-1 {
		return nil
	}
	n := intValueSafe(words[offset])
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		bytesOffset := intValueSafe(words[offset+i+1]) / wordSize
		bytesLen := intValueSafe(words[offset+bytesOffset+1])
		out[i] = extractBytes(data, (bytesOffset+offset+2)*wordSize, bytesLen)
	}
	return out
}

// extractBytes mirrors Arrays.copyOfRange(data, off, off+n): n bytes from off, zero-padded
// past the end of data.
func extractBytes(data []byte, off, n int) []byte {
	out := make([]byte, n)
	if off < len(data) {
		copy(out, data[off:min(off+n, len(data))])
	}
	return out
}

// ---- 0x09 batchvalidatesign ----

// batchValidateSign verifies a batch of (signature, expected-address) pairs against one hash,
// returning a 32-byte word whose byte i is 1 iff signature i recovers to addresses[i].
type batchValidateSign struct{}

func (batchValidateSign) RequiredEnergy(in []byte) uint64 {
	cnt := (int64(len(in))/wordSize - 5) / 6
	if cnt < 0 {
		return 0
	}
	return uint64(cnt) * energyPerSign
}

func (batchValidateSign) Run(in []byte) (out []byte, err error) {
	res := make([]byte, wordSize)
	// java-tron catches any Throwable and returns a 32-byte zero word.
	defer func() {
		if recover() != nil {
			out, err = make([]byte, wordSize), nil
		}
	}()
	words := parseWords(in)
	if len(words) < 3 {
		return res, nil
	}
	hash := words[0]
	sigs := extractBytesArray(words, intValueSafe(words[1])/wordSize, in)
	addrs := extractBytes32Array(words, intValueSafe(words[2])/wordSize)
	cnt := len(sigs)
	if cnt == 0 || cnt > batchMaxSize || cnt != len(addrs) {
		return res, nil
	}
	for i := 0; i < cnt; i++ {
		if equalAddressBytes(addrs[i], recoverTronAddr(sigs[i], hash)) {
			res[i] = 1
		}
	}
	return res, nil
}

// ---- 0x0a validatemultisign ----

// validateMultiSign checks that the supplied signatures over sha256(address||permissionId||
// data) meet the account permission's weight threshold. It reads account-permission state via
// the AccountPermissionReader (nil => always false).
type validateMultiSign struct{ perm AccountPermissionReader }

func (validateMultiSign) RequiredEnergy(in []byte) uint64 {
	cnt := (int64(len(in))/wordSize - 5) / 5
	if cnt < 0 {
		return 0
	}
	return uint64(cnt) * energyPerSign
}

func (v validateMultiSign) Run(in []byte) (out []byte, err error) {
	dataFalse := make([]byte, wordSize)
	defer func() {
		if recover() != nil {
			out, err = make([]byte, wordSize), nil
		}
	}()
	words := parseWords(in)
	if len(words) < 4 {
		return dataFalse, nil
	}
	address := toTronAddress(words[0])
	permissionID := intValueSafe(words[1])
	data := words[2]

	combine := make([]byte, 0, len(address)+4+len(data))
	combine = append(combine, address...)
	combine = append(combine, int32BE(permissionID)...)
	combine = append(combine, data...)
	hash := crypto.Sha256(combine)

	sigs := extractBytesArray(words, intValueSafe(words[3])/wordSize, in)
	if len(sigs) == 0 || len(sigs) > multiSignMax {
		return dataFalse, nil
	}
	if v.perm == nil {
		return dataFalse, nil
	}
	threshold, keys, ok := v.perm.PermissionById(address, permissionID)
	if !ok {
		return dataFalse, nil
	}

	var total int64
	seenAddr := make(map[string]bool)
	seenSign := make(map[string]bool)
	for _, sign := range sigs {
		recovered := recoverTronAddr(sign, hash)
		merged := append(append([]byte{}, recovered...), sign...)
		if seenAddr[string(recovered)] && seenSign[string(merged)] {
			continue // exact (address, signature) duplicate — do not double-count
		}
		weight := weightOf(keys, recovered)
		if weight == 0 {
			return dataFalse, nil
		}
		total += weight
		seenSign[string(merged)] = true
		seenAddr[string(recovered)] = true
	}
	if total >= threshold {
		res := make([]byte, wordSize)
		res[wordSize-1] = 1
		return res, nil
	}
	return dataFalse, nil
}

// weightOf mirrors TransactionCapsule.getWeight: the weight of the key whose address matches,
// or 0 if none (which fails the whole call).
func weightOf(keys []PermissionKey, addr []byte) int64 {
	if addr == nil {
		return 0
	}
	for _, k := range keys {
		if string(k.Address) == string(addr) {
			return k.Weight
		}
	}
	return 0
}

// int32BE is ByteArray.fromInt: a 4-byte big-endian encoding of the permission id.
func int32BE(v int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(v)))
	return b[:]
}
