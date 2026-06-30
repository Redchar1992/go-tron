package tvm

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

// gasSstore: SET (absent slot -> non-zero) = 20000; CLEAR (present -> zero) = 5000;
// otherwise RESET = 5000. Matches java-tron getSstoreCost. Stack: key, value.
func gasSstore(in *interpreter, sc *scope) (uint64, error) {
	key := sc.stack.peek(0)
	val := sc.stack.peek(1)
	_, present := sc.contract.Storage.Load(key.Bytes32())
	switch {
	case !present && !val.IsZero():
		return 20000, nil
	case present && val.IsZero():
		return 5000, nil
	default:
		return 5000, nil
	}
}
