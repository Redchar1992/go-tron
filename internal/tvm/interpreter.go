package tvm

import (
	"errors"

	"github.com/holiman/uint256"
)

var (
	// ErrInvalidOpcode is returned when the interpreter meets a byte that is not a
	// defined (M3.0-supported) operation.
	ErrInvalidOpcode = errors.New("tvm: invalid opcode")
	// ErrBadJumpDest is returned when JUMP/JUMPI targets a non-JUMPDEST position.
	ErrBadJumpDest = errors.New("tvm: bad jump destination")
	// ErrMemoryOverflow is returned when an access would exceed the 3 MB memory limit.
	ErrMemoryOverflow = errors.New("tvm: memory overflow")
	// ErrGasUintOverflow is returned when a memory offset/size does not fit in uint64.
	ErrGasUintOverflow = errors.New("tvm: gas uint64 overflow")
)

// memLimit is java-tron's 3 MB cap on a single execution's memory (EnergyCost MEM_LIMIT).
const memLimit = 3 * 1024 * 1024

// VMConfig holds the hardfork/feature toggles. M3.0 only needs the memory-load/store
// SPECIAL_TIER surcharge, which current mainnet applies (getMloadCost2 path); later
// sub-milestones (M3.4) wire the full gate set. Default zero value = current mainnet.
type VMConfig struct {
	// LegacyMemCost, when true, uses the original MLOAD/MSTORE cost (no SPECIAL_TIER
	// surcharge) — the pre-allowHigherLimitForMaxCpuTimeOfOneTx behavior.
	LegacyMemCost bool
}

// interpreter executes a single contract frame.
type interpreter struct {
	cfg   VMConfig
	block BlockContext
	meter *energyMeter
	table *[256]*operation
	dests []bool // valid JUMPDEST bitmap for the current code
}

// scope is the mutable per-frame execution state.
type scope struct {
	contract   *Contract
	stack      *Stack
	mem        *Memory
	pc         int
	stop       bool
	ret        []byte
	reverted   bool
	returnData []byte // RETURNDATA buffer; empty in M3.0 (no nested calls)
}

// Run executes contract under energyLimit with the given block context and returns the
// result. REVERT is reported via Result.Reverted (not Err); a VM exception sets Err and
// consumes all energy (java-tron semantics), except REVERT which refunds the remainder.
func Run(contract *Contract, input []byte, energyLimit uint64, block BlockContext, cfg VMConfig) *Result {
	contract.Input = input
	in := &interpreter{
		cfg:   cfg,
		block: block,
		meter: newEnergyMeter(energyLimit),
		table: opTable(),
	}
	sc := &scope{contract: contract, stack: newStack(), mem: newMemory()}
	in.dests = analyzeJumpDests(contract.Code)

	for !sc.stop && sc.pc < len(contract.Code) {
		op := OpCode(contract.Code[sc.pc])
		o := in.table[op]
		if o == nil {
			return in.fail(ErrInvalidOpcode)
		}
		if sc.stack.Len() < o.pop {
			return in.fail(ErrStackUnderflow)
		}
		if sc.stack.Len()-o.pop+o.push > MaxStack {
			return in.fail(ErrStackOverflow)
		}
		cost, err := o.gas(in, sc)
		if err != nil {
			return in.fail(err)
		}
		if err := in.meter.spend(cost); err != nil {
			return in.fail(err)
		}
		if err := o.exec(in, sc); err != nil {
			return in.fail(err)
		}
		if !o.jumps && !o.halts {
			sc.pc++
		}
	}
	// Normal end (STOP / RETURN / REVERT / running off the code end). REVERT does not
	// burn the remaining energy; it is reported via Reverted.
	return &Result{Return: sc.ret, Reverted: sc.reverted, EnergyUsed: in.meter.used}
}

// fail builds a Result for a VM exception: all energy is consumed.
func (in *interpreter) fail(err error) *Result {
	return &Result{EnergyUsed: in.meter.limit, Err: err}
}

// ---- energy / memory cost helpers ----

// memCostWords is java-tron's f(w) = MEMORY*w + w^2/512 (integer division).
func memCostWords(words uint64) uint64 { return 3*words + words*words/512 }

// memExpandCost returns the energy to grow memory so it covers newByteSize, charging the
// f(new)-f(old) delta. Returns ErrMemoryOverflow past the 3 MB limit.
func memExpandCost(mem *Memory, newByteSize uint64) (uint64, error) {
	if newByteSize == 0 {
		return 0, nil
	}
	if newByteSize > memLimit {
		return 0, ErrMemoryOverflow
	}
	newSize := wordsForBytes(newByteSize) * 32
	old := uint64(mem.Len())
	if newSize <= old {
		return 0, nil
	}
	return memCostWords(newSize/32) - memCostWords(old/32), nil
}

// toUint64 converts a 256-bit word to uint64, erroring if it does not fit (used for
// memory offsets/sizes, where an out-of-range value is a hard fault).
func toUint64(v *uint256.Int) (uint64, error) {
	if !v.IsUint64() {
		return 0, ErrGasUintOverflow
	}
	return v.Uint64(), nil
}

// uint256From boxes a constant as a 256-bit word (for memAccessSize calls).
func uint256From(n uint64) *uint256.Int { return new(uint256.Int).SetUint64(n) }

// clampU64 returns v as uint64, or MaxUint64 if it does not fit — used for data-copy
// source offsets, where an out-of-range offset simply means "read past the end" (the
// copy zero-fills), not a fault.
func clampU64(v uint256.Int) uint64 {
	if v.IsUint64() {
		return v.Uint64()
	}
	return ^uint64(0)
}

// memAccessSize returns offset+size as uint64, erroring on overflow. A zero size yields 0
// (no expansion), matching java-tron memNeeded.
func memAccessSize(offset, size *uint256.Int) (uint64, error) {
	if size.IsZero() {
		return 0, nil
	}
	off, err := toUint64(offset)
	if err != nil {
		return 0, err
	}
	sz, err := toUint64(size)
	if err != nil {
		return 0, err
	}
	end := off + sz
	if end < off { // uint64 wrap
		return 0, ErrGasUintOverflow
	}
	return end, nil
}

// analyzeJumpDests returns a bitmap of valid JUMPDEST positions: bytes equal to JUMPDEST
// (0x5b) that are not inside PUSH immediate data.
func analyzeJumpDests(code []byte) []bool {
	dests := make([]bool, len(code))
	for pc := 0; pc < len(code); pc++ {
		op := OpCode(code[pc])
		if n, ok := op.pushBytes(); ok {
			pc += n // skip immediate bytes
			continue
		}
		if op == JUMPDEST {
			dests[pc] = true
		}
	}
	return dests
}
