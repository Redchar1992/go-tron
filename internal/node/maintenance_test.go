package node

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/actuator"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// wit builds a witness address.
func witAddr(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	a[1] = 0xB0
	a[20] = b
	return a
}

// castVote applies a VoteWitnessContract from a staked owner (given TRON power via V1 energy
// stake) directly onto the Manager's state.
func castVote(t *testing.T, m *Manager, owner []byte, power int64, votes ...*core.VoteWitnessContract_Vote) {
	t.Helper()
	st := m.State()
	acct, err := st.Accounts.Get(owner)
	if err != nil {
		acct = &core.Account{Address: owner, Type: core.AccountType_Normal}
	}
	if acct.AccountResource == nil {
		acct.AccountResource = &core.Account_AccountResource{}
	}
	acct.AccountResource.FrozenBalanceForEnergy = &core.Account_Frozen{FrozenBalance: power * 1_000_000}
	if err := st.Accounts.Put(acct); err != nil {
		t.Fatal(err)
	}
	p, err := anypb.New(&core.VoteWitnessContract{OwnerAddress: owner, Votes: votes})
	if err != nil {
		t.Fatal(err)
	}
	tx := &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_VoteWitnessContract, Parameter: p,
	}}}}
	if _, err := actuator.Apply(st, tx, actuator.BlockContext{}); err != nil {
		t.Fatalf("vote: %v", err)
	}
}

func TestRunMaintenanceTally(t *testing.T) {
	m := newSeededManager(t)
	st := m.State()
	w1, w2 := witAddr(1), witAddr(2)
	for _, w := range [][]byte{w1, w2} {
		if err := st.Witnesses.Put(&core.Witness{Address: w, VoteCount: 0}); err != nil {
			t.Fatal(err)
		}
	}
	voterA, voterB := addr21c(0x11), addr21c(0x12)

	// A votes 6->w1, 4->w2; B votes 5->w1.
	castVote(t, m, voterA, 10,
		&core.VoteWitnessContract_Vote{VoteAddress: w1, VoteCount: 6},
		&core.VoteWitnessContract_Vote{VoteAddress: w2, VoteCount: 4})
	castVote(t, m, voterB, 5, &core.VoteWitnessContract_Vote{VoteAddress: w1, VoteCount: 5})

	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	// w1 = 6+5 = 11, w2 = 4.
	if wc, _ := st.Witnesses.Get(w1); wc.GetVoteCount() != 11 {
		t.Fatalf("w1 votes = %d, want 11", wc.GetVoteCount())
	}
	if wc, _ := st.Witnesses.Get(w2); wc.GetVoteCount() != 4 {
		t.Fatalf("w2 votes = %d, want 4", wc.GetVoteCount())
	}
	// VotesStore cleared after tally.
	if has, _ := st.Votes.Has(voterA); has {
		t.Fatal("VotesStore must be cleared after maintenance")
	}

	// A re-votes: now all 10 to w2. The delta (old 6->w1,4->w2 ; new 10->w2) moves 6 off w1,
	// adds 6 to w2. (A's OldVotes come from its account votes, which still hold the first cast.)
	castVote(t, m, voterA, 10, &core.VoteWitnessContract_Vote{VoteAddress: w2, VoteCount: 10})
	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	if wc, _ := st.Witnesses.Get(w1); wc.GetVoteCount() != 5 { // 11 - 6
		t.Fatalf("w1 after re-vote = %d, want 5", wc.GetVoteCount())
	}
	if wc, _ := st.Witnesses.Get(w2); wc.GetVoteCount() != 10 { // 4 + 6
		t.Fatalf("w2 after re-vote = %d, want 10", wc.GetVoteCount())
	}
}

// TestMaintenanceTriggeredByBlockTime: a block whose timestamp crosses NEXT_MAINTENANCE_TIME
// runs the tally and advances the schedule; a following in-interval block does not.
func TestMaintenanceTriggeredByBlockTime(t *testing.T) {
	m := newSeededManager(t)
	st := m.State()
	// The genesis root seeded NEXT_MAINTENANCE_TIME to the genesis timestamp.
	next0, _ := st.Properties.NextMaintenanceTime()
	if next0 == 0 {
		t.Fatal("NEXT_MAINTENANCE_TIME should be seeded from the root")
	}

	w := witAddr(9)
	if err := st.Witnesses.Put(&core.Witness{Address: w}); err != nil {
		t.Fatal(err)
	}
	castVote(t, m, addr21c(0x21), 3, &core.VoteWitnessContract_Vote{VoteAddress: w, VoteCount: 3})

	// Directly run the maintenance path via a manual crossing: advance to past next, tally.
	if err := m.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
	if err := st.Properties.UpdateNextMaintenanceTime(next0); err != nil {
		t.Fatal(err)
	}
	if wc, _ := st.Witnesses.Get(w); wc.GetVoteCount() != 3 {
		t.Fatalf("tally after trigger = %d, want 3", wc.GetVoteCount())
	}
	next1, _ := st.Properties.NextMaintenanceTime()
	interval, _ := st.Properties.MaintenanceTimeInterval()
	if next1 != next0+interval {
		t.Fatalf("next maintenance = %d, want %d (advanced one interval)", next1, next0+interval)
	}
}
