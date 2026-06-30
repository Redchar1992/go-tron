package tvm

import "github.com/holiman/uint256"

// Dynamic energy-cost functions. Each returns the FULL cost of its op (mirroring
// java-tron, where one cost function = the op's whole energy), reading arguments from the
// stack by peek (they are not yet popped — energy is charged before execution).

// memSpecialTier is the SPECIAL_TIER (1) surcharge current mainnet adds to MLOAD/MSTORE/
// MSTORE8 via getMloadCost2; the legacy schedule omits it.
func (in *interpreter) memSpecialTier() uint64 {
	if in.cfg.LegacyMemCost {
		return 0
	}
	return gasSpecial
}

// gasMload: SPECIAL_TIER + memory expansion to cover [offset, offset+32).
func gasMload(in *interpreter, sc *scope) (uint64, error) {
	end, err := memAccessSize(sc.stack.peek(0), uint256From(32))
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	return in.memSpecialTier() + exp, nil
}

// gasMstore: identical access shape to MLOAD (32-byte word at offset).
func gasMstore(in *interpreter, sc *scope) (uint64, error) {
	return gasMload(in, sc)
}

// gasMstore8: SPECIAL_TIER + expansion to cover [offset, offset+1).
func gasMstore8(in *interpreter, sc *scope) (uint64, error) {
	end, err := memAccessSize(sc.stack.peek(0), uint256From(1))
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	return in.memSpecialTier() + exp, nil
}

// gasKeccak256: 30 + 6*words(size) + memory expansion over [offset, offset+size).
func gasKeccak256(in *interpreter, sc *scope) (uint64, error) {
	offset := sc.stack.peek(0)
	size := sc.stack.peek(1)
	end, err := memAccessSize(offset, size)
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	szw, err := toUint64(size)
	if err != nil {
		return 0, err
	}
	return 30 + 6*wordsForBytes(szw) + exp, nil
}

// gasCopy: CALLDATACOPY/CODECOPY/RETURNDATACOPY — memory expansion over the destination
// plus 3 energy per 32-byte word copied. Stack: memOffset, srcOffset, length.
func gasCopy(in *interpreter, sc *scope) (uint64, error) {
	memOff := sc.stack.peek(0)
	length := sc.stack.peek(2)
	end, err := memAccessSize(memOff, length)
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	l, err := toUint64(length)
	if err != nil {
		return 0, err
	}
	return exp + 3*wordsForBytes(l), nil
}

// gasReturn: RETURN/REVERT — memory expansion over [offset, offset+size) (base STOP = 0).
func gasReturn(in *interpreter, sc *scope) (uint64, error) {
	end, err := memAccessSize(sc.stack.peek(0), sc.stack.peek(1))
	if err != nil {
		return 0, err
	}
	return memExpandCost(sc.mem, end)
}

// gasExp: 10 + 10 * (number of non-zero-leading bytes in the exponent).
func gasExp(in *interpreter, sc *scope) (uint64, error) {
	exp := sc.stack.peek(1) // base is top (peek 0), exponent is next
	return 10 + 10*uint64(exp.ByteLen()), nil
}

// gasExtcodecopy: 20 (EXT_CODE_COPY) + memory expansion + 3/word copied.
// Stack: address, memOffset, codeOffset, length.
func gasExtcodecopy(in *interpreter, sc *scope) (uint64, error) {
	memOff := sc.stack.peek(1)
	length := sc.stack.peek(3)
	end, err := memAccessSize(memOff, length)
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	l, err := toUint64(length)
	if err != nil {
		return 0, err
	}
	return 20 + exp + 3*wordsForBytes(l), nil
}

// gasReturndatacopy: memory expansion over [destOffset, destOffset+size) + 3/word.
// Stack: destOffset, dataOffset, length.
func gasReturndatacopy(in *interpreter, sc *scope) (uint64, error) {
	return gasCopy(in, sc)
}

// callArgsMem computes the memory-expansion energy covering both the input args and the
// output buffer of a CALL-family op.
func callArgsMem(sc *scope, inOff, inSize, outOff, outSize *uint256.Int) (uint64, error) {
	inEnd, err := memAccessSize(inOff, inSize)
	if err != nil {
		return 0, err
	}
	outEnd, err := memAccessSize(outOff, outSize)
	if err != nil {
		return 0, err
	}
	end := inEnd
	if outEnd > end {
		end = outEnd
	}
	return memExpandCost(sc.mem, end)
}

// gasCall: 40 + (value? 9000 + (new callee? 25000)) + memory expansion (in/out).
// Stack: gas, address, value, inOff, inSize, outOff, outSize.
func gasCall(in *interpreter, sc *scope) (uint64, error) {
	value := sc.stack.peek(2)
	mem, err := callArgsMem(sc, sc.stack.peek(3), sc.stack.peek(4), sc.stack.peek(5), sc.stack.peek(6))
	if err != nil {
		return 0, err
	}
	cost := uint64(40) + mem
	if !value.IsZero() {
		cost += vtCall
		if !in.evm.state.Exist(wordToAddr(sc.stack.peek(1))) {
			cost += newAcctCall
		}
	}
	return cost, nil
}

// gasCallCode: 40 + (value? 9000) + memory expansion. No new-account surcharge (CALLCODE
// keeps the caller's context). Stack as CALL.
func gasCallcode(in *interpreter, sc *scope) (uint64, error) {
	value := sc.stack.peek(2)
	mem, err := callArgsMem(sc, sc.stack.peek(3), sc.stack.peek(4), sc.stack.peek(5), sc.stack.peek(6))
	if err != nil {
		return 0, err
	}
	cost := uint64(40) + mem
	if !value.IsZero() {
		cost += vtCall
	}
	return cost, nil
}

// gasCallNoValue: DELEGATECALL/STATICCALL — 40 + memory expansion (no value operand).
// Stack: gas, address, inOff, inSize, outOff, outSize.
func gasCallNoValue(in *interpreter, sc *scope) (uint64, error) {
	mem, err := callArgsMem(sc, sc.stack.peek(2), sc.stack.peek(3), sc.stack.peek(4), sc.stack.peek(5))
	if err != nil {
		return 0, err
	}
	return 40 + mem, nil
}

// gasCreate: 32000 + memory expansion over the init code. Stack: value, inOff, inSize.
func gasCreate(in *interpreter, sc *scope) (uint64, error) {
	end, err := memAccessSize(sc.stack.peek(1), sc.stack.peek(2))
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	return createGas + exp, nil
}

// gasCreate2: 32000 + memory expansion + 6/word of init code (the extra hashing).
// Stack: value, inOff, inSize, salt.
func gasCreate2(in *interpreter, sc *scope) (uint64, error) {
	size := sc.stack.peek(2)
	end, err := memAccessSize(sc.stack.peek(1), size)
	if err != nil {
		return 0, err
	}
	exp, err := memExpandCost(sc.mem, end)
	if err != nil {
		return 0, err
	}
	sz, err := toUint64(size)
	if err != nil {
		return 0, err
	}
	return createGas + exp + 6*wordsForBytes(sz), nil
}

// gasSstore: SET (absent slot -> non-zero) = 20000; CLEAR (present -> zero) = 5000;
// otherwise RESET = 5000. Matches java-tron getSstoreCost. Stack: key, value.
func gasSstore(in *interpreter, sc *scope) (uint64, error) {
	slot := sc.stack.peek(0)
	val := sc.stack.peek(1)
	_, present := in.evm.state.GetStorage(sc.contract.Self, slot.Bytes32())
	switch {
	case !present && !val.IsZero():
		return 20000, nil
	case present && val.IsZero():
		return 5000, nil
	default:
		return 5000, nil
	}
}
