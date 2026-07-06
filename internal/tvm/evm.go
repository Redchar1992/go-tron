package tvm

import "github.com/holiman/uint256"

// EVM is the cross-frame execution context: the account state, the block context, the
// config, and the current call depth. It runs the top-level message frame and the nested
// CALL/CREATE frames, sharing one journaled StateDB across them so a failed child frame
// rolls back exactly what it touched.
//
// This is the go-tron analog of java-tron's VM + Program wiring. M3.1 adds the call/create
// frames over the M3.0 single-frame interpreter.
type EVM struct {
	state    StateDB
	block    BlockContext
	cfg      VMConfig
	depth    int
	rootTxID []byte                  // 32-byte root transaction id, used in CREATE address derivation
	perm     AccountPermissionReader // account-permission source for validatemultisign (0x0a); may be nil
	logs     []*Log                  // LOG0..LOG4 events, journaled to snapshots (reverted frames discard theirs)
}

// maxCallDepth is the TVM call-stack limit. TRON's MAX_DEPTH is 64 (NOT Ethereum's 1024);
// a frame at depth 64 cannot call deeper — the call fails and pushes 0.
const maxCallDepth = 64

// NewEVM builds an EVM over the given state and block context.
func NewEVM(state StateDB, block BlockContext, cfg VMConfig) *EVM {
	return &EVM{state: state, block: block, cfg: cfg}
}

// State returns the underlying account state.
func (evm *EVM) State() StateDB { return evm.state }

// SetRootTxID sets the 32-byte root transaction id used in CREATE address derivation.
func (evm *EVM) SetRootTxID(id []byte) { evm.rootTxID = id }

// SetPermissionReader wires the account-permission source the validatemultisign precompile
// (0x0a) reads. Left unset, 0x0a returns false.
func (evm *EVM) SetPermissionReader(p AccountPermissionReader) { evm.perm = p }

// call/create energy constants (java-tron EnergyCost).
const (
	stipendCall = 2300  // free energy granted to a value-bearing child
	createData  = 200   // per-byte cost of deployed code
	vtCall      = 9000  // value-transfer surcharge
	newAcctCall = 25000 // surcharge to create a new callee account
	createGas   = 32000 // CREATE base
)

// callEnergy returns the energy to hand a child frame: the 63/64-capped available energy
// (when Forward6364 is set), bounded by the requested amount.
func (evm *EVM) callEnergy(requested, available uint64) uint64 {
	if evm.cfg.Forward6364 {
		available -= available / 64
	}
	if requested < available {
		return requested
	}
	return available
}

// callKind distinguishes the CALL-family variants.
type callKind int

const (
	kindCall callKind = iota
	kindCallCode
	kindDelegate
	kindStatic
)

// doCall runs one CALL-family op. The op's base cost was charged by its gas function
// before exec; here we reserve the child budget from the meter, run the child frame
// against a state snapshot, refund unspent energy, and push success (1) or failure (0).
func (evm *EVM) doCall(in *interpreter, sc *scope, kind callKind, to []byte,
	value *uint256.Int, requested uint64, args []byte, outOff, outSize uint64) error {

	transfersValue := value != nil && !value.IsZero()
	if (kind == kindStatic || in.readOnly) && transfersValue {
		return ErrStaticStateChange
	}

	available := in.meter.remaining()
	childGas := evm.callEnergy(requested, available)

	// Depth or insufficient balance: the call does not run; push 0, energy untouched.
	if evm.depth >= maxCallDepth ||
		(transfersValue && evm.state.GetBalance(sc.contract.Self).Lt(value)) {
		sc.returnData = nil
		return sc.stack.push(uint256.Int{})
	}

	in.meter.spend(childGas) // reserve (childGas <= available)
	budget := childGas
	if transfersValue {
		budget += stipendCall
	}

	snap := evm.state.Snapshot()
	logMark := len(evm.logs)

	child := &Contract{
		Origin: sc.contract.Origin,
		Input:  args,
		Value:  new(uint256.Int),
	}
	switch kind {
	case kindCall, kindStatic:
		child.Self, child.CodeAddr, child.Caller = to, to, sc.contract.Self
		if transfersValue {
			child.Value.Set(value)
		}
		child.Code = evm.state.GetCode(to)
	case kindCallCode:
		child.Self, child.CodeAddr, child.Caller = sc.contract.Self, to, sc.contract.Self
		if transfersValue {
			child.Value.Set(value)
		}
		child.Code = evm.state.GetCode(to)
	case kindDelegate:
		child.Self, child.CodeAddr, child.Caller = sc.contract.Self, to, sc.contract.Caller
		child.Value.Set(sc.contract.Value)
		child.Code = evm.state.GetCode(to)
	}

	// Value transfer is a real balance move only for CALL (CALLCODE is self->self).
	if kind == kindCall && transfersValue {
		evm.state.SubBalance(sc.contract.Self, value)
		evm.state.AddBalance(to, value)
	}

	// Precompiled contract: run natively instead of as bytecode.
	if pc := lookupPrecompile(to, evm.cfg, evm.perm); pc != nil {
		out, used, perr := runPrecompile(pc, child.Input, budget, evm.cfg)
		in.meter.restore(budget - used)
		if perr != nil {
			evm.state.RevertToSnapshot(snap)
			evm.logs = evm.logs[:logMark]
			sc.returnData = nil
			return sc.stack.push(uint256.Int{})
		}
		sc.returnData = out
		writeReturn(sc.mem, outOff, out, outSize)
		return sc.stack.push(*uint256.NewInt(1))
	}

	evm.depth++
	res := evm.run(child, budget, in.readOnly || kind == kindStatic)
	evm.depth--

	in.meter.restore(budget - res.EnergyUsed)

	if res.Err != nil || res.Reverted {
		evm.state.RevertToSnapshot(snap)
		evm.logs = evm.logs[:logMark]
		if res.Err != nil {
			sc.returnData = nil
		} else {
			sc.returnData = res.Return
			writeReturn(sc.mem, outOff, res.Return, outSize)
		}
		return sc.stack.push(uint256.Int{})
	}
	sc.returnData = res.Return
	writeReturn(sc.mem, outOff, res.Return, outSize)
	return sc.stack.push(*uint256.NewInt(1))
}

// doCreate runs CREATE/CREATE2: derive the new address, run the init code against a
// snapshot, charge code-deposit, and push the new address (or 0 on failure).
func (evm *EVM) doCreate(in *interpreter, sc *scope, value *uint256.Int, initCode, salt []byte, create2 bool) error {
	if in.readOnly {
		return ErrStaticStateChange
	}
	transfersValue := !value.IsZero()
	if evm.depth >= maxCallDepth ||
		(transfersValue && evm.state.GetBalance(sc.contract.Self).Lt(value)) {
		sc.returnData = nil
		return sc.stack.push(uint256.Int{})
	}

	nonce := evm.state.GetNonce(sc.contract.Self)
	evm.state.SetNonce(sc.contract.Self, nonce+1)
	var newAddr []byte
	if create2 {
		newAddr = create2Address(sc.contract.Self, salt, initCode)
	} else {
		newAddr = createAddress(evm.rootTxID, nonce)
	}

	available := in.meter.remaining()
	childGas := evm.callEnergy(available, available)
	in.meter.spend(childGas)

	snap := evm.state.Snapshot()
	logMark := len(evm.logs)
	evm.state.CreateAccount(newAddr)
	if transfersValue {
		evm.state.SubBalance(sc.contract.Self, value)
		evm.state.AddBalance(newAddr, value)
	}

	child := &Contract{
		Self: newAddr, CodeAddr: newAddr, Caller: sc.contract.Self,
		Origin: sc.contract.Origin, Value: value, Code: initCode,
	}
	evm.depth++
	res := evm.run(child, childGas, false)
	evm.depth--
	leftover := childGas - res.EnergyUsed

	if res.Err != nil || res.Reverted {
		evm.state.RevertToSnapshot(snap)
		evm.logs = evm.logs[:logMark]
		in.meter.restore(leftover)
		if res.Err != nil {
			sc.returnData = nil
		} else {
			sc.returnData = res.Return
		}
		return sc.stack.push(uint256.Int{})
	}

	deployCost := uint64(len(res.Return)) * createData
	if deployCost > leftover {
		// Not enough energy to store the returned code: creation fails, all child energy
		// consumed (no refund).
		evm.state.RevertToSnapshot(snap)
		evm.logs = evm.logs[:logMark]
		sc.returnData = nil
		return sc.stack.push(uint256.Int{})
	}
	in.meter.restore(leftover - deployCost)
	evm.state.SetCode(newAddr, res.Return)
	sc.returnData = nil
	return sc.stack.push(addrWord(newAddr))
}

// writeReturn copies up to outSize bytes of a child's return data into memory at outOff.
func writeReturn(mem *Memory, outOff uint64, data []byte, outSize uint64) {
	n := uint64(len(data))
	if n > outSize {
		n = outSize
	}
	if n > 0 {
		mem.set(outOff, data[:n])
	}
}

// Execute runs the top-level (transaction-entry) message frame.
func (evm *EVM) Execute(contract *Contract, input []byte, energyLimit uint64) *Result {
	contract.Input = input
	if contract.CodeAddr == nil {
		contract.CodeAddr = contract.Self
	}
	return evm.run(contract, energyLimit, false)
}

// run builds an interpreter for a single frame and executes it.
func (evm *EVM) run(contract *Contract, energyLimit uint64, readOnly bool) *Result {
	in := &interpreter{
		evm:      evm,
		cfg:      evm.cfg,
		block:    evm.block,
		meter:    newEnergyMeter(energyLimit),
		table:    opTable(),
		readOnly: readOnly,
	}
	sc := &scope{contract: contract, stack: newStack(), mem: newMemory()}
	return in.runFrame(sc)
}
