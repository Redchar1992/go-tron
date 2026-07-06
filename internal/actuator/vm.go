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
}

// vmActuator executes CreateSmartContract (create=true) and TriggerSmartContract on the
// TVM. It is the M3.5a bridge wiring the finished internal/tvm engine into the actuator
// pipeline: build a StateDB over the node stores, run the EVM, apply the energy bill, and
// flush on success.
//
// M3.5a deliberately simplifies the parts that need historical resource state or dynamic
// properties (all deferred to later M3.5 slices, and marked below):
//   - the caller burns ALL consumed energy as TRX at the floor price (no staked-energy
//     derivation yet), so origin-pays / free-energy splitting is a no-op here;
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

	ownerBalance := int64(sdb.GetBalance(owner).Uint64())
	energyLimit := resource.AccountEnergyLimit(0, ownerBalance, callValue,
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

	evm := tvm.NewEVM(sdb, blockCtx, tvm.LatestVMConfig())
	evm.SetRootTxID(ctx.TxID)
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
		ConsumeUserPercent: consumeUserPercent,
		OriginEnergyLimit:  originEnergyLimit,
		EnergyPrice:        defaultEnergyFee,
	}.Compute()
	if bill.EnergyFee > 0 {
		sdb.SubBalance(owner, uint256.NewInt(uint64(bill.EnergyFee)))
	}

	if err := sdb.Flush(); err != nil {
		return fmt.Errorf("actuator: vm state flush: %w", err)
	}

	ctx.Receipt = &Receipt{
		Energy:          bill,
		ContractAddress: contractAddr,
		Return:          res.Return,
		Reverted:        reverted,
		VMError:         errString(res.Err),
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
