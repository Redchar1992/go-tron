package state

import (
	"math/big"
	"testing"

	"github.com/Redchar1992/go-tron/internal/db"
)

func newDelegation(t *testing.T) *DelegationStore {
	t.Helper()
	d := db.NewDatabase(db.NewMemKV())
	d.BuildSession()
	return &DelegationStore{d}
}

func dgAddr(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	a[20] = b
	return a
}

// TestDelegationRewardAndCursors covers the plain int64 accessors: reward accrual, the
// vote/brokerage/cursor defaults (REMARK, 20), and the round-trips.
func TestDelegationRewardAndCursors(t *testing.T) {
	s := newDelegation(t)
	w := dgAddr(1)

	// Reward accrues additively per cycle.
	if got, _ := s.GetReward(5, w); got != 0 {
		t.Fatalf("fresh reward = %d, want 0", got)
	}
	if err := s.AddReward(5, w, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.AddReward(5, w, 25); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetReward(5, w); got != 125 {
		t.Fatalf("reward = %d, want 125", got)
	}

	// Witness vote defaults to REMARK (-1), distinct from a real 0.
	if got, _ := s.GetWitnessVote(5, w); got != RemarkNoVote {
		t.Fatalf("fresh witness vote = %d, want REMARK %d", got, RemarkNoVote)
	}
	if err := s.SetWitnessVote(5, w, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetWitnessVote(5, w); got != 0 {
		t.Fatalf("witness vote = %d, want 0", got)
	}

	// Brokerage defaults to 20; the 1-arg form is the sentinel cycle -1.
	if got, _ := s.GetBrokerage(w); got != DefaultBrokerage {
		t.Fatalf("fresh brokerage = %d, want %d", got, DefaultBrokerage)
	}
	if err := s.SetBrokerage(w, 30); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBrokerageAt(-1, w); got != 30 {
		t.Fatalf("brokerage at -1 = %d, want 30 (1-arg == cycle -1)", got)
	}

	// Cursors: begin defaults 0, end defaults REMARK.
	if got, _ := s.GetBeginCycle(w); got != 0 {
		t.Fatalf("fresh begin cycle = %d, want 0", got)
	}
	if got, _ := s.GetEndCycle(w); got != RemarkNoVote {
		t.Fatalf("fresh end cycle = %d, want REMARK %d", got, RemarkNoVote)
	}
}

// TestAccumulateWitnessVi checks the per-vote index math: delta = reward*1e18/voteCount added to
// the prior cycle's Vi, with the reward==0 || voteCount==0 forward-only rule.
func TestAccumulateWitnessVi(t *testing.T) {
	s := newDelegation(t)
	w := dgAddr(2)

	// Cycle 0: no reward yet -> Vi stays absent (zero, not recorded).
	if err := s.AccumulateWitnessVi(0, w, 10); err != nil {
		t.Fatal(err)
	}
	if vi, _ := s.GetWitnessVi(0, w); vi.Sign() != 0 {
		t.Fatalf("Vi[0] = %s, want 0 (no reward)", vi)
	}

	// Cycle 1: reward 25_600_000 over 10 votes -> Vi[1] = 25_600_000 * 1e18 / 10.
	if err := s.AddReward(1, w, 25_600_000); err != nil {
		t.Fatal(err)
	}
	if err := s.AccumulateWitnessVi(1, w, 10); err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Mul(big.NewInt(25_600_000), DecimalOfViReward)
	want.Div(want, big.NewInt(10))
	if vi, _ := s.GetWitnessVi(1, w); vi.Cmp(want) != 0 {
		t.Fatalf("Vi[1] = %s, want %s", vi, want)
	}

	// Cycle 2: no reward -> forward Vi[1] unchanged into Vi[2].
	if err := s.AccumulateWitnessVi(2, w, 10); err != nil {
		t.Fatal(err)
	}
	if vi, _ := s.GetWitnessVi(2, w); vi.Cmp(want) != 0 {
		t.Fatalf("Vi[2] = %s, want forwarded %s", vi, want)
	}
}

// TestAccountVoteSnapshotRoundTrip checks the compact vote-snapshot encoding.
func TestAccountVoteSnapshotRoundTrip(t *testing.T) {
	s := newDelegation(t)
	acct := dgAddr(9)
	in := &AccountVote{
		Addresses: [][]byte{dgAddr(1), dgAddr(2)},
		Counts:    []int64{10, 7},
	}
	if err := s.SetAccountVote(3, acct, in); err != nil {
		t.Fatal(err)
	}
	if none, _ := s.GetAccountVote(4, acct); none != nil {
		t.Fatal("account vote at wrong cycle should be nil")
	}
	got, err := s.GetAccountVote(3, acct)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Addresses) != 2 || got.Counts[0] != 10 || got.Counts[1] != 7 {
		t.Fatalf("snapshot round-trip = %+v", got)
	}
}
