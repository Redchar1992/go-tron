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
	// ErrStaticStateChange is returned when a state-mutating op runs in a STATICCALL.
	ErrStaticStateChange = errors.New("tvm: state change in static context")
	// ErrReturnDataOutOfBounds is returned when RETURNDATACOPY reads past the buffer.
	ErrReturnDataOutOfBounds = errors.New("tvm: return data out of bounds")
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
	// Forward6364, when true, caps the energy passed to a child CALL/CREATE at 63/64 of
	// the available energy (java-tron's allowTvmCompatibleEvm && contractVersion==1 path).
	// Off = original TRON behavior: the child may receive all available energy.
	Forward6364 bool

	// Hardfork/TIP gates (java-tron VMConfig.allowTvm*). An opcode introduced by a fork
	// faults as an invalid opcode until its gate is enabled. Activation on a real network
	// is committee-proposal driven; the node maps proposal state -> these flags.
	AllowTransferTrc10  bool // CALLTOKEN, TOKENBALANCE, CALLTOKENVALUE, CALLTOKENID
	AllowConstantinople bool // SHL, SHR, SAR, CREATE2, EXTCODEHASH
	AllowSolidity059    bool // ISCONTRACT
	AllowIstanbul       bool // CHAINID, SELFBALANCE
	AllowLondon         bool // BASEFEE (not yet implemented)

	// AllowShieldedTRC20Transaction gates the shielded-TRC-20 precompiles (0x1000001..4).
	// It is proposal #39, DISABLED by default and off on mainnet & a from-genesis chain, so
	// those addresses are ordinary account calls (not precompiles) — see shielded.go. Not
	// version-derivable, so VMConfigForVersion leaves it false.
	AllowShieldedTRC20Transaction bool
}

// interpreter executes a single contract frame within an EVM.
type interpreter struct {
	evm      *EVM
	cfg      VMConfig
	block    BlockContext
	meter    *energyMeter
	table    *[256]*operation
	dests    []bool // valid JUMPDEST bitmap for the current code
	readOnly bool   // STATICCALL context: state-mutating ops fault
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
	returnData []byte // last nested call's RETURN/REVERT payload (RETURNDATASIZE/COPY)
}

// runFrame executes a single frame to completion and returns its result. REVERT is
// reported via Result.Reverted (not Err); a VM exception sets Err and consumes all
// energy (java-tron semantics), except REVERT which refunds the remainder.
func (in *interpreter) runFrame(sc *scope) *Result {
	in.dests = analyzeJumpDests(sc.contract.Code)

	for !sc.stop && sc.pc < len(sc.contract.Code) {
		op := OpCode(sc.contract.Code[sc.pc])
		o := in.table[op]
		if o == nil || (o.enabled != nil && !o.enabled(in.cfg)) {
			// Unknown opcode, or one whose introducing hardfork is not active yet.
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
