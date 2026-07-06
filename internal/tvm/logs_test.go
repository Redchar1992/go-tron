package tvm

import (
	"bytes"
	"testing"

	"github.com/holiman/uint256"
)

// runEVM executes code at self and returns the EVM (to read Logs()) plus the result.
func runEVM(state *MemStateDB, self, code []byte, limit uint64, cfg VMConfig) (*EVM, *Result) {
	c := &Contract{
		Self: self, CodeAddr: self, Caller: addr(0x02), Origin: addr(0x02),
		Value: new(uint256.Int), Code: code,
	}
	evm := NewEVM(state, BlockContext{ChainID: uint256From(728126428)}, cfg)
	return evm, evm.Execute(c, nil, limit)
}

// push32 pushes a 32-byte word.
func push32(b []byte) []byte { return append([]byte{0x7f}, b...) }

const opLOG0 = 0xa0

// TestLogEmit: LOG2 records the emitting address, both topics (in order), and the data.
func TestLogEmit(t *testing.T) {
	s := NewMemStateDB()
	self := addr(0x01)
	data := bytes.Repeat([]byte{0x11}, 32)
	topic0 := bytes.Repeat([]byte{0xAA}, 32)
	topic1 := bytes.Repeat([]byte{0xBB}, 32)
	// mem[0:32]=data; then LOG2(offset=0, size=32, topic0, topic1)
	code := cat(
		push32(data), push1(0), []byte{opMSTORE},
		push32(topic1), push32(topic0), push1(32), push1(0), []byte{0xa2}, // LOG2
		[]byte{opSTOP},
	)
	s.SetCode(self, code)
	evm, r := runEVM(s, self, code, 100000, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	logs := evm.Logs()
	if len(logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(logs))
	}
	l := logs[0]
	if !bytes.Equal(l.Address, self) {
		t.Fatalf("log address = %x, want %x", l.Address, self)
	}
	if len(l.Topics) != 2 || !bytes.Equal(l.Topics[0][:], topic0) || !bytes.Equal(l.Topics[1][:], topic1) {
		t.Fatalf("topics = %x", l.Topics)
	}
	if !bytes.Equal(l.Data, data) {
		t.Fatalf("data = %x, want %x", l.Data, data)
	}
}

// TestLogEnergy: LOG0 over empty data costs exactly 375 (+ the two PUSHes).
func TestLogEnergy(t *testing.T) {
	s := NewMemStateDB()
	self := addr(0x01)
	// PUSH1 0 (size); PUSH1 0 (offset); LOG0; STOP  -> 3 + 3 + 375 + 0
	code := cat(push1(0), push1(0), []byte{opLOG0}, []byte{opSTOP})
	s.SetCode(self, code)
	evm, r := runEVM(s, self, code, 100000, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if r.EnergyUsed != 381 {
		t.Fatalf("energy = %d, want 381 (3+3+375)", r.EnergyUsed)
	}
	if len(evm.Logs()) != 1 {
		t.Fatalf("want 1 log, got %d", len(evm.Logs()))
	}
}

// TestLogStaticCallRejected: LOG under a STATICCALL faults (state change) — the call
// reports 0 and no log survives.
func TestLogStaticCallRejected(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	// callee: PUSH1 0; PUSH1 0; LOG0; STOP
	s.SetCode(callee, cat(push1(0), push1(0), []byte{opLOG0}, []byte{opSTOP}))
	// STATICCALL(gas, callee, in=(0,0), out=(0,0)); MSTORE result; RETURN it.
	code := cat(
		push1(0), push1(0), push1(0), push1(0), // outSize, outOff, inSize, inOff
		push20(callee), push3(100000),
		[]byte{0xfa}, // STATICCALL
		push1(0), []byte{opMSTORE},
		retMem(),
	)
	s.SetCode(caller, code)
	evm, r := runEVM(s, caller, code, 1_000_000, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 0 {
		t.Fatalf("static-call LOG should fail (0), got %x", r.Return)
	}
	if len(evm.Logs()) != 0 {
		t.Fatalf("no log may survive a rejected static LOG, got %d", len(evm.Logs()))
	}
}

// TestLogDiscardedOnNestedRevert: a callee that LOGs then REVERTs contributes no log.
func TestLogDiscardedOnNestedRevert(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	// callee: PUSH1 0; PUSH1 0; LOG0; PUSH1 0; PUSH1 0; REVERT
	s.SetCode(callee, cat(push1(0), push1(0), []byte{opLOG0}, push1(0), push1(0), []byte{0xfd}))
	// caller: CALL callee; POP; STOP
	code := cat(
		push1(0), push1(0), push1(0), push1(0), push1(0), // outSize,outOff,inSize,inOff,value
		push20(callee), push3(100000),
		[]byte{opCALL}, []byte{opPOP}, []byte{opSTOP},
	)
	s.SetCode(caller, code)
	evm, r := runEVM(s, caller, code, 1_000_000, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if len(evm.Logs()) != 0 {
		t.Fatalf("nested-revert logs must be discarded, got %d", len(evm.Logs()))
	}
}

// TestLogAccumulatesAcrossFrames: a successful callee's log plus the caller's own log both
// survive, in emission order (callee first).
func TestLogAccumulatesAcrossFrames(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	// callee: PUSH1 0; PUSH1 0; LOG0; STOP  (success)
	s.SetCode(callee, cat(push1(0), push1(0), []byte{opLOG0}, []byte{opSTOP}))
	// caller: CALL callee; POP; LOG0; STOP
	code := cat(
		push1(0), push1(0), push1(0), push1(0), push1(0),
		push20(callee), push3(100000),
		[]byte{opCALL}, []byte{opPOP},
		push1(0), push1(0), []byte{opLOG0},
		[]byte{opSTOP},
	)
	s.SetCode(caller, code)
	evm, r := runEVM(s, caller, code, 1_000_000, VMConfig{})
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	logs := evm.Logs()
	if len(logs) != 2 {
		t.Fatalf("want 2 logs (callee + caller), got %d", len(logs))
	}
	if !bytes.Equal(logs[0].Address, callee) || !bytes.Equal(logs[1].Address, caller) {
		t.Fatalf("log order wrong: %x then %x", logs[0].Address, logs[1].Address)
	}
}
