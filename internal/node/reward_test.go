package node

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/actuator"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// enableRewards turns on proposal #34 (allowChangeDelegation) and activates the new reward
// algorithm from cycle 0, so the per-cycle Vi accrual + withdraw path is live.
func enableRewards(t *testing.T, m *Manager) {
	t.Helper()
	p := m.State().Properties
	if err := p.SaveChangeDelegation(1); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveNewRewardAlgorithmEffectiveCycle(0); err != nil {
		t.Fatal(err)
	}
}

// withdrawBalance applies a WithdrawBalanceContract for owner directly onto the Manager's state.
func withdrawBalance(t *testing.T, m *Manager, owner []byte) error {
	t.Helper()
	p, err := anypb.New(&core.WithdrawBalanceContract{OwnerAddress: owner})
	if err != nil {
		t.Fatal(err)
	}
	tx := &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_WithdrawBalanceContract, Parameter: p,
	}}}}
	_, err = actuator.Apply(m.State(), tx, actuator.BlockContext{})
	return err
}

// TestRewardEndToEnd powers the WITHDRAWREWARD path: a voter backs a witness, a produced block
// funds the cycle reward pool, two maintenance windows fold it into the witness's Vi index, and
// the voter withdraws its pro-rata share into spendable balance — with the witness collecting its
// brokerage cut.
func TestRewardEndToEnd(t *testing.T) {
	m := newSeededManager(t)
	st := m.State()
	enableRewards(t, m)

	w := witAddr(1)
	if err := st.Witnesses.Put(&core.Witness{Address: w, VoteCount: 0}); err != nil {
		t.Fatal(err)
	}
	voter := addr21c(0x31)

	// Cycle 0: voter stakes 10 TRX of power and casts all 10 votes to w.
	castVote(t, m, voter, 10, &core.VoteWitnessContract_Vote{VoteAddress: w, VoteCount: 10})

	// Maintenance 0->1: tally moves the 10 votes onto w; Vi[0] stays 0 (w had no counted votes
	// during cycle 0). w's vote count is snapshotted into cycle 1.
	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	if wc, _ := st.Witnesses.Get(w); wc.GetVoteCount() != 10 {
		t.Fatalf("witness votes after tally = %d, want 10", wc.GetVoteCount())
	}
	if cyc, _ := st.Properties.CurrentCycleNumber(); cyc != 1 {
		t.Fatalf("cycle after first maintenance = %d, want 1", cyc)
	}

	// A block produced by w during cycle 1 mints 32 TRX: 20% brokerage (6.4 TRX) to w's own
	// allowance, the remaining 25.6 TRX into cycle 1's voter reward pool.
	if err := actuator.PayBlockReward(st, w, 32_000_000); err != nil {
		t.Fatal(err)
	}
	if wa, _ := st.Accounts.Get(w); wa.GetAllowance() != 6_400_000 {
		t.Fatalf("witness brokerage allowance = %d, want 6_400_000", func() int64 {
			if wa != nil {
				return wa.GetAllowance()
			}
			return -1
		}())
	}

	// Maintenance 1->2: accumulate Vi[1] = 25.6e6 * 1e18 / 10 votes.
	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	if cyc, _ := st.Properties.CurrentCycleNumber(); cyc != 2 {
		t.Fatalf("cycle after second maintenance = %d, want 2", cyc)
	}

	// The voter withdraws: its whole cycle-1 share (sole voter) = 25.6 TRX, moved into balance.
	before, _ := st.Accounts.Get(voter)
	beforeBal := before.GetBalance()
	if err := withdrawBalance(t, m, voter); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	after, _ := st.Accounts.Get(voter)
	if got := after.GetBalance() - beforeBal; got != 25_600_000 {
		t.Fatalf("voter reward into balance = %d, want 25_600_000", got)
	}
	if after.GetAllowance() != 0 {
		t.Fatalf("voter allowance after withdraw = %d, want 0", after.GetAllowance())
	}
	now, _ := st.Properties.LatestBlockHeaderTimestamp()
	if after.GetLatestWithdrawTime() != now {
		t.Fatalf("latest withdraw time = %d, want now %d", after.GetLatestWithdrawTime(), now)
	}
}

// TestRewardDormantWithoutChangeDelegation verifies the whole subsystem is a no-op from genesis:
// with allowChangeDelegation off, a produced block funds no pool, cycles do not advance, and a
// withdraw with no prior allowance is rejected ("does not have any reward").
func TestRewardDormantWithoutChangeDelegation(t *testing.T) {
	m := newSeededManager(t)
	st := m.State()

	w := witAddr(2)
	if err := st.Witnesses.Put(&core.Witness{Address: w}); err != nil {
		t.Fatal(err)
	}
	voter := addr21c(0x32)
	castVote(t, m, voter, 5, &core.VoteWitnessContract_Vote{VoteAddress: w, VoteCount: 5})

	if err := actuator.PayBlockReward(st, w, 32_000_000); err != nil {
		t.Fatal(err)
	}
	// No pool credited, no brokerage — the witness account was never created.
	if has, _ := st.Accounts.Has(w); has {
		t.Fatal("witness account must not exist (no reward credited while dormant)")
	}
	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	if cyc, _ := st.Properties.CurrentCycleNumber(); cyc != 0 {
		t.Fatalf("cycle must stay 0 while dormant, got %d", cyc)
	}
	// A withdraw with nothing accrued is rejected.
	if err := withdrawBalance(t, m, voter); err == nil {
		t.Fatal("withdraw must fail when no reward is accrued")
	}
}
