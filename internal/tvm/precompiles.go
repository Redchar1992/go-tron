package tvm

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // RIPEMD-160 is consensus-required
)

// errPrecompileFailure is the hard-failure returned by a precompile's Run on malformed input
// (e.g. an off-curve bn128 point or a wrong-length blake2F block). It fails the CALL and, per
// java-tron Program.callToPrecompiledAddress (refundEnergy(0) on a false result), consumes
// all forwarded energy — which runPrecompile already does on any Run error.
var errPrecompileFailure = errors.New("tvm: precompile failure")

// Precompile is a built-in contract executed natively instead of as bytecode. Run returns
// the output (nil with no error means "valid call, empty result", e.g. ecrecover on a bad
// signature); a non-nil error is a hard failure that fails the CALL and consumes the
// forwarded energy.
//
// Faithful to java-tron PrecompiledContracts: each contract exposes a fixed-or-input-
// derived energy cost and a Run. CONSENSUS-CRITICAL.
type Precompile interface {
	RequiredEnergy(input []byte) uint64
	Run(input []byte) ([]byte, error)
}

// configEnergy is an optional interface for precompiles whose energy cost depends on the
// active fork (VMConfig) — e.g. the bn128 contracts, repriced by allowTvmIstanbul. When a
// precompile implements it, runPrecompile prefers requiredEnergyCfg over RequiredEnergy.
type configEnergy interface {
	requiredEnergyCfg(input []byte, cfg VMConfig) uint64
}

// precompiles maps a TRON precompile address (21-byte, 0x41-prefixed) to its contract.
// Built once; these are the fork-independent (ungated) contracts: the standard 0x01..0x08
// set (incl. the alt_bn128 curve ops) plus the EVM-compat RIPEMD-160. Availability-gated
// contracts (blake2F) are resolved in lookupPrecompile instead.
var precompiles = func() map[string]Precompile {
	m := map[string]Precompile{
		string(precompileAddr(0x01)):    ecrecover{},
		string(precompileAddr(0x02)):    sha256Hash{},
		string(precompileAddr(0x03)):    tronRipemd160{}, // TRON: sha256(sha256(x)[:20]), NOT RIPEMD-160
		string(precompileAddr(0x04)):    dataCopy{},
		string(precompileAddr(0x05)):    bigModExp{},
		string(precompileAddr(0x06)):    bn128Add{},       // alt_bn128 G1 addition (EIP-196)
		string(precompileAddr(0x07)):    bn128ScalarMul{}, // alt_bn128 G1 scalar-mul (EIP-196)
		string(precompileAddr(0x08)):    bn128Pairing{},   // alt_bn128 pairing check (EIP-197)
		string(precompileAddr(0x20003)): ethRipemd160{},   // EVM-compat: the real RIPEMD-160
	}
	return m
}()

// precompileAddr builds the 21-byte TRON address of a precompile from its numeric address
// value (0x41 prefix + the value big-endian in the low bytes). Matches wordToAddr applied
// to the 32-byte DataWord the VM compares against (last 20 bytes + 0x41 prefix).
func precompileAddr(v uint64) []byte {
	a := make([]byte, 21)
	a[0] = addrPrefix
	binary.BigEndian.PutUint64(a[13:21], v)
	return a
}

// lookupPrecompile returns the precompile at addr under the active fork config, or nil.
// Ungated contracts come from the static map; availability-gated ones are resolved here to
// match java-tron getContractForAddr's per-contract VMConfig guards:
//   - blake2F (0x20009): present once allowTvmCompatibleEvm activates (cfg.Forward6364).
//   - batchvalidatesign (0x09) / validatemultisign (0x0a): present once allowTvmSolidity059
//     activates (cfg.AllowSolidity059). validatemultisign is stateful, so it carries the
//     account-permission reader (nil for a bare EVM => it returns false).
func lookupPrecompile(addr []byte, cfg VMConfig, perm AccountPermissionReader) Precompile {
	if pc := precompiles[string(addr)]; pc != nil {
		return pc
	}
	if cfg.Forward6364 && string(addr) == string(precompileAddr(0x20009)) {
		return blake2F{}
	}
	if cfg.AllowSolidity059 {
		switch string(addr) {
		case string(precompileAddr(0x09)):
			return batchValidateSign{}
		case string(precompileAddr(0x0a)):
			return validateMultiSign{perm: perm}
		}
	}
	return nil
}

// runPrecompile charges a precompile's energy against budget and runs it. A cost over
// budget or a Run error consumes the whole forwarded budget and fails the call. Fork-
// dependent contracts (bn128) price via configEnergy; the rest use RequiredEnergy.
func runPrecompile(pc Precompile, input []byte, budget uint64, cfg VMConfig) (out []byte, used uint64, err error) {
	var cost uint64
	if ce, ok := pc.(configEnergy); ok {
		cost = ce.requiredEnergyCfg(input, cfg)
	} else {
		cost = pc.RequiredEnergy(input)
	}
	if cost > budget {
		return nil, budget, ErrOutOfEnergy
	}
	out, err = pc.Run(input)
	if err != nil {
		return nil, budget, err
	}
	return out, cost, nil
}

// ---- energy helpers ----

// wordCount returns ceil(len/32) — the 32-byte word count used in linear cost formulas.
func wordCount(n int) uint64 { return (uint64(n) + 31) / 32 }

// ---- 0x04 identity (data copy) ----

type dataCopy struct{}

func (dataCopy) RequiredEnergy(in []byte) uint64 { return 15 + 3*wordCount(len(in)) }
func (dataCopy) Run(in []byte) ([]byte, error)   { return append([]byte(nil), in...), nil }

// ---- 0x02 sha256 ----

type sha256Hash struct{}

func (sha256Hash) RequiredEnergy(in []byte) uint64 { return 60 + 12*wordCount(len(in)) }
func (sha256Hash) Run(in []byte) ([]byte, error) {
	h := sha256.Sum256(in)
	return h[:], nil
}

// ---- 0x03 "ripempd160" — TRON DEVIATION: this is NOT RIPEMD-160. ----
// TRON's 0x03 computes sha256(sha256(input)[:20]); the real RIPEMD-160 lives at 0x20003
// (ethRipemd160). A contract expecting Ethereum's 0x03 behavior would diverge.

type tronRipemd160 struct{}

func (tronRipemd160) RequiredEnergy(in []byte) uint64 { return 600 + 120*wordCount(len(in)) }
func (tronRipemd160) Run(in []byte) ([]byte, error) {
	orig := sha256.Sum256(in)
	second := sha256.Sum256(orig[:20])
	return second[:], nil
}

// ---- 0x20003 ethRipemd160 — the real RIPEMD-160, output left-padded to 32 bytes. ----

type ethRipemd160 struct{}

func (ethRipemd160) RequiredEnergy(in []byte) uint64 { return 600 + 120*wordCount(len(in)) }
func (ethRipemd160) Run(in []byte) ([]byte, error) {
	h := ripemd160.New()
	h.Write(in)
	out := make([]byte, 32)
	copy(out[12:], h.Sum(nil))
	return out, nil
}

// ---- 0x05 modexp (big-integer modular exponentiation) ----

type bigModExp struct{}

// fields parses the three length headers and the base/exp/mod from a modexp input,
// zero-extending a short input (EVM calldata semantics).
func (bigModExp) fields(in []byte) (baseLen, expLen, modLen int, base, exp, mod *big.Int) {
	get := func(off int) *big.Int { return new(big.Int).SetBytes(rightPad(in, off, 32)) }
	baseLen = int(get(0).Uint64())
	expLen = int(get(32).Uint64())
	modLen = int(get(64).Uint64())
	off := 96
	base = new(big.Int).SetBytes(rightPad(in, off, baseLen))
	exp = new(big.Int).SetBytes(rightPad(in, off+baseLen, expLen))
	mod = new(big.Int).SetBytes(rightPad(in, off+baseLen+expLen, modLen))
	return
}

func (m bigModExp) Run(in []byte) ([]byte, error) {
	_, _, modLen, base, exp, mod := m.fields(in)
	// TRON deviation: a zero-length or zero-valued modulus returns an EMPTY result (not
	// modLen zero bytes as in EIP-198). See PrecompiledContracts modExp.
	if modLen == 0 || mod.Sign() == 0 {
		return []byte{}, nil
	}
	out := make([]byte, modLen)
	res := new(big.Int).Exp(base, exp, mod)
	resB := res.Bytes()
	if len(resB) <= modLen {
		copy(out[modLen-len(resB):], resB) // left-pad to modLen
	} else {
		copy(out, resB[len(resB)-modLen:]) // truncate to low modLen bytes
	}
	return out, nil
}

// RequiredEnergy is java-tron's modexp cost: multComplexity(max(baseLen,modLen)) *
// max(adjustedExpLen, 1) / 20 (GQUAD_DIVISOR), matching PrecompiledContracts.getEnergyForData.
func (m bigModExp) RequiredEnergy(in []byte) uint64 {
	baseLen, expLen, _, _, exp, _ := m.fields(in)
	maxLen := baseLen
	if l := modLenOf(in); l > maxLen {
		maxLen = l
	}
	mult := multComplexity(uint64(maxLen))
	adj := adjustedExpLen(uint64(expLen), exp)
	const gQuadDivisor = 20
	return mult * maxU64(adj, 1) / gQuadDivisor
}

func modLenOf(in []byte) int {
	return int(new(big.Int).SetBytes(rightPad(in, 64, 32)).Uint64())
}

// multComplexity is EIP-198's f(x): x^2 for x<=64, etc.
func multComplexity(x uint64) uint64 {
	switch {
	case x <= 64:
		return x * x
	case x <= 1024:
		return x*x/4 + 96*x - 3072
	default:
		return x*x/16 + 480*x - 199680
	}
}

// adjustedExpLen is EIP-198's adjusted exponent length: for a short exponent it is the
// index of its highest set bit; for a long one it adds 8 bits per byte beyond the first
// 32, plus the highest set bit within those first (most-significant) 32 bytes.
func adjustedExpLen(expLen uint64, exp *big.Int) uint64 {
	if expLen <= 32 {
		if exp.Sign() == 0 {
			return 0
		}
		return uint64(exp.BitLen() - 1)
	}
	high := new(big.Int).Rsh(exp, uint(8*(expLen-32))) // the most-significant 32 bytes
	hb := uint64(0)
	if high.BitLen() > 0 {
		hb = uint64(high.BitLen() - 1)
	}
	return 8*(expLen-32) + hb
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// rightPad returns size bytes of src starting at off, zero-filling past the end.
func rightPad(src []byte, off, size int) []byte {
	out := make([]byte, size)
	if off < len(src) {
		copy(out, src[off:min(off+size, len(src))])
	}
	return out
}

// ecrecover (0x01) — secp256k1 public-key recovery. Implemented in M3.2c (needs a
// secp256k1 dependency); registered here so the address resolves.
type ecrecover struct{}

func (ecrecover) RequiredEnergy([]byte) uint64  { return 3000 }
func (ecrecover) Run(in []byte) ([]byte, error) { return ecrecoverRun(in) }
