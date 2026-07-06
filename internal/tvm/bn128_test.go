package tvm

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// bn254 constants used by the vectors below.
const (
	// The G1 generator is (1, 2); 2G is its double on y² = x³ + 3.
	twoGx = "030644e72e131a029b85045b68181585d97816a916871ca8d3c208c16d87cfd3"
	twoGy = "15ed738c0e0a7c92e7845f96b2ae9c0a68a6a449e3538fc7ff3ebf7a5a18a2c4"
	// p is the base-field modulus; p-2 negates the y of the generator (2 -> p-2).
	fieldP  = "30644e72e131a029b85045b68181585d97816a916871ca8d3c208c16d87cfd47"
	negTwoY = "30644e72e131a029b85045b68181585d97816a916871ca8d3c208c16d87cfd45"
	// The standard G2 generator, coordinates in EIP-197 order (imaginary A1, real A0).
	g2xi = "198e9393920d483a7260bfb731fb5d25f1aa493335a9e71297e485b7aef312c2"
	g2xr = "1800deef121f1e76426a00665e5c4479674322d4f75edadd46debd5cd992f6ed"
	g2yi = "090689d0585ff075ec9e99ad690c3395bc4b313370b38ef355acdadcd122975b"
	g2yr = "12c85ea5db8c6deb4aab71808dcb408fe3d1e7690c43d37b4ce6cc0166fa7daa"
)

// w decodes an even-length hex string into a 32-byte big-endian, left-padded word.
func w(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatalf("hex %q: %v", h, err)
	}
	if len(b) > 32 {
		t.Fatalf("word %q > 32 bytes", h)
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// TestBn128Add covers EIP-196 addition: G + G = 2G, and identity with the point at
// infinity (0,0).
func TestBn128Add(t *testing.T) {
	// (1,2) + (1,2) = 2G
	in := concat(w(t, "01"), w(t, "02"), w(t, "01"), w(t, "02"))
	out, err := bn128Add{}.Run(in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := concat(w(t, twoGx), w(t, twoGy)); !bytes.Equal(out, want) {
		t.Fatalf("G+G = %x\nwant   %x", out, want)
	}
	// (1,2) + infinity(0,0) = (1,2)
	in2 := concat(w(t, "01"), w(t, "02"), w(t, "00"), w(t, "00"))
	out2, err := bn128Add{}.Run(in2)
	if err != nil {
		t.Fatalf("Run infinity: %v", err)
	}
	if want := concat(w(t, "01"), w(t, "02")); !bytes.Equal(out2, want) {
		t.Fatalf("G+O = %x, want %x", out2, want)
	}
}

// TestBn128ScalarMul covers EIP-196 scalar multiplication: 1·G = G and 2·G = 2G.
func TestBn128ScalarMul(t *testing.T) {
	one := concat(w(t, "01"), w(t, "02"), w(t, "01"))
	out, err := bn128ScalarMul{}.Run(one)
	if err != nil {
		t.Fatalf("Run 1G: %v", err)
	}
	if want := concat(w(t, "01"), w(t, "02")); !bytes.Equal(out, want) {
		t.Fatalf("1*G = %x, want %x", out, want)
	}
	two := concat(w(t, "01"), w(t, "02"), w(t, "02"))
	out2, err := bn128ScalarMul{}.Run(two)
	if err != nil {
		t.Fatalf("Run 2G: %v", err)
	}
	if want := concat(w(t, twoGx), w(t, twoGy)); !bytes.Equal(out2, want) {
		t.Fatalf("2*G = %x\nwant   %x", out2, want)
	}
}

// TestBn128Pairing covers EIP-197: empty input is the empty product (1); e(G1,G2)·e(-G1,G2)
// cancels to 1 (true); and a single pair e(G1,G2) ≠ 1 (false).
func TestBn128Pairing(t *testing.T) {
	trueWord := w(t, "01")
	falseWord := make([]byte, 32)

	// Empty input -> 1.
	if out, err := (bn128Pairing{}).Run(nil); err != nil || !bytes.Equal(out, trueWord) {
		t.Fatalf("empty pairing = %x, err %v; want 1", out, err)
	}

	g2 := concat(w(t, g2xi), w(t, g2xr), w(t, g2yi), w(t, g2yr))
	pairPos := concat(w(t, "01"), w(t, "02"), g2)    // e(G1, G2)
	pairNeg := concat(w(t, "01"), w(t, negTwoY), g2) // e(-G1, G2)

	// Two cancelling pairs -> 1.
	out, err := bn128Pairing{}.Run(concat(pairPos, pairNeg))
	if err != nil {
		t.Fatalf("Run cancelling: %v", err)
	}
	if !bytes.Equal(out, trueWord) {
		t.Fatalf("e(G,G2)·e(-G,G2) = %x, want 1", out)
	}

	// Single non-trivial pairing -> 0.
	out1, err := bn128Pairing{}.Run(pairPos)
	if err != nil {
		t.Fatalf("Run single: %v", err)
	}
	if !bytes.Equal(out1, falseWord) {
		t.Fatalf("e(G,G2) = %x, want 0", out1)
	}

	// Length not a multiple of 192 -> hard failure.
	if _, err := (bn128Pairing{}).Run(make([]byte, 100)); err == nil {
		t.Fatal("non-multiple-of-192 input must fail")
	}
}

// TestBn128Reject verifies invalid points are rejected (hard failure): a G1 point off the
// curve and a coordinate >= the field modulus.
func TestBn128Reject(t *testing.T) {
	// (1,1) is not on y² = x³ + 3.
	offCurve := concat(w(t, "01"), w(t, "01"), w(t, "01"), w(t, "02"))
	if _, err := (bn128Add{}).Run(offCurve); err == nil {
		t.Fatal("off-curve point must fail")
	}
	// x = p (== modulus) is out of range.
	badCoord := concat(w(t, fieldP), w(t, "02"), w(t, "01"), w(t, "02"))
	if _, err := (bn128Add{}).Run(badCoord); err == nil {
		t.Fatal("coordinate >= p must fail")
	}
}

// TestBn128Energy pins the java-tron energy schedule and the allowTvmIstanbul repricing.
func TestBn128Energy(t *testing.T) {
	istanbul := VMConfig{AllowIstanbul: true}
	pre := VMConfig{}

	if g := (bn128Add{}).requiredEnergyCfg(nil, pre); g != 500 {
		t.Fatalf("add pre-Istanbul = %d, want 500", g)
	}
	if g := (bn128Add{}).requiredEnergyCfg(nil, istanbul); g != 150 {
		t.Fatalf("add Istanbul = %d, want 150", g)
	}
	if g := (bn128ScalarMul{}).requiredEnergyCfg(nil, pre); g != 40000 {
		t.Fatalf("mul pre-Istanbul = %d, want 40000", g)
	}
	if g := (bn128ScalarMul{}).requiredEnergyCfg(nil, istanbul); g != 6000 {
		t.Fatalf("mul Istanbul = %d, want 6000", g)
	}

	onePair := make([]byte, bn128PairSize)
	if g := (bn128Pairing{}).requiredEnergyCfg(onePair, pre); g != 80000+100000 {
		t.Fatalf("pairing(1) pre-Istanbul = %d, want 180000", g)
	}
	if g := (bn128Pairing{}).requiredEnergyCfg(onePair, istanbul); g != 34000+45000 {
		t.Fatalf("pairing(1) Istanbul = %d, want 79000", g)
	}
	if g := (bn128Pairing{}).requiredEnergyCfg(nil, pre); g != 100000 {
		t.Fatalf("pairing(empty) pre-Istanbul = %d, want 100000", g)
	}
	if g := (bn128Pairing{}).requiredEnergyCfg(nil, istanbul); g != 45000 {
		t.Fatalf("pairing(empty) Istanbul = %d, want 45000", g)
	}
}

// TestRunPrecompileConfigEnergy checks runPrecompile prices bn128 through configEnergy: the
// same call is out-of-energy before Istanbul but succeeds after the repricing.
func TestRunPrecompileConfigEnergy(t *testing.T) {
	in := concat(w(t, "01"), w(t, "02"), w(t, "01"), w(t, "02"))
	// Budget 400 < 500 (pre-Istanbul add) -> out of energy, all budget consumed.
	_, used, err := runPrecompile(bn128Add{}, in, 400, VMConfig{})
	if err != ErrOutOfEnergy || used != 400 {
		t.Fatalf("pre-Istanbul: used %d err %v, want 400 / ErrOutOfEnergy", used, err)
	}
	// Same budget, Istanbul add costs 150 -> runs, only 150 used.
	out, used, err := runPrecompile(bn128Add{}, in, 400, VMConfig{AllowIstanbul: true})
	if err != nil || used != 150 {
		t.Fatalf("Istanbul: used %d err %v, want 150 / nil", used, err)
	}
	if want := concat(w(t, twoGx), w(t, twoGy)); !bytes.Equal(out, want) {
		t.Fatalf("Istanbul add out = %x, want %x", out, want)
	}
}
