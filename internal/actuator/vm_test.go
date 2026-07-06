package actuator

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// addr21 builds a 21-byte 0x41-prefixed TRON address filled with b.
func addr21(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	for i := 1; i < 21; i++ {
		a[i] = b
	}
	return a
}

// deployer wraps runtime code in a minimal init/deployer: CODECOPY the runtime (which the
// prefix places at code offset 0x0c) into memory and RETURN it. Works for runtime < 256 B.
func deployer(runtime []byte) []byte {
	n := byte(len(runtime))
	prefix := []byte{
		0x60, n, // PUSH1 len
		0x60, 0x0c, // PUSH1 0x0c (runtime offset in this init code)
		0x60, 0x00, // PUSH1 0x00 (dest mem offset)
		0x39,    // CODECOPY
		0x60, n, // PUSH1 len
		0x60, 0x00, // PUSH1 0x00
		0xf3, // RETURN
	}
	return append(prefix, runtime...)
}

func newState(t *testing.T, owner []byte, balance int64) (*state.State, *db.Database) {
	t.Helper()
	d := db.NewDatabase(db.NewMemKV())
	st := state.New(d)
	if err := st.Accounts.Put(&core.Account{Address: owner, Balance: balance}); err != nil {
		t.Fatal(err)
	}
	return st, d
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
			Type:      core.Transaction_Contract_CreateSmartContract,
			Parameter: param,
		}},
		FeeLimit: 1_000_000_000,
	}}
}

func triggerTx(t *testing.T, owner, contract, data []byte) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.TriggerSmartContract{
		OwnerAddress:    owner,
		ContractAddress: contract,
		Data:            data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type:      core.Transaction_Contract_TriggerSmartContract,
			Parameter: param,
		}},
		FeeLimit: 1_000_000_000,
	}}
}

// applyInSession mirrors the Manager: open a revoking session, apply, commit.
func applyInSession(t *testing.T, st *state.State, d *db.Database, tx *core.Transaction, num int64) ApplyResult {
	t.Helper()
	d.BuildSession()
	// Version 35 (tvm.LatestForkVersion) → all fork gates on, matching the modern era
	// these VM vectors were written for (the prior hardcoded tvm.LatestVMConfig()).
	res, err := Apply(st, tx, BlockContext{Number: num, Timestamp: num * 3000, Version: 35})
	if err != nil {
		d.Revoke()
		t.Fatalf("apply: %v", err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

var slot0 [32]byte // the all-zero storage key

// TestVMDeployAndTriggerPersists deploys a contract that stores 42 at slot 0 on trigger,
// then triggers it in a later block, and checks the code + storage persisted across the
// two separate transactions (each with its own StateDB adapter).
func TestVMDeployAndTriggerPersists(t *testing.T) {
	owner := addr21(0x11)
	st, d := newState(t, owner, 1_000_000_000_000)

	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00} // PUSH1 42; PUSH1 0; SSTORE; STOP
	dep := applyInSession(t, st, d, createTx(t, owner, deployer(runtime)), 1)
	if len(dep.Receipts) != 1 {
		t.Fatalf("deploy receipts = %d, want 1", len(dep.Receipts))
	}
	addr := dep.Receipts[0].ContractAddress
	if dep.Receipts[0].Reverted {
		t.Fatal("deploy reverted")
	}

	code, err := st.Contracts.GetCode(addr)
	if err != nil {
		t.Fatalf("deployed code missing: %v", err)
	}
	if !bytes.Equal(code, runtime) {
		t.Fatalf("deployed code = %x, want %x", code, runtime)
	}

	// Storage is untouched until the contract is triggered.
	if _, present, _ := st.Storage.Get(addr, slot0); present {
		t.Fatal("slot 0 set before trigger")
	}

	trig := applyInSession(t, st, d, triggerTx(t, owner, addr, nil), 2)
	if trig.Receipts[0].Reverted {
		t.Fatalf("trigger reverted: %s", trig.Receipts[0].VMError)
	}

	v, present, err := st.Storage.Get(addr, slot0)
	if err != nil {
		t.Fatal(err)
	}
	if !present || v[31] != 0x2a {
		t.Fatalf("slot 0 = %x present=%v, want ...2a present=true", v, present)
	}
}

// TestVMRevertRollsBack triggers a contract that writes storage then REVERTs: the write
// must not persist, but the caller must still be charged for the consumed energy.
func TestVMRevertRollsBack(t *testing.T) {
	owner := addr21(0x22)
	st, d := newState(t, owner, 1_000_000_000_000)

	// PUSH1 42; PUSH1 0; SSTORE; PUSH1 0; PUSH1 0; REVERT
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x60, 0x00, 0x60, 0x00, 0xfd}
	addr := applyInSession(t, st, d, createTx(t, owner, deployer(runtime)), 1).Receipts[0].ContractAddress

	before, _ := st.Accounts.Get(owner)
	trig := applyInSession(t, st, d, triggerTx(t, owner, addr, nil), 2)
	rec := trig.Receipts[0]

	if !rec.Reverted {
		t.Fatal("expected reverted trigger")
	}
	if _, present, _ := st.Storage.Get(addr, slot0); present {
		t.Fatal("storage write survived a revert")
	}
	if rec.Energy.EnergyFee <= 0 {
		t.Fatalf("reverted tx charged no energy: %+v", rec.Energy)
	}
	after, _ := st.Accounts.Get(owner)
	if got := before.GetBalance() - after.GetBalance(); got != rec.Energy.EnergyFee {
		t.Fatalf("owner debited %d, want energy_fee %d", got, rec.Energy.EnergyFee)
	}
}

// TestVMReceiptMatchesBill checks the receipt is the M3.3 energy Bill: with no staked
// energy (M3.5a), the caller burns the whole bill as TRX at the 100-sun floor price.
func TestVMReceiptMatchesBill(t *testing.T) {
	owner := addr21(0x33)
	st, d := newState(t, owner, 1_000_000_000_000)

	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	addr := applyInSession(t, st, d, createTx(t, owner, deployer(runtime)), 1).Receipts[0].ContractAddress

	rec := applyInSession(t, st, d, triggerTx(t, owner, addr, nil), 2).Receipts[0]
	e := rec.Energy
	if e.EnergyUsageTotal <= 0 {
		t.Fatalf("no energy consumed: %+v", e)
	}
	if e.EnergyUsage != 0 || e.OriginEnergyUsage != 0 {
		t.Fatalf("expected all-burned bill, got %+v", e)
	}
	if e.EnergyFee != e.EnergyUsageTotal*100 {
		t.Fatalf("energy_fee = %d, want total(%d) * 100", e.EnergyFee, e.EnergyUsageTotal)
	}
}
