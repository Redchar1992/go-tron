package tvm

import (
	"bytes"
	"testing"

	"github.com/holiman/uint256"
)

// ---- bytecode assembly helpers ----

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func push1(b byte) []byte   { return []byte{0x60, b} }
func push3(v uint32) []byte { return []byte{0x62, byte(v >> 16), byte(v >> 8), byte(v)} }

// push20 pushes a 20-byte address body (the callee address without its 0x41 prefix).
func push20(addr21 []byte) []byte { return append([]byte{0x73}, addr21[1:21]...) }

const (
	opMSTORE = 0x52
	opRETURN = 0xf3
	opSTOP   = 0x00
	opCALL   = 0xf1
	opPOP    = 0x50
)

// retMem returns bytecode that RETURNs memory[0:32].
func retMem() []byte { return cat(push1(32), push1(0), []byte{opRETURN}) }

// TestCallReturnsCalleeData: caller CALLs a callee that returns 42, copies the callee's
// output into its own out buffer, and returns it.
func TestCallReturnsCalleeData(t *testing.T) {
	s := NewMemStateDB()
	callee := addr(0x02)
	// callee: PUSH1 42; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
	s.SetCode(callee, cat(push1(42), push1(0), []byte{opMSTORE}, retMem()))

	caller := addr(0x01)
	// CALL(gas, callee, value=0, in=(0,0), out=(0,32)); then RETURN mem[0:32].
	code := cat(
		push1(32), // outSize
		push1(0),  // outOff
		push1(0),  // inSize
		push1(0),  // inOff
		push1(0),  // value
		push20(callee),
		push3(100000), // gas
		[]byte{opCALL},
		[]byte{opPOP},
		retMem(),
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 42 {
		t.Fatalf("return = %x, want ...2a", r.Return)
	}
}

// TestCallValueTransfer moves TRX from caller to callee.
func TestCallValueTransfer(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	s.SetCode(callee, []byte{opSTOP})
	s.SetCode(caller, nil)
	s.AddBalance(caller, uint256.NewInt(1000))

	code := cat(
		push1(0),   // outSize
		push1(0),   // outOff
		push1(0),   // inSize
		push1(0),   // inOff
		push1(100), // value
		push20(callee),
		push3(100000), // gas
		[]byte{opCALL, opSTOP},
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if got := s.GetBalance(callee).Uint64(); got != 100 {
		t.Fatalf("callee balance = %d, want 100", got)
	}
	if got := s.GetBalance(caller).Uint64(); got != 900 {
		t.Fatalf("caller balance = %d, want 900", got)
	}
}

// TestStaticCallWriteProtection: a callee that SSTOREs faults under STATICCALL; the call
// reports failure (0) and no storage is written.
func TestStaticCallWriteProtection(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	// callee: PUSH1 1; PUSH1 0; SSTORE; STOP
	s.SetCode(callee, []byte{0x60, 1, 0x60, 0, 0x55, opSTOP})

	// STATICCALL(gas, callee, in=(0,0), out=(0,0)); store success at slot 0; RETURN it.
	code := cat(
		push1(0), // outSize
		push1(0), // outOff
		push1(0), // inSize
		push1(0), // inOff
		push20(callee),
		push3(100000), // gas
		[]byte{0xfa},  // STATICCALL
		push1(0), []byte{opMSTORE},
		retMem(),
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 0 {
		t.Fatalf("static call success = %x, want 0 (write rejected)", r.Return)
	}
	if _, present := s.GetStorage(callee, [32]byte{}); present {
		t.Fatal("callee storage must be unchanged after static-call rejection")
	}
}

// TestCallRevertRollsBack: a callee that writes storage then REVERTs has its write rolled
// back, and the CALL reports failure (0).
func TestCallRevertRollsBack(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)
	callee := addr(0x02)
	// callee: PUSH1 99; PUSH1 0; SSTORE; PUSH1 0; PUSH1 0; REVERT
	s.SetCode(callee, []byte{0x60, 99, 0x60, 0, 0x55, 0x60, 0, 0x60, 0, 0xfd})

	code := cat(
		push1(0), push1(0), push1(0), push1(0), push1(0), // out/in/value
		push20(callee), push3(100000),
		[]byte{opCALL},
		push1(0), []byte{opMSTORE},
		retMem(),
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if lastByte(r.Return) != 0 {
		t.Fatalf("reverting call success = %x, want 0", r.Return)
	}
	if _, present := s.GetStorage(callee, [32]byte{}); present {
		t.Fatal("callee storage write must roll back on REVERT")
	}
}

// TestCreateDeploysCode: CREATE runs init code that returns a 1-byte runtime ([STOP]) and
// the new account ends up holding that code; the returned address is non-zero.
func TestCreateDeploysCode(t *testing.T) {
	s := NewMemStateDB()
	caller := addr(0x01)

	// init code: PUSH1 0; PUSH1 0; MSTORE8; PUSH1 1; PUSH1 0; RETURN  -> returns [0x00]
	initCode := []byte{0x60, 0, 0x60, 0, 0x53, 0x60, 1, 0x60, 0, opRETURN}
	// 32-byte word holding initCode right-aligned (last len bytes).
	var word [32]byte
	copy(word[32-len(initCode):], initCode)

	code := cat(
		append([]byte{0x7f}, word[:]...), // PUSH32 word
		push1(0), []byte{opMSTORE},       // mem[0:32] = word
		push1(byte(len(initCode))),    // inSize
		push1(byte(32-len(initCode))), // inOff (where initCode starts)
		push1(0),                      // value
		[]byte{0xf0},                  // CREATE
		push1(0), []byte{opMSTORE},    // store returned address word
		retMem(),
	)
	s.SetCode(caller, code)
	r := runOn(s, caller, code, nil, 1_000_000)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	// returned address word: last 20 bytes must be non-zero.
	if bytes.Equal(r.Return[12:32], make([]byte, 20)) {
		t.Fatalf("CREATE returned zero address: %x", r.Return)
	}
	// The new contract (createAddress(nil, nonce=0)) must hold the deployed runtime [0x00].
	newAddr := createAddress(nil, 0)
	if got := s.GetCode(newAddr); !bytes.Equal(got, []byte{0x00}) {
		t.Fatalf("deployed code = %x, want 00", got)
	}
}

// TestCallDepthGuard verifies the depth-64 limit at the doCall boundary (white-box).
func TestCallDepthGuard(t *testing.T) {
	s := NewMemStateDB()
	callee := addr(0x02)
	s.SetCode(callee, []byte{opSTOP}) // succeeds, returns nothing
	evm := NewEVM(s, BlockContext{}, VMConfig{})
	in := &interpreter{evm: evm, meter: newEnergyMeter(1_000_000), table: opTable()}
	sc := &scope{contract: &Contract{Self: addr(0x01)}, stack: newStack(), mem: newMemory()}

	evm.depth = maxCallDepth // at the limit: the call must not run
	if err := evm.doCall(in, sc, kindCall, callee, nil, 100000, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	top, _ := sc.stack.pop()
	if !top.IsZero() {
		t.Fatal("call at depth limit must push 0")
	}

	evm.depth = 0 // below the limit: the call runs and succeeds
	if err := evm.doCall(in, sc, kindCall, callee, nil, 100000, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	top, _ = sc.stack.pop()
	if top.IsZero() {
		t.Fatal("call below depth limit must push 1 (success)")
	}
}
