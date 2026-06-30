package tvm

import (
	"sync"

	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// Energy tiers (java-tron EnergyCost). These are the whole cost of a fixed-cost op.
const (
	gasZero    = 0
	gasSpecial = 1
	gasBase    = 2
	gasVeryLow = 3
	gasLow     = 5
	gasMid     = 8
	gasHigh    = 10
)

// operation is one entry in the jump table: its executor, its energy-cost function, and
// its stack effect. halts ends the frame; jumps means exec sets pc itself.
type operation struct {
	exec  func(in *interpreter, sc *scope) error
	gas   func(in *interpreter, sc *scope) (uint64, error)
	pop   int
	push  int
	halts bool
	jumps bool
}

// constGas returns a cost function for a fixed-cost op.
func constGas(c uint64) func(*interpreter, *scope) (uint64, error) {
	return func(*interpreter, *scope) (uint64, error) { return c, nil }
}

var (
	builtTable     *[256]*operation
	buildTableOnce sync.Once
)

// opTable builds the jump table once and caches it (concurrency-safe).
func opTable() *[256]*operation {
	buildTableOnce.Do(buildTable)
	return builtTable
}

func buildTable() {
	t := new([256]*operation)
	reg := func(op OpCode, e func(*interpreter, *scope) error, g func(*interpreter, *scope) (uint64, error), pop, push int) {
		t[op] = &operation{exec: e, gas: g, pop: pop, push: push}
	}

	// 0x00 — stop & arithmetic.
	t[STOP] = &operation{exec: opStop, gas: constGas(gasZero), halts: true}
	reg(ADD, opAdd, constGas(gasVeryLow), 2, 1)
	reg(MUL, opMul, constGas(gasLow), 2, 1)
	reg(SUB, opSub, constGas(gasVeryLow), 2, 1)
	reg(DIV, opDiv, constGas(gasLow), 2, 1)
	reg(SDIV, opSdiv, constGas(gasLow), 2, 1)
	reg(MOD, opMod, constGas(gasLow), 2, 1)
	reg(SMOD, opSmod, constGas(gasLow), 2, 1)
	reg(ADDMOD, opAddmod, constGas(gasMid), 3, 1)
	reg(MULMOD, opMulmod, constGas(gasMid), 3, 1)
	reg(EXP, opExp, gasExp, 2, 1)
	reg(SIGNEXTEND, opSignextend, constGas(gasLow), 2, 1)

	// 0x10 — comparison & bitwise.
	reg(LT, opLt, constGas(gasVeryLow), 2, 1)
	reg(GT, opGt, constGas(gasVeryLow), 2, 1)
	reg(SLT, opSlt, constGas(gasVeryLow), 2, 1)
	reg(SGT, opSgt, constGas(gasVeryLow), 2, 1)
	reg(EQ, opEq, constGas(gasVeryLow), 2, 1)
	reg(ISZERO, opIszero, constGas(gasVeryLow), 1, 1)
	reg(AND, opAnd, constGas(gasVeryLow), 2, 1)
	reg(OR, opOr, constGas(gasVeryLow), 2, 1)
	reg(XOR, opXor, constGas(gasVeryLow), 2, 1)
	reg(NOT, opNot, constGas(gasVeryLow), 1, 1)
	reg(BYTE, opByte, constGas(gasVeryLow), 2, 1)
	reg(SHL, opShl, constGas(gasVeryLow), 2, 1)
	reg(SHR, opShr, constGas(gasVeryLow), 2, 1)
	reg(SAR, opSar, constGas(gasVeryLow), 2, 1)

	// 0x20 — KECCAK256.
	reg(KECCAK256, opKeccak256, gasKeccak256, 2, 1)

	// 0x30 — environment (context-free subset).
	reg(ADDRESS, opAddress, constGas(gasBase), 0, 1)
	reg(ORIGIN, opOrigin, constGas(gasBase), 0, 1)
	reg(CALLER, opCaller, constGas(gasBase), 0, 1)
	reg(CALLVALUE, opCallvalue, constGas(gasBase), 0, 1)
	reg(CALLDATALOAD, opCalldataload, constGas(gasVeryLow), 1, 1)
	reg(CALLDATASIZE, opCalldatasize, constGas(gasBase), 0, 1)
	reg(CALLDATACOPY, opCalldatacopy, gasCopy, 3, 0)
	reg(CODESIZE, opCodesize, constGas(gasBase), 0, 1)
	reg(CODECOPY, opCodecopy, gasCopy, 3, 0)
	reg(GASPRICE, opGasprice, constGas(gasBase), 0, 1)
	reg(RETURNDATASIZE, opReturndatasize, constGas(gasBase), 0, 1)
	reg(RETURNDATACOPY, opReturndatacopy, gasReturndatacopy, 3, 0)

	// account access (cross-contract state).
	reg(BALANCE, opBalance, constGas(20), 1, 1)
	reg(EXTCODESIZE, opExtcodesize, constGas(20), 1, 1)
	reg(EXTCODEHASH, opExtcodehash, constGas(400), 1, 1)
	reg(EXTCODECOPY, opExtcodecopy, gasExtcodecopy, 4, 0)
	reg(SELFBALANCE, opSelfbalance, constGas(gasLow), 0, 1)

	// 0x40 — block context.
	reg(COINBASE, opCoinbase, constGas(gasBase), 0, 1)
	reg(TIMESTAMP, opTimestamp, constGas(gasBase), 0, 1)
	reg(NUMBER, opNumber, constGas(gasBase), 0, 1)
	reg(DIFFICULTY, opZeroWord, constGas(gasBase), 0, 1) // TRON: always 0
	reg(GASLIMIT, opZeroWord, constGas(gasBase), 0, 1)   // TRON: always 0
	reg(CHAINID, opChainid, constGas(gasBase), 0, 1)

	// 0x50 — stack, memory, storage, flow.
	reg(POP, opPop, constGas(gasBase), 1, 0)
	reg(MLOAD, opMload, gasMload, 1, 1)
	reg(MSTORE, opMstore, gasMstore, 2, 0)
	reg(MSTORE8, opMstore8, gasMstore8, 2, 0)
	reg(SLOAD, opSload, constGas(gasSLOAD), 1, 1)
	reg(SSTORE, opSstore, gasSstore, 2, 0)
	t[JUMP] = &operation{exec: opJump, gas: constGas(gasMid), pop: 1, jumps: true}
	t[JUMPI] = &operation{exec: opJumpi, gas: constGas(gasHigh), pop: 2, jumps: true}
	reg(PC, opPc, constGas(gasBase), 0, 1)
	reg(MSIZE, opMsize, constGas(gasBase), 0, 1)
	reg(GAS, opGas, constGas(gasBase), 0, 1)
	reg(JUMPDEST, opJumpdest, constGas(gasSpecial), 0, 0)

	// 0x60–0x7f — PUSH1..PUSH32.
	for n := 0; n <= 31; n++ {
		op := OpCode(int(PUSH1) + n)
		size := n + 1
		t[op] = &operation{exec: makePush(size), gas: constGas(gasVeryLow), pop: 0, push: 1}
	}
	// 0x80–0x8f — DUP1..DUP16.
	for n := 1; n <= 16; n++ {
		op := OpCode(int(DUP1) + n - 1)
		t[op] = &operation{exec: makeDup(n), gas: constGas(gasVeryLow), pop: n, push: n + 1}
	}
	// 0x90–0x9f — SWAP1..SWAP16.
	for n := 1; n <= 16; n++ {
		op := OpCode(int(SWAP1) + n - 1)
		t[op] = &operation{exec: makeSwap(n), gas: constGas(gasVeryLow), pop: n + 1, push: n + 1}
	}

	// 0xd0 — TRON token (TRC10) ops (M3.1: read-side plumbing; CALLTOKEN deferred to M3.3).
	reg(TOKENBALANCE, opTokenbalance, constGas(20), 2, 1)
	reg(CALLTOKENVALUE, opCalltokenvalue, constGas(gasBase), 0, 1)
	reg(CALLTOKENID, opCalltokenid, constGas(gasBase), 0, 1)
	reg(ISCONTRACT, opIscontract, constGas(gasBase), 1, 1)

	// 0xf0 — call frames, create, halts.
	t[CREATE] = &operation{exec: opCreate, gas: gasCreate, pop: 3, push: 1}
	t[CREATE2] = &operation{exec: opCreate2, gas: gasCreate2, pop: 4, push: 1}
	t[CALL] = &operation{exec: opCall, gas: gasCall, pop: 7, push: 1}
	t[CALLCODE] = &operation{exec: opCallcode, gas: gasCallcode, pop: 7, push: 1}
	t[DELEGATECALL] = &operation{exec: opDelegatecall, gas: gasCallNoValue, pop: 6, push: 1}
	t[STATICCALL] = &operation{exec: opStaticcall, gas: gasCallNoValue, pop: 6, push: 1}
	t[RETURN] = &operation{exec: opReturn, gas: gasReturn, pop: 2, halts: true}
	t[REVERT] = &operation{exec: opRevert, gas: gasReturn, pop: 2, halts: true}
	t[INVALID] = &operation{exec: opInvalid, gas: constGas(gasZero)}

	builtTable = t
}

const gasSLOAD = 50

// ---- arithmetic ----

func opStop(in *interpreter, sc *scope) error { sc.stop = true; return nil }

func opAdd(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Add(&x, y)
	return nil
}

func opMul(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Mul(&x, y)
	return nil
}

func opSub(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Sub(&x, y)
	return nil
}

func opDiv(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Div(&x, y)
	return nil
}

func opSdiv(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.SDiv(&x, y)
	return nil
}

func opMod(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Mod(&x, y)
	return nil
}

func opSmod(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.SMod(&x, y)
	return nil
}

func opAddmod(in *interpreter, sc *scope) error {
	a, _ := sc.stack.pop()
	b, _ := sc.stack.pop()
	m := sc.stack.peek(0)
	m.AddMod(&a, &b, m)
	return nil
}

func opMulmod(in *interpreter, sc *scope) error {
	a, _ := sc.stack.pop()
	b, _ := sc.stack.pop()
	m := sc.stack.peek(0)
	m.MulMod(&a, &b, m)
	return nil
}

func opExp(in *interpreter, sc *scope) error {
	base, _ := sc.stack.pop()
	exp := sc.stack.peek(0)
	exp.Exp(&base, exp)
	return nil
}

func opSignextend(in *interpreter, sc *scope) error {
	back, _ := sc.stack.pop()
	num := sc.stack.peek(0)
	num.ExtendSign(num, &back)
	return nil
}

// ---- comparison & bitwise ----

func setBool(z *uint256.Int, b bool) {
	if b {
		z.SetUint64(1)
	} else {
		z.Clear()
	}
}

func opLt(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	setBool(y, x.Lt(y))
	return nil
}

func opGt(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	setBool(y, x.Gt(y))
	return nil
}

func opSlt(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	setBool(y, x.Slt(y))
	return nil
}

func opSgt(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	setBool(y, x.Sgt(y))
	return nil
}

func opEq(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	setBool(y, x.Eq(y))
	return nil
}

func opIszero(in *interpreter, sc *scope) error {
	x := sc.stack.peek(0)
	setBool(x, x.IsZero())
	return nil
}

func opAnd(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.And(&x, y)
	return nil
}

func opOr(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Or(&x, y)
	return nil
}

func opXor(in *interpreter, sc *scope) error {
	x, _ := sc.stack.pop()
	y := sc.stack.peek(0)
	y.Xor(&x, y)
	return nil
}

func opNot(in *interpreter, sc *scope) error {
	x := sc.stack.peek(0)
	x.Not(x)
	return nil
}

func opByte(in *interpreter, sc *scope) error {
	idx, _ := sc.stack.pop()
	val := sc.stack.peek(0)
	val.Byte(&idx)
	return nil
}

// shiftAmount returns min(shift, 256) as uint, so shifts >= 256 collapse correctly.
func shiftAmount(shift *uint256.Int) uint {
	if shift.LtUint64(256) {
		return uint(shift.Uint64())
	}
	return 256
}

func opShl(in *interpreter, sc *scope) error {
	shift, _ := sc.stack.pop()
	value := sc.stack.peek(0)
	value.Lsh(value, shiftAmount(&shift))
	return nil
}

func opShr(in *interpreter, sc *scope) error {
	shift, _ := sc.stack.pop()
	value := sc.stack.peek(0)
	value.Rsh(value, shiftAmount(&shift))
	return nil
}

func opSar(in *interpreter, sc *scope) error {
	shift, _ := sc.stack.pop()
	value := sc.stack.peek(0)
	value.SRsh(value, shiftAmount(&shift))
	return nil
}

// ---- KECCAK256 ----

func opKeccak256(in *interpreter, sc *scope) error {
	offset, _ := sc.stack.pop()
	size := sc.stack.peek(0)
	data := sc.mem.get(offset.Uint64(), size.Uint64())
	size.SetBytes(crypto.Keccak256(data))
	return nil
}

// ---- environment ----

func opAddress(in *interpreter, sc *scope) error {
	w := addrWord(sc.contract.Self)
	return sc.stack.push(w)
}

func opOrigin(in *interpreter, sc *scope) error {
	w := addrWord(sc.contract.Origin)
	return sc.stack.push(w)
}

func opCaller(in *interpreter, sc *scope) error {
	w := addrWord(sc.contract.Caller)
	return sc.stack.push(w)
}

func opCallvalue(in *interpreter, sc *scope) error {
	v := new(uint256.Int)
	if sc.contract.Value != nil {
		v.Set(sc.contract.Value)
	}
	return sc.stack.push(*v)
}

func opCalldataload(in *interpreter, sc *scope) error {
	off := sc.stack.peek(0)
	var buf [32]byte
	if off.IsUint64() {
		copyDataAt(buf[:], sc.contract.Input, off.Uint64(), 32)
	}
	off.SetBytes(buf[:])
	return nil
}

func opCalldatasize(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(len(sc.contract.Input)))
	return sc.stack.push(w)
}

func opCodesize(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(len(sc.contract.Code)))
	return sc.stack.push(w)
}

func opCalldatacopy(in *interpreter, sc *scope) error {
	memOff, _ := sc.stack.pop()
	dataOff, _ := sc.stack.pop()
	length, _ := sc.stack.pop()
	out := make([]byte, length.Uint64())
	copyDataAt(out, sc.contract.Input, clampU64(dataOff), length.Uint64())
	sc.mem.set(memOff.Uint64(), out)
	return nil
}

func opCodecopy(in *interpreter, sc *scope) error {
	memOff, _ := sc.stack.pop()
	codeOff, _ := sc.stack.pop()
	length, _ := sc.stack.pop()
	out := make([]byte, length.Uint64())
	copyDataAt(out, sc.contract.Code, clampU64(codeOff), length.Uint64())
	sc.mem.set(memOff.Uint64(), out)
	return nil
}

func opGasprice(in *interpreter, sc *scope) error {
	// M3.0: energy price modeling is deferred (M3.3); GASPRICE yields 0.
	return sc.stack.push(uint256.Int{})
}

func opReturndatasize(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(len(sc.returnData)))
	return sc.stack.push(w)
}

func opCoinbase(in *interpreter, sc *scope) error {
	return sc.stack.push(addrWord(in.block.Coinbase))
}

func opTimestamp(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(in.block.Timestamp))
	return sc.stack.push(w)
}

func opNumber(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(in.block.Number))
	return sc.stack.push(w)
}

func opZeroWord(in *interpreter, sc *scope) error {
	return sc.stack.push(uint256.Int{})
}

func opChainid(in *interpreter, sc *scope) error {
	var w uint256.Int
	if in.block.ChainID != nil {
		w.Set(in.block.ChainID)
	}
	return sc.stack.push(w)
}

// ---- stack / memory / storage / flow ----

func opPop(in *interpreter, sc *scope) error {
	_, err := sc.stack.pop()
	return err
}

func opMload(in *interpreter, sc *scope) error {
	off := sc.stack.peek(0)
	b := sc.mem.get(off.Uint64(), 32)
	off.SetBytes(b)
	return nil
}

func opMstore(in *interpreter, sc *scope) error {
	off, _ := sc.stack.pop()
	val, _ := sc.stack.pop()
	sc.mem.set32(off.Uint64(), val.Bytes32())
	return nil
}

func opMstore8(in *interpreter, sc *scope) error {
	off, _ := sc.stack.pop()
	val, _ := sc.stack.pop()
	sc.mem.set(off.Uint64(), []byte{byte(val.Uint64())})
	return nil
}

func opSload(in *interpreter, sc *scope) error {
	slot := sc.stack.peek(0)
	v, _ := in.evm.state.GetStorage(sc.contract.Self, slot.Bytes32())
	slot.SetBytes(v[:])
	return nil
}

func opSstore(in *interpreter, sc *scope) error {
	if in.readOnly {
		return ErrStaticStateChange
	}
	slot, _ := sc.stack.pop()
	val, _ := sc.stack.pop()
	in.evm.state.SetStorage(sc.contract.Self, slot.Bytes32(), val.Bytes32())
	return nil
}

func opJump(in *interpreter, sc *scope) error {
	dest, _ := sc.stack.pop()
	return jumpTo(in, sc, &dest)
}

func opJumpi(in *interpreter, sc *scope) error {
	dest, _ := sc.stack.pop()
	cond, _ := sc.stack.pop()
	if cond.IsZero() {
		sc.pc++ // fall through
		return nil
	}
	return jumpTo(in, sc, &dest)
}

func jumpTo(in *interpreter, sc *scope, dest *uint256.Int) error {
	if !dest.IsUint64() {
		return ErrBadJumpDest
	}
	d := dest.Uint64()
	if d >= uint64(len(sc.contract.Code)) || !in.dests[d] {
		return ErrBadJumpDest
	}
	sc.pc = int(d)
	return nil
}

func opPc(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(sc.pc))
	return sc.stack.push(w)
}

func opMsize(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(uint64(sc.mem.Len()))
	return sc.stack.push(w)
}

func opGas(in *interpreter, sc *scope) error {
	var w uint256.Int
	w.SetUint64(in.meter.remaining())
	return sc.stack.push(w)
}

func opJumpdest(in *interpreter, sc *scope) error { return nil }

func makePush(size int) func(*interpreter, *scope) error {
	return func(in *interpreter, sc *scope) error {
		code := sc.contract.Code
		start := sc.pc + 1
		var buf [32]byte
		for i := 0; i < size; i++ {
			if start+i < len(code) {
				buf[32-size+i] = code[start+i]
			}
		}
		var w uint256.Int
		w.SetBytes(buf[:])
		sc.pc += size // immediate bytes; the loop adds the final +1
		return sc.stack.push(w)
	}
}

func makeDup(n int) func(*interpreter, *scope) error {
	return func(in *interpreter, sc *scope) error { return sc.stack.dup(n) }
}

func makeSwap(n int) func(*interpreter, *scope) error {
	return func(in *interpreter, sc *scope) error { return sc.stack.swap(n) }
}

func opReturn(in *interpreter, sc *scope) error {
	off, _ := sc.stack.pop()
	size, _ := sc.stack.pop()
	sc.ret = sc.mem.get(off.Uint64(), size.Uint64())
	sc.stop = true
	return nil
}

func opRevert(in *interpreter, sc *scope) error {
	off, _ := sc.stack.pop()
	size, _ := sc.stack.pop()
	sc.ret = sc.mem.get(off.Uint64(), size.Uint64())
	sc.reverted = true
	sc.stop = true
	return nil
}

func opInvalid(in *interpreter, sc *scope) error { return ErrInvalidOpcode }

// ---- account access (M3.1: cross-contract state) ----

func opBalance(in *interpreter, sc *scope) error {
	a := sc.stack.peek(0)
	a.Set(in.evm.state.GetBalance(wordToAddr(a)))
	return nil
}

func opSelfbalance(in *interpreter, sc *scope) error {
	return sc.stack.push(*in.evm.state.GetBalance(sc.contract.Self))
}

func opExtcodesize(in *interpreter, sc *scope) error {
	a := sc.stack.peek(0)
	a.SetUint64(uint64(in.evm.state.GetCodeSize(wordToAddr(a))))
	return nil
}

func opExtcodehash(in *interpreter, sc *scope) error {
	a := sc.stack.peek(0)
	if !in.evm.state.Exist(wordToAddr(a)) {
		a.Clear()
		return nil
	}
	h := in.evm.state.GetCodeHash(wordToAddr(a))
	a.SetBytes(h[:])
	return nil
}

func opExtcodecopy(in *interpreter, sc *scope) error {
	addr, _ := sc.stack.pop()
	memOff, _ := sc.stack.pop()
	codeOff, _ := sc.stack.pop()
	length, _ := sc.stack.pop()
	code := in.evm.state.GetCode(wordToAddr(&addr))
	out := make([]byte, length.Uint64())
	copyDataAt(out, code, clampU64(codeOff), length.Uint64())
	sc.mem.set(memOff.Uint64(), out)
	return nil
}

func opIscontract(in *interpreter, sc *scope) error {
	a := sc.stack.peek(0)
	setBool(a, in.evm.state.GetCodeSize(wordToAddr(a)) > 0)
	return nil
}

// ---- return data ----

func opReturndatacopy(in *interpreter, sc *scope) error {
	memOff, _ := sc.stack.pop()
	dataOff, _ := sc.stack.pop()
	length, _ := sc.stack.pop()
	end, err := memAccessSize(&dataOff, &length)
	if err != nil {
		return err
	}
	if end > uint64(len(sc.returnData)) {
		return ErrReturnDataOutOfBounds
	}
	out := make([]byte, length.Uint64())
	copyDataAt(out, sc.returnData, dataOff.Uint64(), length.Uint64())
	sc.mem.set(memOff.Uint64(), out)
	return nil
}

// ---- TRC10 token plumbing (M3.1: structure only; token transfer is M3.3) ----

func opCalltokenvalue(in *interpreter, sc *scope) error { return sc.stack.push(uint256.Int{}) }
func opCalltokenid(in *interpreter, sc *scope) error    { return sc.stack.push(uint256.Int{}) }

func opTokenbalance(in *interpreter, sc *scope) error {
	// pops address, tokenId; TRC10 asset balances are not modeled until M3.3 -> 0.
	sc.stack.pop()
	id := sc.stack.peek(0)
	id.Clear()
	return nil
}

// ---- CALL family ----

func opCall(in *interpreter, sc *scope) error {
	gasW, _ := sc.stack.pop()
	addrW, _ := sc.stack.pop()
	value, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	outOff, _ := sc.stack.pop()
	outSize, _ := sc.stack.pop()
	args := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	return in.evm.doCall(in, sc, kindCall, wordToAddr(&addrW), &value, clampU64(gasW), args, outOff.Uint64(), outSize.Uint64())
}

func opCallcode(in *interpreter, sc *scope) error {
	gasW, _ := sc.stack.pop()
	addrW, _ := sc.stack.pop()
	value, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	outOff, _ := sc.stack.pop()
	outSize, _ := sc.stack.pop()
	args := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	return in.evm.doCall(in, sc, kindCallCode, wordToAddr(&addrW), &value, clampU64(gasW), args, outOff.Uint64(), outSize.Uint64())
}

func opDelegatecall(in *interpreter, sc *scope) error {
	gasW, _ := sc.stack.pop()
	addrW, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	outOff, _ := sc.stack.pop()
	outSize, _ := sc.stack.pop()
	args := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	return in.evm.doCall(in, sc, kindDelegate, wordToAddr(&addrW), nil, clampU64(gasW), args, outOff.Uint64(), outSize.Uint64())
}

func opStaticcall(in *interpreter, sc *scope) error {
	gasW, _ := sc.stack.pop()
	addrW, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	outOff, _ := sc.stack.pop()
	outSize, _ := sc.stack.pop()
	args := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	return in.evm.doCall(in, sc, kindStatic, wordToAddr(&addrW), nil, clampU64(gasW), args, outOff.Uint64(), outSize.Uint64())
}

// ---- CREATE ----

func opCreate(in *interpreter, sc *scope) error {
	value, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	initCode := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	return in.evm.doCreate(in, sc, &value, initCode, nil, false)
}

func opCreate2(in *interpreter, sc *scope) error {
	value, _ := sc.stack.pop()
	inOff, _ := sc.stack.pop()
	inSize, _ := sc.stack.pop()
	salt, _ := sc.stack.pop()
	initCode := sc.mem.get(inOff.Uint64(), inSize.Uint64())
	saltB := salt.Bytes32()
	return in.evm.doCreate(in, sc, &value, initCode, saltB[:], true)
}

// copyDataAt copies into dst (already sized) `length` bytes of src starting at srcOff,
// zero-filling past the end of src — the EVM CALLDATACOPY/CODECOPY/CALLDATALOAD rule.
func copyDataAt(dst, src []byte, srcOff, length uint64) {
	for i := uint64(0); i < length && i < uint64(len(dst)); i++ {
		j := srcOff + i
		if j >= srcOff && j < uint64(len(src)) { // j>=srcOff guards uint64 wrap
			dst[i] = src[j]
		}
	}
}
