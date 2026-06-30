package tvm

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// TestEcrecover signs a hash with a fixed key, then recovers the signer address through
// the precompile and checks it equals keccak(pubXY)[12:32].
func TestEcrecover(t *testing.T) {
	privBytes := make([]byte, 32)
	privBytes[31] = 0x2a // deterministic non-trivial key
	priv := secp256k1.PrivKeyFromBytes(privBytes)

	hash := sha256.Sum256([]byte("tron ecrecover vector"))
	// [recoveryCode(27/28) || R(32) || S(32)]
	sig := ecdsa.SignCompact(priv, hash[:], false)
	v, r, s := sig[0], sig[1:33], sig[33:65]

	input := make([]byte, 128)
	copy(input[0:32], hash[:])
	input[63] = v
	copy(input[64:96], r)
	copy(input[96:128], s)

	out, err := ecrecoverRun(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 32 {
		t.Fatalf("output len = %d, want 32", len(out))
	}

	// Expected address: last 20 bytes of keccak256 of the 64-byte uncompressed pubkey.
	pub := priv.PubKey().SerializeUncompressed()
	want := crypto.Keccak256(pub[1:])
	if !bytes.Equal(out[12:32], want[12:32]) {
		t.Fatalf("recovered addr = %x, want %x", out[12:32], want[12:32])
	}
	if !bytes.Equal(out[0:12], make([]byte, 12)) {
		t.Fatalf("output not left-padded: %x", out)
	}
}

func TestEcrecoverBadV(t *testing.T) {
	input := make([]byte, 128)
	input[63] = 26 // invalid v (not 27/28)
	out, err := ecrecoverRun(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("bad-v ecrecover = %x, want empty", out)
	}
}

// TestEcrecoverViaCall dispatches ecrecover through a CALL and returns the address word.
func TestEcrecoverViaCall(t *testing.T) {
	privBytes := make([]byte, 32)
	privBytes[31] = 0x07
	priv := secp256k1.PrivKeyFromBytes(privBytes)
	hash := sha256.Sum256([]byte("via call"))
	sig := ecdsa.SignCompact(priv, hash[:], false)

	input := make([]byte, 128)
	copy(input[0:32], hash[:])
	input[63] = sig[0]
	copy(input[64:96], sig[1:33])
	copy(input[96:128], sig[33:65])

	s := NewMemStateDB()
	caller := addr(0x41)
	// Store the 128-byte input across 4 memory words, then CALL 0x01 with in=(0,128) out=(0,32).
	var code []byte
	for i := 0; i < 4; i++ {
		var w [32]byte
		copy(w[:], input[i*32:(i+1)*32])
		code = append(code, append([]byte{0x7f}, w[:]...)...) // PUSH32 word
		code = append(code, push1(byte(i*32))...)             // offset
		code = append(code, opMSTORE)
	}
	code = append(code, cat(
		push1(32),  // outSize
		push1(0),   // outOff
		push1(128), // inSize
		push1(0),   // inOff
		push1(0),   // value
		append([]byte{0x73}, precompileAddr(0x01)[1:21]...), // ecrecover addr body
		push3(100000),
		[]byte{opCALL, opPOP},
		retMem(),
	)...)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	pub := priv.PubKey().SerializeUncompressed()
	want := crypto.Keccak256(pub[1:])
	if !bytes.Equal(r.Return[12:32], want[12:32]) {
		t.Fatalf("call ecrecover = %x, want %x", r.Return[12:32], want[12:32])
	}
}
