package actuator

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

func voteTx(t *testing.T, owner []byte, votes ...*core.VoteWitnessContract_Vote) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.VoteWitnessContract{OwnerAddress: owner, Votes: votes})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_VoteWitnessContract, Parameter: p,
	}}}}
}

func vote(addr []byte, count int64) *core.VoteWitnessContract_Vote {
	return &core.VoteWitnessContract_Vote{VoteAddress: addr, VoteCount: count}
}

// stakedVoter builds a genesis-seeded state with an owner holding `trx` TRX of V1 energy
// stake (= TRON power) and the given witnesses registered.
func stakedVoter(t *testing.T, owner []byte, trx int64, witnesses ...[]byte) *state.State {
	t.Helper()
	st, _ := newChainState(t, owner, 1_000_000, 1_600_000_000_000)
	acct, _ := st.Accounts.Get(owner)
	if acct.AccountResource == nil {
		acct.AccountResource = &core.Account_AccountResource{}
	}
	acct.AccountResource.FrozenBalanceForEnergy = &core.Account_Frozen{FrozenBalance: trx * 1_000_000}
	if err := st.Accounts.Put(acct); err != nil {
		t.Fatal(err)
	}
	for _, w := range witnesses {
		if err := st.Witnesses.Put(&core.Witness{Address: w}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestVoteWitness(t *testing.T) {
	owner, w1, w2 := addr21(0x61), addr21(0xa1), addr21(0xa2)
	st := stakedVoter(t, owner, 10, w1, w2) // 10 TRX power

	mustApply(t, st, voteTx(t, owner, vote(w1, 6), vote(w2, 4))) // total 10 == power

	acct, _ := st.Accounts.Get(owner)
	if len(acct.GetVotes()) != 2 {
		t.Fatalf("account votes = %d, want 2", len(acct.GetVotes()))
	}
	v, err := st.Votes.Get(owner)
	if err != nil {
		t.Fatalf("VotesStore.Get: %v", err)
	}
	if len(v.GetNewVotes()) != 2 || v.GetNewVotes()[0].GetVoteCount() != 6 {
		t.Fatalf("VotesStore new votes = %+v", v.GetNewVotes())
	}

	// Re-voting replaces NewVotes and carries the prior account votes into OldVotes.
	mustApply(t, st, voteTx(t, owner, vote(w1, 3)))
	acct, _ = st.Accounts.Get(owner)
	if len(acct.GetVotes()) != 1 || acct.GetVotes()[0].GetVoteCount() != 3 {
		t.Fatalf("re-vote account votes = %+v", acct.GetVotes())
	}
}

func TestVoteWitnessValidation(t *testing.T) {
	owner, w1 := addr21(0x62), addr21(0xa3)
	st := stakedVoter(t, owner, 5, w1) // 5 TRX power

	cases := []struct {
		name string
		tx   *core.Transaction
		want string
	}{
		{"no votes", voteTx(t, owner), "must be more than 0"},
		{"unknown witness", voteTx(t, owner, vote(addr21(0xff), 1)), "does not exist"},
		{"zero count", voteTx(t, owner, vote(w1, 0)), "greater than 0"},
		{"over power", voteTx(t, owner, vote(w1, 6)), "exceeds the tronPower"}, // 6 > 5
	}
	for _, c := range cases {
		if _, err := Apply(st, c.tx, BlockContext{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

// TestTronPowerSources: every stake kind (V1/V2, self + delegated-out) contributes vote power.
func TestTronPowerSources(t *testing.T) {
	a := &core.Account{
		Frozen:                               []*core.Account_Frozen{{FrozenBalance: 1_000_000}},
		DelegatedFrozenBalanceForBandwidth:   2_000_000,
		DelegatedFrozenV2BalanceForBandwidth: 3_000_000,
		FrozenV2: []*core.Account_FreezeV2{
			{Type: core.ResourceCode_ENERGY, Amount: 4_000_000},
			{Type: core.ResourceCode_TRON_POWER, Amount: 9_000_000}, // excluded
		},
		AccountResource: &core.Account_AccountResource{
			FrozenBalanceForEnergy:            &core.Account_Frozen{FrozenBalance: 5_000_000},
			DelegatedFrozenBalanceForEnergy:   6_000_000,
			DelegatedFrozenV2BalanceForEnergy: 7_000_000,
		},
	}
	// 1+2+3+4+5+6+7 = 28M sun; the 9M TRON_POWER V2 entry is excluded.
	if got := tronPower(a); got != 28_000_000 {
		t.Fatalf("tronPower = %d, want 28_000_000 (TRON_POWER excluded)", got)
	}
}
