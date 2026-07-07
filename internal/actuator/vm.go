package actuator

import (
	"fmt"

	"github.com/holiman/uint256"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/resource"
	"github.com/Redchar1992/go-tron/internal/tvm"
)

// mainnetChainID is the value the TVM CHAINID opcode returns on TRON mainnet.
var mainnetChainID = uint256.NewInt(728126428)

// defaultEnergyFee is the sun-per-energy price the bill uses until dynamic-property wiring
// (energy_fee from the DynamicPropertiesStore) lands in M3.5d. The 100-sun floor is the
// long-standing mainnet-genesis value.
const defaultEnergyFee = resource.SunPerEnergyFloor

// Receipt is the outcome of a VM-executed transaction: the energy split (M3.3 Bill) plus
// the execution result. M3.5b threads these into the differential fixtures; for now they
// let the pipeline (and tests) observe what the VM did.
type Receipt struct {
	Energy          resource.Receipt
	ContractAddress []byte // 21-byte 0x41 address (deployed addr for CreateSmartContract)
	Return          []byte
	Reverted        bool
	VMError         string
	Logs            []*tvm.Log // LOG0..LOG4 events; empty when the top-level frame reverted
}

// vmActuator executes CreateSmartContract (create=true) and TriggerSmartContract on the
// TVM. It is the M3.5a bridge wiring the finished internal/tvm engine into the actuator
// pipeline: build a StateDB over the node stores, run the EVM, apply the energy bill, and
// flush on success.
//
// M3.5a deliberately simplifies the parts that need historical resource state or dynamic
// properties (all deferred to later M3.5 slices, and marked below):
//   - staked-energy derivation is wired (M3.5d, see energy.go) and now reads the network
//     globals from the PropertyStore (total energy weight / current limit). On a
//     from-genesis chain TOTAL_ENERGY_WEIGHT is still 0 (freeze actuators that grow it are a
//     later milestone), so the derivation evaluates to 0 and the caller burns ALL consumed
//     energy as TRX at the floor price — origin-pays / free-energy splitting activates
//     automatically once the weight becomes positive;
//   - consume_user_resource_percent for a Trigger is not read back from stored contract
//     metadata (only runtime code is persisted in M3.5a);
//   - validation is minimal (unpack only).
type vmActuator struct{ create bool }

func (a vmActuator) Validate(ctx *Context) error {
	if a.create {
		csc := new(core.CreateSmartContract)
		if err := ctx.Contract.GetParameter().UnmarshalTo(csc); err != nil {
			return fmt.Errorf("unpack CreateSmartContract: %w", err)
		}
		if csc.GetNewContract() == nil {
			return fmt.Errorf("actuator: CreateSmartContract has no new_contract")
		}
		if len(csc.GetOwnerAddress()) == 0 {
			return fmt.Errorf("actuator: CreateSmartContract missing owner")
		}
		return nil
	}
	tsc := new(core.TriggerSmartContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(tsc); err != nil {
		return fmt.Errorf("unpack TriggerSmartContract: %w", err)
	}
	if len(tsc.GetContractAddress()) == 0 {
		return fmt.Errorf("actuator: TriggerSmartContract missing contract address")
	}
	return nil
}

func (a vmActuator) Execute(ctx *Context) error {
	sdb := newVMStateDB(ctx.State, ctx.Block.Provider)

	var owner, contractAddr, code, input []byte
	var callValue, consumeUserPercent, originEnergyLimit int64

	if a.create {
		csc := new(core.CreateSmartContract)
		if err := ctx.Contract.GetParameter().UnmarshalTo(csc); err != nil {
			return fmt.Errorf("unpack CreateSmartContract: %w", err)
		}
		sc := csc.GetNewContract()
		owner = csc.GetOwnerAddress()
		contractAddr = tvm.CreateContractAddress(owner, ctx.TxID)
		code = sc.GetBytecode()
		callValue = sc.GetCallValue()
		consumeUserPercent = sc.GetConsumeUserResourcePercent()
		originEnergyLimit = sc.GetOriginEnergyLimit()
	} else {
		tsc := new(core.TriggerSmartContract)
		if err := ctx.Contract.GetParameter().UnmarshalTo(tsc); err != nil {
			return fmt.Errorf("unpack TriggerSmartContract: %w", err)
		}
		owner = tsc.GetOwnerAddress()
		contractAddr = tsc.GetContractAddress()
		code = sdb.GetCode(contractAddr)
		input = tsc.GetData()
		callValue = tsc.GetCallValue()
		consumeUserPercent = 100 // deferred (M3.5d): read the stored contract's setting
	}

	// Derive the caller's currently-available staked energy from stored account state
	// (see energy.go). The network globals come from the PropertyStore; the freeze
	// actuators (freeze.go) grow TOTAL_ENERGY_WEIGHT and per-account stakes, so on a chain
	// that has seen FreezeBalanceContract(ENERGY) this is non-zero and the caller pays from
	// stake before burning TRX. Resource "now" is LATEST_BLOCK_HEADER_TIMESTAMP — the
	// PREVIOUS block's timestamp during processing (java-tron getLatestBlockHeaderTimestamp
	// semantics; see PropertyStore) — not this block's header time.
	props, err := energyDynamicPropsFromState(ctx.State)
	if err != nil {
		return fmt.Errorf("actuator: read energy properties: %w", err)
	}
	nowMs, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return fmt.Errorf("actuator: read header timestamp: %w", err)
	}
	callerAcct := lookupAccount(ctx, owner)
	callerEnergy := availableStakedEnergy(callerAcct, nowMs, props)

	ownerBalance := int64(sdb.GetBalance(owner).Uint64())
	energyLimit := resource.AccountEnergyLimit(callerEnergy, ownerBalance, callValue,
		ctx.Tx.GetRawData().GetFeeLimit(), defaultEnergyFee)
	var budget uint64
	if energyLimit > 0 {
		budget = uint64(energyLimit)
	}

	blockCtx := tvm.BlockContext{
		Number:    ctx.Block.Number,
		Timestamp: ctx.Block.Timestamp,
		Coinbase:  ctx.Block.Witness,
		ChainID:   mainnetChainID.Clone(),
		Version:   ctx.Block.Version,
	}
	value := uint256.NewInt(uint64(callValue))

	// Snapshot before any state mutation so a revert/fault restores the pre-call state
	// (including the value transfer). The energy burn is applied AFTER the revert so it
	// still costs the caller even on a reverted execution (java-tron semantics).
	snap := sdb.Snapshot()
	if a.create {
		sdb.CreateAccount(contractAddr)
	}
	if callValue > 0 {
		sdb.SubBalance(owner, value)
		sdb.AddBalance(contractAddr, value)
	}

	// Resolve the TVM fork gates from the block's header version (not the hardcoded
	// latest preset) so historical blocks replay with the exact gate set they ran under.
	evm := tvm.NewEVM(sdb, blockCtx, tvm.VMConfigForVersion(blockCtx.Version))
	evm.SetRootTxID(ctx.TxID)
	evm.SetPermissionReader(accountPermissions{ctx.State}) // validatemultisign (0x0a) state source
	frame := &tvm.Contract{
		Self:     contractAddr,
		CodeAddr: contractAddr,
		Caller:   owner,
		Origin:   owner,
		Value:    value,
		Code:     code,
	}
	res := evm.Execute(frame, input, budget)
	reverted := res.Err != nil || res.Reverted
	if reverted {
		sdb.RevertToSnapshot(snap)
	} else if a.create {
		// Deploy the returned runtime code. (Deploy-cost energy charging is folded into
		// res.EnergyUsed for M3.5a; precise code-deposit accounting is M3.5d.)
		sdb.SetCode(contractAddr, res.Return)
	}

	bill := resource.Bill{
		EnergyUsed:         int64(res.EnergyUsed),
		CallerEnergy:       callerEnergy,
		CallerIsOrigin:     a.create, // a CreateSmartContract's caller IS the contract origin
		ConsumeUserPercent: consumeUserPercent,
		OriginEnergyLimit:  originEnergyLimit,
		EnergyPrice:        defaultEnergyFee,
		// OriginEnergy for a Trigger (the contract deployer's stake) is deferred together
		// with the stored-contract metadata — consume_user_resource_percent is likewise
		// hardcoded 100 above — so the caller currently bears 100%: no origin split to fund.
	}.Compute()
	if bill.EnergyFee > 0 {
		sdb.SubBalance(owner, uint256.NewInt(uint64(bill.EnergyFee)))
	}

	if err := sdb.Flush(); err != nil {
		return fmt.Errorf("actuator: vm state flush: %w", err)
	}

	// Record the caller's staked-energy consumption (EnergyProcessor.useEnergy): decay-and-
	// add the usage average and stamp the consume slot, so later transactions see depleted
	// (and time-recovering) stake instead of an infinite well. java-tron runs this for every
	// contract tx — including reverted and all-burned ones (usage 0 still stamps the slot).
	// After Flush so the fee-debited balance is not clobbered. The origin-side write-back is
	// deferred with the origin-stake metadata (OriginEnergy is always 0 above).
	if err := chargeCallerEnergy(ctx, owner, bill.EnergyUsage, nowMs); err != nil {
		return fmt.Errorf("actuator: energy usage write-back: %w", err)
	}

	// Harvest event logs only on a non-reverted top-level frame; a reverted tx emits none
	// (nested-frame reverts already discarded theirs inside the EVM).
	var logs []*tvm.Log
	if !reverted {
		logs = evm.Logs()
	}
	ctx.Receipt = &Receipt{
		Energy:          bill,
		ContractAddress: contractAddr,
		Return:          res.Return,
		Reverted:        reverted,
		VMError:         errString(res.Err),
		Logs:            logs,
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
