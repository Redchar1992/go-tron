package tvm

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// blake2F state/message shared by the EIP-152 vectors: h is the same 64-byte chaining value
// and m is "abc" right-padded to 128 bytes, with the offset counter t = 3.
const (
	blake2fH = "48c9bdf267e6096a3ba7ca8485ae67bb2bf894fe72f36e3cf1361d5f3af54fa5" +
		"d182e6ad7f520e511f6c3e2b8c68059b6bbd41fbabd9831f79217e1319cde05b"
	blake2fM = "616263" // "abc"; padded to 128 bytes below
)

// buildBlake2FInput assembles the 213-byte EIP-152 input: rounds(4, BE) ‖ h(64) ‖ m(128) ‖
// t0(8, LE) ‖ t1(8, LE) ‖ f(1).
func buildBlake2FInput(t *testing.T, rounds uint32, final byte) []byte {
	t.Helper()
	h, err := hex.DecodeString(blake2fH)
	if err != nil {
		t.Fatal(err)
	}
	m, err := hex.DecodeString(blake2fM + strings.Repeat("00", 128-len(blake2fM)/2))
	if err != nil {
		t.Fatal(err)
	}
	in := make([]byte, 0, blake2FInputLen)
	var r [4]byte
	binary.BigEndian.PutUint32(r[:], rounds)
	in = append(in, r[:]...)
	in = append(in, h...)
	in = append(in, m...)
	var tbuf [16]byte
	binary.LittleEndian.PutUint64(tbuf[0:8], 3) // t0 = 3
	in = append(in, tbuf[:]...)
	in = append(in, final)
	if len(in) != blake2FInputLen {
		t.Fatalf("built input len = %d, want %d", len(in), blake2FInputLen)
	}
	return in
}

// TestBlake2FVectors runs the canonical EIP-152 F-compression vectors (4–7).
func TestBlake2FVectors(t *testing.T) {
	cases := []struct {
		name   string
		rounds uint32
		final  byte
		want   string
	}{
		{"eip152-4-rounds0", 0, 1,
			"08c9bcf367e6096a3ba7ca8485ae67bb2bf894fe72f36e3cf1361d5f3af54fa5" +
				"d282e6ad7f520e511f6c3e2b8c68059b9442be0454267ce079217e1319cde05b"},
		{"eip152-5-rounds12", 12, 1,
			"ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d1" +
				"7d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"},
		{"eip152-6-final0", 12, 0,
			"75ab69d3190a562c51aef8d88f1c2775876944407270c42c9844252c26d28752" +
				"98743e7f6d5ea2f2d3e8d226039cd31b4e426ac4f2d3d666a610c2116fde4735"},
		{"eip152-7-rounds1", 1, 1,
			"b63a380cb2897d521994a85234ee2c181b5f844d2c624c002677e9703449d2fb" +
				"a551b3a8333bcdf5f2f7e08993d53923de3d64fcc68c034e717b9293fed7a421"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := buildBlake2FInput(t, tc.rounds, tc.final)
			if g := (blake2F{}).RequiredEnergy(in); g != uint64(tc.rounds) {
				t.Fatalf("energy = %d, want %d (= rounds)", g, tc.rounds)
			}
			out, err := blake2F{}.Run(in)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			want, _ := hex.DecodeString(tc.want)
			if !bytes.Equal(out, want) {
				t.Fatalf("out = %x\nwant  %x", out, want)
			}
		})
	}
}

// TestBlake2FMalformed checks the hard-failure paths: a wrong length and an out-of-range
// final flag both fail the call (java-tron returns (false, …)) and, for energy, an input
// that is not exactly 213 bytes or whose final byte has extra bits set costs 0.
func TestBlake2FMalformed(t *testing.T) {
	// Wrong length -> Run fails.
	if _, err := (blake2F{}).Run(make([]byte, 212)); err == nil {
		t.Fatal("212-byte input must fail")
	}
	// Final flag = 2 (neither 0 nor 1) -> Run fails, energy = 0.
	bad := buildBlake2FInput(t, 12, 2)
	if g := (blake2F{}).RequiredEnergy(bad); g != 0 {
		t.Fatalf("energy for bad final flag = %d, want 0", g)
	}
	if _, err := (blake2F{}).Run(bad); err == nil {
		t.Fatal("final flag = 2 must fail")
	}
}

// TestBlake2FGating verifies blake2F is only reachable once allowTvmCompatibleEvm
// (VMConfig.Forward6364) is active — matching java-tron getContractForAddr.
func TestBlake2FGating(t *testing.T) {
	addr := precompileAddr(0x20009)
	if pc := lookupPrecompile(addr, VMConfig{Forward6364: false}); pc != nil {
		t.Fatal("blake2F must be absent before allowTvmCompatibleEvm")
	}
	if pc := lookupPrecompile(addr, VMConfig{Forward6364: true}); pc == nil {
		t.Fatal("blake2F must be present after allowTvmCompatibleEvm")
	}
}
