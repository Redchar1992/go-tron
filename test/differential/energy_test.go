package differential

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/actuator"
	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/node"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/replay"
)

// energyVerdict classifies a contract tx when diffing our receipt against the on-chain one.
type energyVerdict int

const (
	energyMatched   energyVerdict = iota // our energy split equals the on-chain receipt
	energyMismatch                       // a modeled field disagrees (a P0 for M3.5c)
	energyUnmodeled                      // the on-chain receipt carries no energy to diff
)

// compareEnergy diffs go-tron's computed receipt against the on-chain receipt for a
// contract transaction. This is the M3.5b energy-receipt oracle; M3.5c feeds it real
// mainnet contract receipts (which need the historical-state oracle to reproduce).
func compareEnergy(ours actuator.Receipt, chain replay.Receipt) energyVerdict {
	if chain.EnergyUsageTotal == 0 {
		return energyUnmodeled
	}
	if ours.Energy.EnergyUsageTotal == chain.EnergyUsageTotal &&
		ours.Energy.EnergyUsage == chain.EnergyUsage &&
		ours.Energy.OriginEnergyUsage == chain.OriginEnergyUsage &&
		ours.Energy.EnergyFee == chain.EnergyFee {
		return energyMatched
	}
	return energyMismatch
}

func addr21(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	for i := 1; i < 21; i++ {
		a[i] = b
	}
	return a
}

// deployer wraps runtime code in a minimal init that CODECOPYs the runtime (placed at code
// offset 0x0c) into memory and RETURNs it.
func deployer(runtime []byte) []byte {
	n := byte(len(runtime))
	return append([]byte{
		0x60, n, 0x60, 0x0c, 0x60, 0x00, 0x39, // CODECOPY(dest=0, off=0x0c, len=n)
		0x60, n, 0x60, 0x00, 0xf3, // RETURN(off=0, len=n)
	}, runtime...)
}

func createTx(t *testing.T, owner, initCode []byte) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.CreateSmartContract{
		OwnerAddress: owner,
		NewContract: &core.SmartContract{
			OriginAddress:              owner,
			Bytecode:                   initCode,
			ConsumeUserResourcePercent: 100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type: core.Transaction_Contract_CreateSmartContract, Parameter: param,
		}},
		FeeLimit: 1_000_000_000,
	}}
}

func triggerTx(t *testing.T, owner, contract, data []byte) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.TriggerSmartContract{
		OwnerAddress: owner, ContractAddress: contract, Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type: core.Transaction_Contract_TriggerSmartContract, Parameter: param,
		}},
		FeeLimit: 1_000_000_000,
	}}
}

// mkBlock builds a valid core.Block: the header txTrieRoot is the root computed from txs
// (so Manager.validateBlock passes) and parent links to the given id.
func mkBlock(t *testing.T, num int64, parent []byte, txs []*core.Transaction) *core.Block {
	t.Helper()
	root, err := block.CalcTxTrieRoot(txs)
	if err != nil {
		t.Fatal(err)
	}
	return &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{
			Number:         num,
			ParentHash:     parent,
			TxTrieRoot:     root,
			Timestamp:      num * 3000,
			WitnessAddress: addr21(0xee),
		}},
		Transactions: txs,
	}
}

// TestContractReplayEnergyReceipt replays a self-contained deploy->trigger span through the
// node Manager, captures the VM receipts via the receipt sink, and runs the M3.5b
// energy-receipt oracle against them. Because the span is contiguous (the contract is
// deployed within the replay), no historical-state oracle is needed — that dependency is
// only for mid-chain real-block replay (M3.5c).
func TestContractReplayEnergyReceipt(t *testing.T) {
	owner := addr21(0x11)
	m := node.NewManager(db.NewDatabase(db.NewMemKV()), 0)
	if err := m.State().Accounts.Put(&core.Account{Address: owner, Balance: 1_000_000_000_000}); err != nil {
		t.Fatal(err)
	}

	byBlock := map[int64][]*actuator.Receipt{}
	m.SetReceiptSink(func(num int64, rs []*actuator.Receipt) { byBlock[num] = rs })

	root := mkBlock(t, 0, nil, nil)
	if err := m.Start(root); err != nil {
		t.Fatal(err)
	}
	rootID, _ := block.ID(root)

	// Block 1: deploy a contract whose runtime stores 42 at slot 0 on trigger.
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00} // PUSH1 42; PUSH1 0; SSTORE; STOP
	b1 := mkBlock(t, 1, rootID, []*core.Transaction{createTx(t, owner, deployer(runtime))})
	if err := m.PushBlock(b1); err != nil {
		t.Fatalf("deploy block: %v", err)
	}
	if len(byBlock[1]) != 1 {
		t.Fatalf("deploy receipts = %d, want 1", len(byBlock[1]))
	}
	addr := byBlock[1][0].ContractAddress
	if byBlock[1][0].Reverted {
		t.Fatal("deploy reverted")
	}

	// Block 2: trigger the deployed contract. The Manager's own stores carry the contract
	// from block 1 (via the still-open revoking sessions).
	b1ID := m.Head().ID
	b2 := mkBlock(t, 2, b1ID, []*core.Transaction{triggerTx(t, owner, addr, nil)})
	if err := m.PushBlock(b2); err != nil {
		t.Fatalf("trigger block: %v", err)
	}
	rec := byBlock[2][0]
	if rec.Reverted {
		t.Fatalf("trigger reverted: %s", rec.VMError)
	}
	if rec.Energy.EnergyUsageTotal <= 0 {
		t.Fatalf("no energy consumed: %+v", rec.Energy)
	}

	// The energy-receipt oracle: a self-consistent chain receipt matches; a perturbed one is
	// a mismatch (a P0 when this runs against real mainnet receipts in M3.5c); an
	// energy-less receipt is unmodeled.
	chain := replay.Receipt{
		EnergyUsage:       rec.Energy.EnergyUsage,
		OriginEnergyUsage: rec.Energy.OriginEnergyUsage,
		EnergyFee:         rec.Energy.EnergyFee,
		EnergyUsageTotal:  rec.Energy.EnergyUsageTotal,
	}
	if v := compareEnergy(*rec, chain); v != energyMatched {
		t.Fatalf("compareEnergy(consistent) = %d, want matched", v)
	}
	perturbed := chain
	perturbed.EnergyFee += 100
	if v := compareEnergy(*rec, perturbed); v != energyMismatch {
		t.Fatalf("compareEnergy(perturbed) = %d, want mismatch", v)
	}
	if v := compareEnergy(*rec, replay.Receipt{}); v != energyUnmodeled {
		t.Fatalf("compareEnergy(no-energy) = %d, want unmodeled", v)
	}

	// Cross-block state check: the SSTORE from the trigger is visible in the node stores
	// (through the open sessions), confirming the deploy->trigger state carried across.
	if v, present, _ := m.State().Storage.Get(addr, [32]byte{}); !present || v[31] != 0x2a {
		t.Fatalf("slot 0 = %x present=%v, want ...2a", v, present)
	}
}
