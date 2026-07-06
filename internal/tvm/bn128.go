package tvm

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bn254"
	"github.com/consensys/gnark-crypto/ecc/bn254/fp"
)

// The alt_bn128 (BN254) curve precompiles 0x06/0x07/0x08: EIP-196 point addition and scalar
// multiplication over G1, and the EIP-197 pairing check. These are UNGATED in java-tron
// (available since the TVM launch — see PrecompiledContracts.getContractForAddr, no
// VMConfig guard); their energy is repriced downward once allowTvmIstanbul activates
// (VMConfig.AllowIstanbul). CONSENSUS-CRITICAL: point validation and I/O encoding mirror
// java-tron's BN128Addition/BN128Multiplication/BN128Pairing, which port EthereumJ's
// zksnark BN128* classes. go-tron uses gnark-crypto's audited constant-time bn254 here
// rather than importing go-ethereum.

// fpModulus is the BN254 base-field prime p. A coordinate >= p is rejected, matching
// java-tron Fp.isValid (v.compareTo(P) < 0) and EIP-196/197's canonical-encoding rule.
var fpModulus = fp.Modulus()

// setCoord decodes a 32-byte big-endian coordinate into dst, failing if it is >= p.
func setCoord(dst *fp.Element, b []byte) error {
	if new(big.Int).SetBytes(b).Cmp(fpModulus) >= 0 {
		return errPrecompileFailure
	}
	dst.SetBytes(b)
	return nil
}

// decodeG1 reads a 64-byte (x‖y) G1 point, requiring each coordinate < p and the point on
// the curve. The all-zero encoding (0,0) is the point at infinity and is accepted. Mirrors
// BN128Fp.create + BN128.isValid.
func decodeG1(buf []byte) (bn254.G1Affine, error) {
	var p bn254.G1Affine
	if err := setCoord(&p.X, buf[0:32]); err != nil {
		return p, err
	}
	if err := setCoord(&p.Y, buf[32:64]); err != nil {
		return p, err
	}
	if !p.IsOnCurve() { // true for the (0,0) infinity encoding
		return p, errPrecompileFailure
	}
	return p, nil
}

// decodeG2 reads a 128-byte G2 point in EIP-197 order — x_imag‖x_real‖y_imag‖y_real, i.e.
// java-tron's "(b, a; d, c)" where the imaginary coefficient precedes the real one —
// requiring each coordinate < p, the point on the twist AND membership of the r-order
// subgroup. Mirrors BN128Fp2.create + BN128G2.isGroupMember.
func decodeG2(buf []byte) (bn254.G2Affine, error) {
	var p bn254.G2Affine
	// gnark's E2 is A0 + A1·u (A0 real, A1 imaginary); EIP-197 encodes the imaginary part first.
	if err := setCoord(&p.X.A1, buf[0:32]); err != nil { // x imaginary (b)
		return p, err
	}
	if err := setCoord(&p.X.A0, buf[32:64]); err != nil { // x real (a)
		return p, err
	}
	if err := setCoord(&p.Y.A1, buf[64:96]); err != nil { // y imaginary (d)
		return p, err
	}
	if err := setCoord(&p.Y.A0, buf[96:128]); err != nil { // y real (c)
		return p, err
	}
	// Both checks hold for the all-zero infinity encoding.
	if !p.IsOnCurve() || !p.IsInSubGroup() {
		return p, errPrecompileFailure
	}
	return p, nil
}

// encodeG1 serializes a G1 point as x‖y, each a 32-byte big-endian coordinate (infinity
// becomes 64 zero bytes), matching java-tron encodeRes(res.x().bytes(), res.y().bytes()).
func encodeG1(p *bn254.G1Affine) []byte {
	out := make([]byte, 64)
	xb := p.X.Bytes()
	yb := p.Y.Bytes()
	copy(out[0:32], xb[:])
	copy(out[32:64], yb[:])
	return out
}

// ---- 0x06 alt_bn128 addition (EIP-196) ----

type bn128Add struct{}

func (bn128Add) RequiredEnergy([]byte) uint64 { return 150 }
func (bn128Add) requiredEnergyCfg(_ []byte, cfg VMConfig) uint64 {
	if cfg.AllowIstanbul {
		return 150
	}
	return 500
}
func (bn128Add) Run(in []byte) ([]byte, error) {
	p1, err := decodeG1(rightPad(in, 0, 64))
	if err != nil {
		return nil, err
	}
	p2, err := decodeG1(rightPad(in, 64, 64))
	if err != nil {
		return nil, err
	}
	var res bn254.G1Affine
	res.Add(&p1, &p2)
	return encodeG1(&res), nil
}

// ---- 0x07 alt_bn128 scalar multiplication (EIP-196) ----

type bn128ScalarMul struct{}

func (bn128ScalarMul) RequiredEnergy([]byte) uint64 { return 6000 }
func (bn128ScalarMul) requiredEnergyCfg(_ []byte, cfg VMConfig) uint64 {
	if cfg.AllowIstanbul {
		return 6000
	}
	return 40000
}
func (bn128ScalarMul) Run(in []byte) ([]byte, error) {
	p, err := decodeG1(rightPad(in, 0, 64))
	if err != nil {
		return nil, err
	}
	// The scalar is a full 256-bit big-endian value; no field reduction (EIP-196).
	scalar := new(big.Int).SetBytes(rightPad(in, 64, 32))
	var res bn254.G1Affine
	res.ScalarMultiplication(&p, scalar)
	return encodeG1(&res), nil
}

// ---- 0x08 alt_bn128 pairing check (EIP-197) ----

type bn128Pairing struct{}

const bn128PairSize = 192

func (bn128Pairing) RequiredEnergy(in []byte) uint64 {
	return 34000*uint64(len(in)/bn128PairSize) + 45000
}
func (bn128Pairing) requiredEnergyCfg(in []byte, cfg VMConfig) uint64 {
	k := uint64(len(in) / bn128PairSize)
	if cfg.AllowIstanbul {
		return 34000*k + 45000
	}
	return 80000*k + 100000
}
func (bn128Pairing) Run(in []byte) ([]byte, error) {
	// A length that is not a whole number of 192-byte pairs fails the call.
	if len(in)%bn128PairSize != 0 {
		return nil, errPrecompileFailure
	}
	k := len(in) / bn128PairSize
	// Empty input: the product over zero pairs is 1 (EIP-197); return true.
	if k == 0 {
		out := make([]byte, 32)
		out[31] = 1
		return out, nil
	}
	g1s := make([]bn254.G1Affine, 0, k)
	g2s := make([]bn254.G2Affine, 0, k)
	for i := 0; i < k; i++ {
		off := i * bn128PairSize
		p1, err := decodeG1(in[off : off+64])
		if err != nil {
			return nil, err
		}
		p2, err := decodeG2(in[off+64 : off+bn128PairSize])
		if err != nil {
			return nil, err
		}
		g1s = append(g1s, p1)
		g2s = append(g2s, p2)
	}
	ok, err := bn254.PairingCheck(g1s, g2s)
	if err != nil {
		return nil, errPrecompileFailure
	}
	out := make([]byte, 32)
	if ok {
		out[31] = 1
	}
	return out, nil
}
