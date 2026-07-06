package vmoracle

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/resource"
	"github.com/Redchar1992/go-tron/internal/tvm"
)

// mainnetChainID is the value the TVM CHAINID opcode returns on TRON mainnet.
var mainnetChainID = uint256.NewInt(728126428)

// Execute runs the World's tx through the real internal/tvm engine + internal/resource energy
// split and returns the normalized Execution.
//
// The consensus-critical parts are the production functions (tvm.EVM.Execute, resource.Bill,
// resource.AvailableStakedEnergy); the surrounding snapshot/value-transfer/deploy glue mirrors
// internal/actuator vmActuator (which reads from node stores instead of an injected World).
// TODO(M3.5e): extract a shared execution core so the oracle drives the exact actuator path.
func Execute(w World, tx Tx) (Execution, error) {
	owner, err := decodeAddr(tx.Owner)
	if err != nil {
		return Execution{}, fmt.Errorf("owner: %w", err)
	}
	create := tx.Type == "CreateSmartContract"
	if !create && tx.Type != "TriggerSmartContract" {
		return Execution{}, fmt.Errorf("unknown tx type %q", tx.Type)
	}

	// Build state from the World (untracked), then wrap so only execution writes are recorded.
	mem := tvm.NewMemStateDB()
	inputStorage, err := loadWorld(mem, w)
	if err != nil {
		return Execution{}, err
	}
	sdb := &trackingStateDB{StateDB: mem, writes: map[string]map[[32]byte]struct{}{}}

	// Resolve the tx shape.
	var contractAddr, code, input []byte
	var consumeUserPercent, originEnergyLimit int64
	if create {
		if code, err = decodeHex(tx.Bytecode); err != nil {
			return Execution{}, fmt.Errorf("bytecode: %w", err)
		}
		txID, err := decodeHex(tx.TxID)
		if err != nil {
			return Execution{}, fmt.Errorf("txID: %w", err)
		}
		contractAddr = tvm.CreateContractAddress(owner, txID)
	} else {
		if contractAddr, err = decodeAddr(tx.Contract); err != nil {
			return Execution{}, fmt.Errorf("contract: %w", err)
		}
		code = sdb.GetCode(contractAddr)
		if input, err = decodeHex(tx.Data); err != nil {
			return Execution{}, fmt.Errorf("data: %w", err)
		}
		consumeUserPercent = 100 // mirror vmActuator (stored-contract percent still deferred)
	}

	// Caller's available staked energy, derived from the World's globals + owner stake.
	callerEnergy := ownerStakedEnergy(w, tx.Owner)
	energyFee := resource.EnergyPrice(w.DynamicProps.EnergyFee)
	ownerBalance := int64(sdb.GetBalance(owner).Uint64())
	energyLimit := resource.AccountEnergyLimit(callerEnergy, ownerBalance, tx.CallValue, tx.FeeLimit, energyFee)
	var budget uint64
	if energyLimit > 0 {
		budget = uint64(energyLimit)
	}

	witness, _ := decodeHex(w.Block.Witness)
	blockCtx := tvm.BlockContext{
		Number:    w.Block.Number,
		Timestamp: w.Block.Timestamp,
		Coinbase:  witness,
		ChainID:   mainnetChainID.Clone(),
		Version:   w.Version,
	}
	value := uint256.NewInt(uint64(tx.CallValue))

	snap := sdb.Snapshot()
	if create {
		sdb.CreateAccount(contractAddr)
	}
	if tx.CallValue > 0 {
		sdb.SubBalance(owner, value)
		sdb.AddBalance(contractAddr, value)
	}

	evm := tvm.NewEVM(sdb, blockCtx, tvm.VMConfigForVersion(w.Version))
	txID, _ := decodeHex(tx.TxID)
	evm.SetRootTxID(txID)
	frame := &tvm.Contract{
		Self: contractAddr, CodeAddr: contractAddr, Caller: owner, Origin: owner,
		Value: value, Code: code,
	}
	res := evm.Execute(frame, input, budget)
	reverted := res.Err != nil || res.Reverted
	if reverted {
		sdb.RevertToSnapshot(snap)
	} else if create {
		sdb.SetCode(contractAddr, res.Return)
	}

	bill := resource.Bill{
		EnergyUsed:         int64(res.EnergyUsed),
		CallerEnergy:       callerEnergy,
		CallerIsOrigin:     create,
		ConsumeUserPercent: consumeUserPercent,
		OriginEnergyLimit:  originEnergyLimit,
		EnergyPrice:        energyFee,
	}.Compute()

	out := Execution{
		Result:            resultKind(res),
		VMError:           errString(res.Err),
		Return:            hex.EncodeToString(res.Return),
		EnergyUsed:        bill.EnergyUsageTotal,
		EnergyFee:         bill.EnergyFee,
		OriginEnergyUsage: bill.OriginEnergyUsage,
		StorageWrites:     sdb.netWrites(inputStorage),
		Logs:              nil,
	}
	if !reverted {
		out.Logs = collectLogs(evm.Logs())
		if create {
			out.CreatedAddress = hex.EncodeToString(contractAddr)
		}
	}
	return out, nil
}

// ownerStakedEnergy derives the caller's available staked energy from the World. Recovery
// terms (energy_usage / last-consume slot / window) are omitted in v0 (0), so this is the
// full global energy limit; those inputs join the World when real-block seeding lands.
func ownerStakedEnergy(w World, ownerHex string) int64 {
	acct, ok := w.Accounts[ownerHex]
	if !ok {
		return 0
	}
	return resource.AvailableStakedEnergy(resource.StakedEnergyInput{
		FrozenBalanceForEnergy:  acct.EnergyStake,
		TotalEnergyCurrentLimit: w.DynamicProps.TotalEnergyCurrentLimit,
		TotalEnergyWeight:       w.DynamicProps.TotalEnergyWeight,
		SupportUnfreezeDelay:    w.DynamicProps.SupportUnfreezeDelay,
		AllowNewReward:          w.DynamicProps.AllowNewReward,
	})
}

func collectLogs(logs []*tvm.Log) []LogEntry {
	out := make([]LogEntry, 0, len(logs))
	for _, l := range logs {
		topics := make([]string, len(l.Topics))
		for i, t := range l.Topics {
			topics[i] = hex.EncodeToString(t[:])
		}
		out = append(out, LogEntry{
			Address: hex.EncodeToString(l.Address),
			Topics:  topics,
			Data:    hex.EncodeToString(l.Data),
		})
	}
	return out
}

func resultKind(res *tvm.Result) string {
	if res.Reverted {
		return "REVERT"
	}
	switch {
	case res.Err == nil:
		return "SUCCESS"
	case errors.Is(res.Err, tvm.ErrOutOfEnergy):
		return "OUT_OF_ENERGY"
	case errors.Is(res.Err, tvm.ErrInvalidOpcode):
		return "ILLEGAL_OPERATION"
	case errors.Is(res.Err, tvm.ErrBadJumpDest):
		return "BAD_JUMP_DESTINATION"
	case errors.Is(res.Err, tvm.ErrStackUnderflow):
		return "STACK_TOO_SMALL"
	case errors.Is(res.Err, tvm.ErrStackOverflow):
		return "STACK_TOO_LARGE"
	case errors.Is(res.Err, tvm.ErrStaticStateChange):
		return "STATE_CHANGE_IN_STATIC"
	default:
		return "FAULT"
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
