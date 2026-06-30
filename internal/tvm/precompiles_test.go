package tvm

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck
)

func TestIdentityPrecompile(t *testing.T) {
	pc := dataCopy{}
	in := []byte("hello, tron")
	out, err := pc.Run(in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("identity = %x, err %v", out, err)
	}
	// 11 bytes -> 1 word -> 15 + 3 = 18
	if g := pc.RequiredEnergy(in); g != 18 {
		t.Fatalf("identity energy = %d, want 18", g)
	}
}

func TestSha256Precompile(t *testing.T) {
	pc := sha256Hash{}
	in := []byte("abc")
	want := sha256.Sum256(in)
	out, _ := pc.Run(in)
	if !bytes.Equal(out, want[:]) {
		t.Fatalf("sha256 = %x, want %x", out, want)
	}
	if g := pc.RequiredEnergy(in); g != 60+12 {
		t.Fatalf("sha256 energy = %d, want 72", g)
	}
}

// TestTronRipemd160 verifies TRON's 0x03 is sha256(sha256(x)[:20]), NOT RIPEMD-160.
func TestTronRipemd160(t *testing.T) {
	in := []byte("abc")
	orig := sha256.Sum256(in)
	second := sha256.Sum256(orig[:20])
	out, _ := tronRipemd160{}.Run(in)
	if !bytes.Equal(out, second[:]) {
		t.Fatalf("tron ripemd160 = %x, want %x", out, second)
	}
	// It must NOT equal the real RIPEMD-160.
	r := ripemd160.New()
	r.Write(in)
	var real32 [32]byte
	copy(real32[12:], r.Sum(nil))
	if bytes.Equal(out, real32[:]) {
		t.Fatal("TRON 0x03 must differ from real RIPEMD-160")
	}
}

// TestEthRipemd160 verifies 0x20003 is the real RIPEMD-160, left-padded.
func TestEthRipemd160(t *testing.T) {
	in := []byte("abc")
	r := ripemd160.New()
	r.Write(in)
	want := make([]byte, 32)
	copy(want[12:], r.Sum(nil))
	out, _ := ethRipemd160{}.Run(in)
	if !bytes.Equal(out, want) {
		t.Fatalf("eth ripemd160 = %x, want %x", out, want)
	}
}

// modexpInput builds a modexp calldata for equal-length (len bytes) base/exp/mod.
func modexpInput(length int, base, exp, mod byte) []byte {
	in := make([]byte, 96+3*length)
	in[31] = byte(length) // baseLen
	in[63] = byte(length) // expLen
	in[95] = byte(length) // modLen
	in[96+length-1] = base
	in[96+2*length-1] = exp
	in[96+3*length-1] = mod
	return in
}

func TestModExpPrecompile(t *testing.T) {
	pc := bigModExp{}
	// 3^2 mod 5 = 4, with 32-byte lengths.
	in := modexpInput(32, 3, 2, 5)
	out, err := pc.Run(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 32 || out[31] != 4 {
		t.Fatalf("3^2 mod 5 = %x, want ...04", out)
	}
	// multComplexity(32)=1024; adjExpLen(exp=2)=1; cost = 1024*1/20 = 51.
	if g := pc.RequiredEnergy(in); g != 51 {
		t.Fatalf("modexp energy = %d, want 51", g)
	}
}

func TestModExpZeroModulus(t *testing.T) {
	// modulus 0 -> empty output (TRON deviation).
	in := modexpInput(1, 3, 2, 0)
	out, _ := bigModExp{}.Run(in)
	if len(out) != 0 {
		t.Fatalf("zero-modulus modexp = %x, want empty", out)
	}
}

// TestPrecompileViaCall dispatches sha256 (0x02) through a CALL and returns its output.
func TestPrecompileViaCall(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x31)

	// Put a 32-byte input word in memory, CALL 0x02 with in=(0,32) out=(0,32), RETURN it.
	var input [32]byte
	input[31] = 0xab
	want := sha256.Sum256(input[:])

	code := cat(
		append([]byte{0x7f}, input[:]...), // PUSH32 input
		push1(0), []byte{opMSTORE},        // mem[0:32] = input
		push1(32), // outSize
		push1(0),  // outOff
		push1(32), // inSize
		push1(0),  // inOff
		push1(0),  // value
		append([]byte{0x73}, precompileAddr(0x02)[1:21]...), // PUSH20 sha256 addr body
		push3(100000), // gas
		[]byte{opCALL, opPOP},
		retMem(),
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if !bytes.Equal(r.Return, want[:]) {
		t.Fatalf("call sha256 = %x, want %x", r.Return, want)
	}
}
