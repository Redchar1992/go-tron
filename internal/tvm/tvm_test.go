package tvm

import (
	"bytes"
	"errors"
	"testing"

	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// run executes code in a fresh frame with the given energy limit and default context.
func run(code []byte, limit uint64) *Result {
	c := &Contract{
		Self:    make([]byte, 20),
		Caller:  make([]byte, 20),
		Origin:  make([]byte, 20),
		Value:   new(uint256.Int),
		Storage: NewMemStorage(),
		Code:    code,
	}
	return Run(c, nil, limit, BlockContext{ChainID: uint256From(728126428)}, VMConfig{})
}

// wordTail returns the last byte of a 32-byte return word.
func lastByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[len(b)-1]
}

// TestArithmeticAndEnergy: (5 + 3) stored and returned, with exact energy.
//
//	PUSH1 5; PUSH1 3; ADD; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
//	energy = 3+3+3 (push/push/add) + 3 (push0) + 4 (MSTORE: 1 + memExpand 0->32 =3)
//	         + 3 + 3 (push32/push0) + 0 (RETURN, mem already 32) = 22
func TestArithmeticAndEnergy(t *testing.T) {
	code := []byte{0x60, 5, 0x60, 3, 0x01, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if len(r.Return) != 32 || lastByte(r.Return) != 8 {
		t.Fatalf("return = %x, want ...08", r.Return)
	}
	if r.EnergyUsed != 22 {
		t.Fatalf("energy = %d, want 22", r.EnergyUsed)
	}
}

// TestStorageSetEnergy: SSTORE a new non-zero slot (20000), SLOAD it back (50).
//
//	PUSH1 42; PUSH1 1; SSTORE; PUSH1 1; SLOAD; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
//	energy = 3+3 + 20000 + 3 + 50 + 3 + 4 + 3 + 3 + 0 = 20072
func TestStorageSetEnergy(t *testing.T) {
	code := []byte{0x60, 42, 0x60, 1, 0x55, 0x60, 1, 0x54, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	c := &Contract{
		Self: make([]byte, 20), Caller: make([]byte, 20), Origin: make([]byte, 20),
		Value: new(uint256.Int), Storage: NewMemStorage(), Code: code,
	}
	r := Run(c, nil, 100000, BlockContext{}, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 42 {
		t.Fatalf("return = %x, want ...2a", r.Return)
	}
	if r.EnergyUsed != 20072 {
		t.Fatalf("energy = %d, want 20072", r.EnergyUsed)
	}
	// And the slot persisted.
	var key [32]byte
	key[31] = 1
	v, present := c.Storage.Load(key)
	if !present || v[31] != 42 {
		t.Fatalf("storage[1] = %x present=%v, want 42", v, present)
	}
}

// TestKeccak256 hashes 32 zero bytes and returns the digest, with exact energy.
//
//	PUSH1 32; PUSH1 0; SHA3; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
//	energy = 3+3 + (30 + 6*1 + memExpand 0->32 = 3) + 3 + 1 + 3 + 3 + 0 = 55
func TestKeccak256(t *testing.T) {
	code := []byte{0x60, 32, 0x60, 0, 0x20, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	want := crypto.Keccak256(make([]byte, 32))
	if !bytes.Equal(r.Return, want) {
		t.Fatalf("sha3 = %x, want %x", r.Return, want)
	}
	if r.EnergyUsed != 55 {
		t.Fatalf("energy = %d, want 55", r.EnergyUsed)
	}
}

// TestComparisonLT: 2 < 3 == 1.
//
//	PUSH1 3; PUSH1 2; LT  -> top=2(a), next=3(b); a<b -> 1
func TestComparisonLT(t *testing.T) {
	code := []byte{0x60, 3, 0x60, 2, 0x10, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 1 {
		t.Fatalf("2<3 = %x, want 1", r.Return)
	}
}

// TestDupSwap exercises DUP1 and SWAP1 producing a known value.
//
//	PUSH1 7; DUP1; ADD -> 14
func TestDupSwap(t *testing.T) {
	code := []byte{0x60, 7, 0x80, 0x01, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 14 {
		t.Fatalf("7+7 = %x, want 14", r.Return)
	}
}

// TestJumpControlFlow: a JUMP must skip the INVALID byte and land on JUMPDEST.
//
//	PUSH1 4; JUMP; INVALID; JUMPDEST; STOP   (pc4 = JUMPDEST)
//	energy = 3 (push) + 8 (JUMP) + 1 (JUMPDEST) + 0 (STOP) = 12
func TestJumpControlFlow(t *testing.T) {
	code := []byte{0x60, 4, 0x56, 0xfe, 0x5b, 0x00}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("jump err = %v", r.Err)
	}
	if r.EnergyUsed != 12 {
		t.Fatalf("energy = %d, want 12", r.EnergyUsed)
	}
}

func TestBadJumpConsumesAllEnergy(t *testing.T) {
	// PUSH1 2; JUMP; STOP -> jumps to pc2 (JUMP op, not a JUMPDEST) -> fault.
	code := []byte{0x60, 2, 0x56, 0x00}
	r := run(code, 100000)
	if !errors.Is(r.Err, ErrBadJumpDest) {
		t.Fatalf("err = %v, want ErrBadJumpDest", r.Err)
	}
	if r.EnergyUsed != 100000 {
		t.Fatalf("energy = %d, want all (100000)", r.EnergyUsed)
	}
}

func TestStackUnderflowFaults(t *testing.T) {
	r := run([]byte{0x01}, 100000) // ADD with empty stack
	if !errors.Is(r.Err, ErrStackUnderflow) {
		t.Fatalf("err = %v, want ErrStackUnderflow", r.Err)
	}
}

func TestOutOfEnergy(t *testing.T) {
	// Arithmetic vector needs 22; give it 10.
	code := []byte{0x60, 5, 0x60, 3, 0x01, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	r := run(code, 10)
	if !errors.Is(r.Err, ErrOutOfEnergy) {
		t.Fatalf("err = %v, want ErrOutOfEnergy", r.Err)
	}
	if r.EnergyUsed != 10 {
		t.Fatalf("energy = %d, want limit 10", r.EnergyUsed)
	}
}

func TestRevert(t *testing.T) {
	// PUSH1 0; PUSH1 0; REVERT -> reverted, no error, energy not all-burned.
	code := []byte{0x60, 0, 0x60, 0, 0xfd}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("revert should not set Err, got %v", r.Err)
	}
	if !r.Reverted {
		t.Fatal("expected Reverted")
	}
	if r.EnergyUsed != 6 { // two PUSH1 (3+3); REVERT mem cost 0
		t.Fatalf("energy = %d, want 6", r.EnergyUsed)
	}
}

// TestMemoryExpansionEnergy stores at a high offset to exercise quadratic expansion.
//
//	PUSH1 0; PUSH2 0x0400; MSTORE; STOP
//	MSTORE at offset 1024 covers bytes [1024,1056) -> 33 words. f(33)=3*33+33*33/512=99+2=101.
//	MSTORE energy = 1 (special) + 101 = 102. plus PUSH1(3)+PUSH2(3) = 108.
func TestMemoryExpansionEnergy(t *testing.T) {
	code := []byte{0x60, 0, 0x61, 0x04, 0x00, 0x52, 0x00}
	r := run(code, 100000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if r.EnergyUsed != 108 {
		t.Fatalf("energy = %d, want 108", r.EnergyUsed)
	}
}
