package tvm

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// signer is a deterministic test key with its derived 21-byte TRON address.
type signer struct {
	priv *secp256k1.PrivateKey
	addr []byte // 21-byte 0x41 address
}

func newSigner(seed byte) signer {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i) + 1 // non-zero, in-range scalar
	}
	priv := secp256k1.PrivKeyFromBytes(b)
	uncompressed := priv.PubKey().SerializeUncompressed()
	digest := crypto.Keccak256(uncompressed[1:])
	addr := append([]byte{addrPrefix}, digest[12:32]...)
	return signer{priv: priv, addr: addr}
}

// signTron produces a TRON-format r||s||v signature over hash.
func (s signer) signTron(hash []byte) []byte {
	c := ecdsa.SignCompact(s.priv, hash, false) // [v(27+recid)][R][S]
	out := make([]byte, 65)
	copy(out[0:32], c[1:33])
	copy(out[32:64], c[33:65])
	out[64] = c[0]
	return out
}

func word(v int) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint64(b[24:], uint64(v))
	return b
}

// addr32Word right-aligns a 21-byte 0x41 address's 20 address bytes into a 32-byte word.
func addr32Word(addr21 []byte) []byte {
	b := make([]byte, 32)
	copy(b[12:], addr21[1:])
	return b
}

// wordsOf is ceil(n/32).
func wordsOf(n int) int { return (n + 31) / 32 }

// encodeBytesArray ABI-encodes a bytes[] (length ++ element offsets ++ elements), matching
// the layout PrecompiledContracts.extractBytesArray decodes.
func encodeBytesArray(elems [][]byte) []byte {
	var offs, tail bytes.Buffer
	cursor := len(elems) // element length words start after the offset section
	for _, e := range elems {
		offs.Write(word(cursor * 32))
		tail.Write(word(len(e)))
		padded := make([]byte, wordsOf(len(e))*32)
		copy(padded, e)
		tail.Write(padded)
		cursor += 1 + wordsOf(len(e))
	}
	var out bytes.Buffer
	out.Write(word(len(elems)))
	out.Write(offs.Bytes())
	out.Write(tail.Bytes())
	return out.Bytes()
}

func encodeBytes32Array(words [][]byte) []byte {
	var out bytes.Buffer
	out.Write(word(len(words)))
	for _, w := range words {
		out.Write(w)
	}
	return out.Bytes()
}

// encodeBatch builds batchvalidatesign input: (bytes32 hash, bytes[] sigs, bytes32[] addrs).
func encodeBatch(hash []byte, sigs, addrWords [][]byte) []byte {
	sigBlock := encodeBytesArray(sigs)
	addrBlock := encodeBytes32Array(addrWords)
	head := 3 * 32
	var out bytes.Buffer
	out.Write(hash)
	out.Write(word(head))                 // offset to sigs
	out.Write(word(head + len(sigBlock))) // offset to addrs
	out.Write(sigBlock)
	out.Write(addrBlock)
	return out.Bytes()
}

func TestRecoverTronAddr(t *testing.T) {
	s := newSigner(1)
	hash := crypto.Sha256([]byte("hello tron multisig"))
	sig := s.signTron(hash)
	if got := recoverTronAddr(sig, hash); !bytes.Equal(got, s.addr) {
		t.Fatalf("recoverTronAddr = %x, want %x", got, s.addr)
	}
	// Wrong hash -> different (or nil) address, never the signer's.
	if got := recoverTronAddr(sig, crypto.Sha256([]byte("other"))); bytes.Equal(got, s.addr) {
		t.Fatal("recoverTronAddr matched signer on the wrong hash")
	}
	// Too-short signature -> nil.
	if got := recoverTronAddr(sig[:64], hash); got != nil {
		t.Fatalf("recoverTronAddr(short) = %x, want nil", got)
	}
}

func TestMultisigHelpers(t *testing.T) {
	if got := parseWords(make([]byte, 70)); len(got) != 2 { // floor(70/32) = 2
		t.Fatalf("parseWords len = %d, want 2 (floor)", len(got))
	}
	if got := intValueSafe(word(0x2a)); got != 0x2a {
		t.Fatalf("intValueSafe = %d, want 42", got)
	}
	big := make([]byte, 32) // occupies > 4 bytes -> MaxInt32
	big[10] = 1
	if got := intValueSafe(big); got != math.MaxInt32 {
		t.Fatalf("intValueSafe(big) = %d, want MaxInt32", got)
	}
	s := newSigner(2)
	if got := toTronAddress(addr32Word(s.addr)); !bytes.Equal(got, s.addr) {
		t.Fatalf("toTronAddress = %x, want %x", got, s.addr)
	}
	if !equalAddressBytes(s.addr, addr32Word(s.addr)) {
		t.Fatal("equalAddressBytes should match a 21-byte addr against its right-aligned word")
	}
}

func TestBatchValidateSign(t *testing.T) {
	hash := crypto.Sha256([]byte("batch"))
	a, b := newSigner(3), newSigner(4)
	sigA, sigB := a.signTron(hash), b.signTron(hash)

	// a's sig paired with a's addr (valid); b's sig paired with a WRONG addr (invalid).
	in := encodeBatch(hash, [][]byte{sigA, sigB}, [][]byte{addr32Word(a.addr), addr32Word(b.addr)})
	out, err := batchValidateSign{}.Run(in)
	if err != nil || len(out) != 32 {
		t.Fatalf("Run err=%v len=%d", err, len(out))
	}
	if out[0] != 1 || out[1] != 1 {
		t.Fatalf("both valid pairs should be 1, got %x", out[:2])
	}

	// Swap b's expected address so signature 1 no longer matches.
	in2 := encodeBatch(hash, [][]byte{sigA, sigB}, [][]byte{addr32Word(a.addr), addr32Word(a.addr)})
	out2, _ := batchValidateSign{}.Run(in2)
	if out2[0] != 1 || out2[1] != 0 {
		t.Fatalf("want [1 0] for mismatched second addr, got %x", out2[:2])
	}
}

// stubPerm is a fixed permission for validatemultisign tests.
type stubPerm struct {
	threshold int64
	keys      []PermissionKey
	ok        bool
}

func (s stubPerm) PermissionById([]byte, int) (int64, []PermissionKey, bool) {
	return s.threshold, s.keys, s.ok
}

// encodeMultiSign builds validatemultisign input: (address, uint256 permId, bytes32 data,
// bytes[] sigs) and returns it together with the hash the signers must sign.
func encodeMultiSign(addr21 []byte, permID int, data []byte, sigs [][]byte) ([]byte, []byte) {
	head := 4 * 32
	var out bytes.Buffer
	out.Write(addr32Word(addr21))
	out.Write(word(permID))
	out.Write(data)
	out.Write(word(head))
	out.Write(encodeBytesArray(sigs))

	combine := append(append(append([]byte{}, addr21...), int32BE(permID)...), data...)
	return out.Bytes(), crypto.Sha256(combine)
}

func TestValidateMultiSign(t *testing.T) {
	owner := newSigner(5)
	other := newSigner(6)
	data := crypto.Sha256([]byte("msg"))

	// Build input first (to learn the hash), then sign that hash.
	in, hash := encodeMultiSign(owner.addr, 2, data, [][]byte{make([]byte, 65)})
	sig := owner.signTron(hash)
	in, _ = encodeMultiSign(owner.addr, 2, data, [][]byte{sig})

	perm := stubPerm{threshold: 1, keys: []PermissionKey{{Address: owner.addr, Weight: 1}}, ok: true}

	// Valid single sig meeting threshold -> dataOne (last byte 1).
	out, err := (validateMultiSign{perm: perm}).Run(in)
	if err != nil || out[31] != 1 {
		t.Fatalf("valid multisign want last byte 1, got %x (err %v)", out, err)
	}

	// A signer not in the permission -> weight 0 -> false.
	badIn, badHash := encodeMultiSign(owner.addr, 2, data, [][]byte{make([]byte, 65)})
	badIn, _ = encodeMultiSign(owner.addr, 2, data, [][]byte{other.signTron(badHash)})
	out2, _ := (validateMultiSign{perm: perm}).Run(badIn)
	if out2[31] != 0 {
		t.Fatalf("non-permission signer should fail, got %x", out2)
	}

	// Threshold above the summed weight -> false.
	out3, _ := (validateMultiSign{perm: stubPerm{threshold: 2, keys: perm.keys, ok: true}}).Run(in)
	if out3[31] != 0 {
		t.Fatalf("below-threshold want 0, got %x", out3)
	}

	// No permission reader -> false.
	out4, _ := (validateMultiSign{perm: nil}).Run(in)
	if out4[31] != 0 {
		t.Fatalf("nil reader want 0, got %x", out4)
	}
}

func TestMultisigEnergy(t *testing.T) {
	// batchvalidatesign: (len/32 - 5)/6 * 1500.
	in := make([]byte, 17*32) // 17 words -> (17-5)/6 = 2
	if got := (batchValidateSign{}).RequiredEnergy(in); got != 2*1500 {
		t.Fatalf("batch energy = %d, want 3000", got)
	}
	// validatemultisign: (len/32 - 5)/5 * 1500.
	in2 := make([]byte, 15*32) // (15-5)/5 = 2
	if got := (validateMultiSign{}).RequiredEnergy(in2); got != 2*1500 {
		t.Fatalf("multisign energy = %d, want 3000", got)
	}
	// Undersized input clamps to 0 rather than underflowing.
	if got := (batchValidateSign{}).RequiredEnergy(make([]byte, 32)); got != 0 {
		t.Fatalf("undersized energy = %d, want 0", got)
	}
}
