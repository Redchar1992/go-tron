package actuator

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// enableStake2 opens Stake2.0: UNFREEZE_DELAY_DAYS = 14.
func enableStake2(t *testing.T, st *state.State) {
	t.Helper()
	if err := st.Properties.PutInt64([]byte("UNFREEZE_DELAY_DAYS"), 14); err != nil {
		t.Fatal(err)
	}
}

func freezeV2Tx(t *testing.T, owner []byte, amount int64, res core.ResourceCode) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.FreezeBalanceV2Contract{OwnerAddress: owner, FrozenBalance: amount, Resource: res})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_FreezeBalanceV2Contract, Parameter: p,
	}}}}
}

func unfreezeV2Tx(t *testing.T, owner []byte, amount int64, res core.ResourceCode) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.UnfreezeBalanceV2Contract{OwnerAddress: owner, UnfreezeBalance: amount, Resource: res})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_UnfreezeBalanceV2Contract, Parameter: p,
	}}}}
}

func withdrawTx(t *testing.T, owner []byte) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.WithdrawExpireUnfreezeContract{OwnerAddress: owner})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_WithdrawExpireUnfreezeContract, Parameter: p,
	}}}}
}

func TestFreezeV2RejectedPreStake2(t *testing.T) {
	owner := addr21(0x31)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000) // UNFREEZE_DELAY_DAYS = 0
	if _, err := Apply(st, freezeV2Tx(t, owner, 5_000_000, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "not support FreezeV2") {
		t.Fatalf("pre-Stake2.0 FreezeV2 must be rejected, got %v", err)
	}
}

func TestFreezeV2Energy(t *testing.T) {
	owner := addr21(0x32)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000)
	enableStake2(t, st)

	mustApply(t, st, freezeV2Tx(t, owner, 3_000_000, core.ResourceCode_ENERGY))
	acct, _ := st.Accounts.Get(owner)
	if frozenV2Amount(acct, core.ResourceCode_ENERGY) != 3_000_000 {
		t.Fatalf("frozenV2 energy = %d, want 3_000_000", frozenV2Amount(acct, core.ResourceCode_ENERGY))
	}
	if acct.GetBalance() != 7_000_000 {
		t.Fatalf("balance = %d, want 7_000_000", acct.GetBalance())
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 3 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 3", w)
	}
	// A second freeze accumulates into the same entry.
	mustApply(t, st, freezeV2Tx(t, owner, 2_000_000, core.ResourceCode_ENERGY))
	acct, _ = st.Accounts.Get(owner)
	if frozenV2Amount(acct, core.ResourceCode_ENERGY) != 5_000_000 {
		t.Fatalf("frozenV2 after 2nd freeze = %d, want 5_000_000", frozenV2Amount(acct, core.ResourceCode_ENERGY))
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 5 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 5", w)
	}
}

func TestUnfreezeV2AndWithdrawLifecycle(t *testing.T) {
	owner := addr21(0x33)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, owner, 10_000_000, t0)
	enableStake2(t, st)
	mustApply(t, st, freezeV2Tx(t, owner, 5_000_000, core.ResourceCode_ENERGY))

	// Unfreeze 4 TRX: moves it into an UnfrozenV2 entry maturing in 14 days; weight drops.
	mustApply(t, st, unfreezeV2Tx(t, owner, 4_000_000, core.ResourceCode_ENERGY))
	acct, _ := st.Accounts.Get(owner)
	if frozenV2Amount(acct, core.ResourceCode_ENERGY) != 1_000_000 {
		t.Fatalf("frozenV2 after unfreeze = %d, want 1_000_000", frozenV2Amount(acct, core.ResourceCode_ENERGY))
	}
	if len(acct.GetUnfrozenV2()) != 1 || acct.GetUnfrozenV2()[0].GetUnfreezeAmount() != 4_000_000 {
		t.Fatalf("unfrozenV2 = %+v", acct.GetUnfrozenV2())
	}
	if acct.GetUnfrozenV2()[0].GetUnfreezeExpireTime() != t0+14*dayMs {
		t.Fatalf("unfreeze expire = %d, want t0+14d", acct.GetUnfrozenV2()[0].GetUnfreezeExpireTime())
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 1 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT after unfreeze = %d, want 1", w)
	}
	if acct.GetBalance() != 5_000_000 {
		t.Fatalf("balance during unfreezing must be unchanged, got %d", acct.GetBalance())
	}

	// Withdraw before maturity: nothing.
	mustApply(t, st, withdrawTx(t, owner))
	acct, _ = st.Accounts.Get(owner)
	if acct.GetBalance() != 5_000_000 || len(acct.GetUnfrozenV2()) != 1 {
		t.Fatalf("premature withdraw changed state: bal %d unfrozen %d", acct.GetBalance(), len(acct.GetUnfrozenV2()))
	}

	// After maturity: the 4 TRX returns to spendable balance.
	if err := st.Properties.SaveLatestBlockHeaderTimestamp(t0 + 14*dayMs); err != nil {
		t.Fatal(err)
	}
	mustApply(t, st, withdrawTx(t, owner))
	acct, _ = st.Accounts.Get(owner)
	if acct.GetBalance() != 9_000_000 || len(acct.GetUnfrozenV2()) != 0 {
		t.Fatalf("after mature withdraw: bal %d (want 9_000_000) unfrozen %d (want 0)",
			acct.GetBalance(), len(acct.GetUnfrozenV2()))
	}
}

func TestUnfreezeV2Validation(t *testing.T) {
	owner := addr21(0x34)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000)
	enableStake2(t, st)
	mustApply(t, st, freezeV2Tx(t, owner, 3_000_000, core.ResourceCode_ENERGY))

	// unfreeze more than frozen -> error.
	if _, err := Apply(st, unfreezeV2Tx(t, owner, 4_000_000, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "invalid unfreeze_balance") {
		t.Fatalf("over-unfreeze: err = %v", err)
	}
	// unfreeze a resource with no stake -> error.
	if _, err := Apply(st, unfreezeV2Tx(t, owner, 1_000_000, core.ResourceCode_BANDWIDTH), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "no frozenBalance") {
		t.Fatalf("no-stake unfreeze: err = %v", err)
	}
}

// TestFreezeV2PowersVM: on a Stake2.0 chain, FreezeBalanceV2(ENERGY) makes the caller's
// contract call stake-covered (the V2-fractional derivation running on real state).
func TestFreezeV2PowersVM(t *testing.T) {
	owner := addr21(0x35)
	st, d := newChainState(t, owner, 2_000_000_000, 1_600_000_000_000)
	enableStake2(t, st)

	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	res := applyInSession(t, st, d, createTx(t, owner, deployer(runtime)), 1)
	contractAddr := res.Receipts[0].ContractAddress

	// Pre-freeze: all burned.
	res = applyInSession(t, st, d, triggerTx(t, owner, contractAddr, nil), 2)
	if res.Receipts[0].Energy.EnergyFee <= 0 {
		t.Fatalf("pre-freeze want burn, got %+v", res.Receipts[0].Energy)
	}

	// FreezeV2 1000 TRX of energy.
	d.BuildSession()
	if _, err := Apply(st, freezeV2Tx(t, owner, 1_000_000_000, core.ResourceCode_ENERGY),
		BlockContext{Number: 3, Timestamp: 9000, Version: 35}); err != nil {
		t.Fatalf("freezeV2: %v", err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 1000 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 1000", w)
	}

	// Post-freeze: stake-covered.
	res = applyInSession(t, st, d, triggerTx(t, owner, contractAddr, nil), 4)
	if post := res.Receipts[0].Energy; post.EnergyUsage <= 0 || post.EnergyFee != 0 {
		t.Fatalf("post-freezeV2 want stake-covered, got %+v", post)
	}
}
