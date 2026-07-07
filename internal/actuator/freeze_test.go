package actuator

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

const dayMs = 86_400_000

// newChainState builds a genesis-seeded state with one funded owner and the header
// timestamp set to t0 (the "now" actuators read).
func newChainState(t *testing.T, owner []byte, balance, t0 int64) (*state.State, *db.Database) {
	t.Helper()
	st, d := newState(t, owner, balance)
	if err := st.Properties.SeedGenesisDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := st.Properties.SaveLatestBlockHeaderTimestamp(t0); err != nil {
		t.Fatal(err)
	}
	return st, d
}

func freezeTx(t *testing.T, owner []byte, amount, days int64, res core.ResourceCode) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.FreezeBalanceContract{
		OwnerAddress: owner, FrozenBalance: amount, FrozenDuration: days, Resource: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type: core.Transaction_Contract_FreezeBalanceContract, Parameter: param,
		}},
	}}
}

func unfreezeTx(t *testing.T, owner []byte, res core.ResourceCode) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.UnfreezeBalanceContract{OwnerAddress: owner, Resource: res})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type: core.Transaction_Contract_UnfreezeBalanceContract, Parameter: param,
		}},
	}}
}

func mustApply(t *testing.T, st *state.State, tx *core.Transaction) {
	t.Helper()
	if _, err := Apply(st, tx, BlockContext{Number: 1, Timestamp: 3000, Version: 35}); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestFreezeBandwidth(t *testing.T) {
	owner := addr21(0x11)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, owner, 10_000_000, t0)

	mustApply(t, st, freezeTx(t, owner, 5_000_000, 3, core.ResourceCode_BANDWIDTH))

	acct, _ := st.Accounts.Get(owner)
	if acct.GetBalance() != 5_000_000 {
		t.Fatalf("balance = %d, want 5_000_000", acct.GetBalance())
	}
	fz := acct.GetFrozen()
	if len(fz) != 1 || fz[0].GetFrozenBalance() != 5_000_000 || fz[0].GetExpireTime() != t0+3*dayMs {
		t.Fatalf("frozen = %+v, want 5_000_000 expiring at t0+3d", fz)
	}
	if w, _ := st.Properties.TotalNetWeight(); w != 5 {
		t.Fatalf("TOTAL_NET_WEIGHT = %d, want 5", w)
	}
}

func TestFreezeEnergyFloorWeight(t *testing.T) {
	owner := addr21(0x12)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000)

	// Two 1.5-TRX freezes: account total 3 TRX, but the pre-allowNewReward weight rule adds
	// floor(1.5)=1 per freeze -> TOTAL_ENERGY_WEIGHT 2 (the historic floor-drift).
	mustApply(t, st, freezeTx(t, owner, 1_500_000, 3, core.ResourceCode_ENERGY))
	mustApply(t, st, freezeTx(t, owner, 1_500_000, 3, core.ResourceCode_ENERGY))

	acct, _ := st.Accounts.Get(owner)
	if got := acct.GetAccountResource().GetFrozenBalanceForEnergy().GetFrozenBalance(); got != 3_000_000 {
		t.Fatalf("energy frozen = %d, want 3_000_000", got)
	}
	if acct.GetBalance() != 7_000_000 {
		t.Fatalf("balance = %d, want 7_000_000", acct.GetBalance())
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 2 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 2 (floor per freeze)", w)
	}
}

func TestFreezeValidation(t *testing.T) {
	owner := addr21(0x13)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000)

	cases := []struct {
		name string
		tx   *core.Transaction
		want string
	}{
		{"sub-1TRX", freezeTx(t, owner, 999_999, 3, core.ResourceCode_ENERGY), "1 TRX"},
		{"over balance", freezeTx(t, owner, 11_000_000, 3, core.ResourceCode_ENERGY), "accountBalance"},
		{"bad duration", freezeTx(t, owner, 1_000_000, 2, core.ResourceCode_ENERGY), "frozenDuration"},
		{"missing owner", freezeTx(t, addr21(0x99), 1_000_000, 3, core.ResourceCode_ENERGY), "missing"},
		{"tron power", freezeTx(t, owner, 1_000_000, 3, core.ResourceCode_TRON_POWER), "ResourceCode"},
	}
	for _, c := range cases {
		if _, err := Apply(st, c.tx, BlockContext{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}

	// Stake2.0 open (UNFREEZE_DELAY_DAYS > 0) closes V1 freezing.
	if err := st.Properties.PutInt64([]byte("UNFREEZE_DELAY_DAYS"), 14); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(st, freezeTx(t, owner, 1_000_000, 3, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "freeze v2") {
		t.Fatalf("v2-open: err = %v, want 'freeze v2 is open'", err)
	}
}

func TestUnfreezeBandwidthLifecycle(t *testing.T) {
	owner := addr21(0x14)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, owner, 10_000_000, t0)
	mustApply(t, st, freezeTx(t, owner, 5_000_000, 3, core.ResourceCode_BANDWIDTH))

	// Not expired yet: rejected.
	if _, err := Apply(st, unfreezeTx(t, owner, core.ResourceCode_BANDWIDTH), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "not time") {
		t.Fatalf("early unfreeze: err = %v, want 'not time'", err)
	}

	// Advance "now" past expiry (a later block updated the property) and give the account a
	// vote to observe the V1 vote-clearing.
	if err := st.Properties.SaveLatestBlockHeaderTimestamp(t0 + 3*dayMs); err != nil {
		t.Fatal(err)
	}
	acct, _ := st.Accounts.Get(owner)
	acct.Votes = []*core.Vote{{VoteAddress: addr21(0x77), VoteCount: 1}}
	if err := st.Accounts.Put(acct); err != nil {
		t.Fatal(err)
	}

	mustApply(t, st, unfreezeTx(t, owner, core.ResourceCode_BANDWIDTH))
	acct, _ = st.Accounts.Get(owner)
	if acct.GetBalance() != 10_000_000 || len(acct.GetFrozen()) != 0 {
		t.Fatalf("after unfreeze: balance %d frozen %v", acct.GetBalance(), acct.GetFrozen())
	}
	if len(acct.GetVotes()) != 0 {
		t.Fatal("V1 unfreeze must clear votes")
	}
	if w, _ := st.Properties.TotalNetWeight(); w != 0 {
		t.Fatalf("TOTAL_NET_WEIGHT = %d, want 0", w)
	}
}

func TestUnfreezeEnergyLifecycle(t *testing.T) {
	owner := addr21(0x15)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, owner, 10_000_000, t0)
	mustApply(t, st, freezeTx(t, owner, 4_000_000, 3, core.ResourceCode_ENERGY))

	if _, err := Apply(st, unfreezeTx(t, owner, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "not time") {
		t.Fatalf("early unfreeze: err = %v", err)
	}

	if err := st.Properties.SaveLatestBlockHeaderTimestamp(t0 + 3*dayMs); err != nil {
		t.Fatal(err)
	}
	mustApply(t, st, unfreezeTx(t, owner, core.ResourceCode_ENERGY))

	acct, _ := st.Accounts.Get(owner)
	if acct.GetBalance() != 10_000_000 {
		t.Fatalf("balance = %d, want 10_000_000", acct.GetBalance())
	}
	if acct.GetAccountResource().GetFrozenBalanceForEnergy() != nil {
		t.Fatal("energy frozen entry must be cleared")
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 0 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 0", w)
	}
}

// TestFreezeEnergyPowersVM is the end-to-end "power on": before any freeze a contract call
// burns TRX for its whole energy bill; after FreezeBalanceContract(ENERGY) the SAME call is
// paid from the caller's staked energy (EnergyUsage > 0, EnergyFee == 0) — the M3.5d
// derivation running on real, actuator-grown state — and the usage write-back depletes the
// stake so a further call still covers from stake but sees the recorded usage.
func TestFreezeEnergyPowersVM(t *testing.T) {
	owner := addr21(0x16)
	st, d := newChainState(t, owner, 2_000_000_000, 1_600_000_000_000)

	// Deploy a contract that SSTOREs on trigger (runtime: PUSH1 42; PUSH1 0; SSTORE; STOP).
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	res := applyInSession(t, st, d, createTx(t, owner, deployer(runtime)), 1)
	contractAddr := res.Receipts[0].ContractAddress

	// Pre-freeze: no stake -> the whole bill burns as TRX.
	res = applyInSession(t, st, d, triggerTx(t, owner, contractAddr, nil), 2)
	pre := res.Receipts[0].Energy
	if pre.EnergyUsage != 0 || pre.EnergyFee <= 0 {
		t.Fatalf("pre-freeze receipt = %+v, want all burned", pre)
	}

	// Freeze 1000 TRX for energy.
	d.BuildSession()
	if _, err := Apply(st, freezeTx(t, owner, 1_000_000_000, 3, core.ResourceCode_ENERGY),
		BlockContext{Number: 3, Timestamp: 9000, Version: 35}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 1000 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 1000", w)
	}

	// Post-freeze: the same trigger is covered by staked energy — no TRX burned.
	res = applyInSession(t, st, d, triggerTx(t, owner, contractAddr, nil), 4)
	post := res.Receipts[0].Energy
	if post.EnergyUsage <= 0 || post.EnergyFee != 0 {
		t.Fatalf("post-freeze receipt = %+v, want stake-covered", post)
	}

	// The write-back recorded the consumption: usage stored, consume slot stamped.
	acct, _ := st.Accounts.Get(owner)
	ar := acct.GetAccountResource()
	if ar.GetEnergyUsage() < post.EnergyUsage {
		t.Fatalf("stored energy_usage = %d, want >= %d", ar.GetEnergyUsage(), post.EnergyUsage)
	}
	if ar.GetLatestConsumeTimeForEnergy() == 0 {
		t.Fatal("latest_consume_time_for_energy must be stamped")
	}

	// A second call still covers from stake and accumulates usage.
	res = applyInSession(t, st, d, triggerTx(t, owner, contractAddr, nil), 5)
	second := res.Receipts[0].Energy
	if second.EnergyFee != 0 {
		t.Fatalf("second call receipt = %+v, want stake-covered", second)
	}
	acct, _ = st.Accounts.Get(owner)
	if got := acct.GetAccountResource().GetEnergyUsage(); got < ar.GetEnergyUsage() {
		t.Fatalf("energy_usage after second call = %d, want >= %d", got, ar.GetEnergyUsage())
	}
}

// TestFreezeErrorsPreserveState pins that a rejected freeze leaves no partial effects.
func TestFreezeErrorsPreserveState(t *testing.T) {
	owner := addr21(0x17)
	st, _ := newChainState(t, owner, 10_000_000, 1_600_000_000_000)
	if _, err := Apply(st, freezeTx(t, owner, 999_999, 3, core.ResourceCode_ENERGY), BlockContext{}); err == nil {
		t.Fatal("want validation error")
	}
	acct, _ := st.Accounts.Get(owner)
	if acct.GetBalance() != 10_000_000 || energyFrozen(acct) != 0 {
		t.Fatalf("state mutated on rejected freeze: %+v", acct)
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 0 {
		t.Fatalf("weight mutated on rejected freeze: %d", w)
	}
	var errCheck error = errDelegateResourceDeferred
	if !errors.Is(errCheck, errDelegateResourceDeferred) {
		t.Fatal("sentinel identity")
	}
}
