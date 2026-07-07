package actuator

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func delegateTx(t *testing.T, owner, receiver []byte, amount int64, res core.ResourceCode, lock bool) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.DelegateResourceContract{
		OwnerAddress: owner, ReceiverAddress: receiver, Balance: amount, Resource: res, Lock: lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_DelegateResourceContract, Parameter: p,
	}}}}
}

func unDelegateTx(t *testing.T, owner, receiver []byte, amount int64, res core.ResourceCode) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.UnDelegateResourceContract{
		OwnerAddress: owner, ReceiverAddress: receiver, Balance: amount, Resource: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_UnDelegateResourceContract, Parameter: p,
	}}}}
}

func TestV2DelegateEnergyLifecycle(t *testing.T) {
	from, to := addr21(0x51), addr21(0x52)
	st, _ := newChainState(t, from, 10_000_000, 1_600_000_000_000)
	enableStake2(t, st)
	if err := st.Accounts.Put(&core.Account{Address: to, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	// from stakes 5 TRX of energy (V2), then delegates it to `to`.
	mustApply(t, st, freezeV2Tx(t, from, 5_000_000, core.ResourceCode_ENERGY))
	if w, _ := st.Properties.TotalEnergyWeight(); w != 5 {
		t.Fatalf("weight after freezeV2 = %d, want 5", w)
	}

	mustApply(t, st, delegateTx(t, from, to, 5_000_000, core.ResourceCode_ENERGY, false))

	fromA, _ := st.Accounts.Get(from)
	if frozenV2Amount(fromA, core.ResourceCode_ENERGY) != 0 {
		t.Fatalf("owner frozenV2 after delegate = %d, want 0 (all delegated out)",
			frozenV2Amount(fromA, core.ResourceCode_ENERGY))
	}
	if fromA.GetAccountResource().GetDelegatedFrozenV2BalanceForEnergy() != 5_000_000 {
		t.Fatalf("owner delegated-out V2 energy = %d, want 5_000_000",
			fromA.GetAccountResource().GetDelegatedFrozenV2BalanceForEnergy())
	}
	// weight preserved (owner still owns the stake, just delegated its use).
	if w, _ := st.Properties.TotalEnergyWeight(); w != 5 {
		t.Fatalf("weight after delegate = %d, want 5 (unchanged)", w)
	}
	toA, _ := st.Accounts.Get(to)
	if toA.GetAccountResource().GetAcquiredDelegatedFrozenV2BalanceForEnergy() != 5_000_000 {
		t.Fatalf("receiver acquired-delegated V2 energy = %d, want 5_000_000",
			toA.GetAccountResource().GetAcquiredDelegatedFrozenV2BalanceForEnergy())
	}
	if got := allFrozenBalanceForEnergy(toA); got != 5_000_000 {
		t.Fatalf("receiver getAllFrozenBalanceForEnergy = %d, want 5_000_000", got)
	}
	if _, err := st.Delegated.GetV2(from, to, false); err != nil {
		t.Fatalf("unlocked V2 delegation entry missing: %v", err)
	}

	// Undelegate reverses every move.
	mustApply(t, st, unDelegateTx(t, from, to, 5_000_000, core.ResourceCode_ENERGY))
	fromA, _ = st.Accounts.Get(from)
	if frozenV2Amount(fromA, core.ResourceCode_ENERGY) != 5_000_000 ||
		fromA.GetAccountResource().GetDelegatedFrozenV2BalanceForEnergy() != 0 {
		t.Fatalf("owner after undelegate: frozenV2 %d delegated-out %d",
			frozenV2Amount(fromA, core.ResourceCode_ENERGY),
			fromA.GetAccountResource().GetDelegatedFrozenV2BalanceForEnergy())
	}
	toA, _ = st.Accounts.Get(to)
	if toA.GetAccountResource().GetAcquiredDelegatedFrozenV2BalanceForEnergy() != 0 {
		t.Fatal("receiver acquired must be zero after undelegate")
	}
	if _, err := st.Delegated.GetV2(from, to, false); err == nil {
		t.Fatal("emptied V2 delegation entry must be deleted")
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 5 {
		t.Fatalf("weight after undelegate = %d, want 5 (unchanged)", w)
	}
}

func TestV2DelegateValidation(t *testing.T) {
	from, to := addr21(0x53), addr21(0x54)
	st, _ := newChainState(t, from, 10_000_000, 1_600_000_000_000)
	enableStake2(t, st)
	if err := st.Accounts.Put(&core.Account{Address: to, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	mustApply(t, st, freezeV2Tx(t, from, 3_000_000, core.ResourceCode_ENERGY))

	cases := []struct {
		name string
		tx   *core.Transaction
		want string
	}{
		{"lock deferred", delegateTx(t, from, to, 1_000_000, core.ResourceCode_ENERGY, true), "locked resource delegation"},
		{"self delegate", delegateTx(t, from, from, 1_000_000, core.ResourceCode_ENERGY, false), "same as ownerAddress"},
		{"over frozenV2", delegateTx(t, from, to, 5_000_000, core.ResourceCode_ENERGY, false), "exceeds the owner's frozenV2"},
		{"sub-1TRX", delegateTx(t, from, to, 999_999, core.ResourceCode_ENERGY, false), "1 TRX"},
	}
	for _, c := range cases {
		if _, err := Apply(st, c.tx, BlockContext{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}

	// contract receiver rejected.
	cAddr := addr21(0x55)
	if err := st.Accounts.Put(&core.Account{Address: cAddr, Type: core.AccountType_Contract}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(st, delegateTx(t, from, cAddr, 1_000_000, core.ResourceCode_ENERGY, false), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "contract addresses") {
		t.Fatalf("contract receiver: err = %v", err)
	}
}

// TestV2DelegatePowersReceiverVM: A stakes+delegates energy to B (Stake2.0 rental); B's
// contract call is covered by the delegated-in stake though B staked nothing.
func TestV2DelegatePowersReceiverVM(t *testing.T) {
	from, to := addr21(0x56), addr21(0x57)
	st, d := newChainState(t, from, 2_000_000_000, 1_600_000_000_000)
	enableStake2(t, st)
	if err := st.Accounts.Put(&core.Account{Address: to, Balance: 100_000_000, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}

	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	res := applyInSession(t, st, d, createTx(t, to, deployer(runtime)), 1)
	contractAddr := res.Receipts[0].ContractAddress

	// A: freezeV2 1000 TRX energy, then delegate all to B.
	d.BuildSession()
	blk := BlockContext{Number: 2, Timestamp: 6000, Version: 35}
	if _, err := Apply(st, freezeV2Tx(t, from, 1_000_000_000, core.ResourceCode_ENERGY), blk); err != nil {
		t.Fatalf("freezeV2: %v", err)
	}
	if _, err := Apply(st, delegateTx(t, from, to, 1_000_000_000, core.ResourceCode_ENERGY, false), blk); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}

	// B triggers its contract: covered by the delegated-in energy.
	res = applyInSession(t, st, d, triggerTx(t, to, contractAddr, nil), 3)
	if bill := res.Receipts[0].Energy; bill.EnergyUsage <= 0 || bill.EnergyFee != 0 {
		t.Fatalf("receiver receipt = %+v, want delegated-stake-covered", bill)
	}
}
