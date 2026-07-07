package state

import (
	"encoding/binary"
	"errors"
	"math/big"

	"github.com/Redchar1992/go-tron/internal/db"
)

// DelegationStore is the DPoS reward accounting store — java-tron's DelegationStore. It holds
// per-cycle, per-witness figures used by the "new reward algorithm":
//   - reward[cycle][witness]  : the voters' reward pool a witness accrued that cycle (sun)
//   - vi[cycle][witness]      : the cumulative reward-per-vote index (BigInteger, 1e18-scaled)
//   - vote[cycle][witness]    : the witness's vote count snapshotted for the cycle
//   - accountVote[cycle][acct]: the account's votes snapshotted at a withdraw boundary
//   - beginCycle/endCycle[acct]: the account's withdraw-progress cursors
//   - brokerage[witness]      : the witness's cut of block rewards, percent (default 20)
//
// The Vi accumulator (accumulateWitnessVi) advances vi by reward*1e18/voteCount each cycle;
// an account's reward over [begin,end) is Σ (vi[end-1] - vi[begin-1]) * userVote / 1e18. All
// of this is gated by allowChangeDelegation (off from-genesis), so it is dormant by default.
type DelegationStore struct{ db *db.Database }

// DecimalOfViReward is DelegationStore.DECIMAL_OF_VI_REWARD = 10^18: the fixed-point scale the
// per-vote reward index carries.
var DecimalOfViReward = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// DefaultBrokerage is DelegationStore.DEFAULT_BROKERAGE (percent).
const DefaultBrokerage = 20

var (
	dgRewardPrefix      = []byte("dg/r/")
	dgViPrefix          = []byte("dg/v/")
	dgVotePrefix        = []byte("dg/o/")
	dgAccountVotePrefix = []byte("dg/a/")
	dgBeginCyclePrefix  = []byte("dg/bc/")
	dgEndCyclePrefix    = []byte("dg/ec/")
	dgBrokeragePrefix   = []byte("dg/bk/")
)

// cycleKey builds prefix||cycle(8 BE)||addr.
func cycleKey(prefix []byte, cycle int64, addr []byte) []byte {
	k := make([]byte, 0, len(prefix)+8+len(addr))
	k = append(k, prefix...)
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], uint64(cycle))
	k = append(k, c[:]...)
	return append(k, addr...)
}

func (s *DelegationStore) getInt64(key []byte, def int64) (int64, error) {
	b, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return def, nil
		}
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

func (s *DelegationStore) putInt64(key []byte, v int64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return s.db.Put(key, b[:])
}

// GetReward / AddReward: the voters' reward pool a witness accrued in a cycle.
func (s *DelegationStore) GetReward(cycle int64, addr []byte) (int64, error) {
	return s.getInt64(cycleKey(dgRewardPrefix, cycle, addr), 0)
}

func (s *DelegationStore) AddReward(cycle int64, addr []byte, v int64) error {
	cur, err := s.GetReward(cycle, addr)
	if err != nil {
		return err
	}
	return s.putInt64(cycleKey(dgRewardPrefix, cycle, addr), cur+v)
}

// GetWitnessVote / SetWitnessVote: the witness's vote count snapshot for a cycle.
func (s *DelegationStore) GetWitnessVote(cycle int64, addr []byte) (int64, error) {
	return s.getInt64(cycleKey(dgVotePrefix, cycle, addr), RemarkNoVote)
}

// RemarkNoVote mirrors java-tron DelegationStore.REMARK sentinel (-1): "unset", distinct from a
// real zero. getWitnessVote and getEndCycle both fall back to it.
const RemarkNoVote int64 = -1

func (s *DelegationStore) SetWitnessVote(cycle int64, addr []byte, v int64) error {
	return s.putInt64(cycleKey(dgVotePrefix, cycle, addr), v)
}

// GetWitnessVi / SetWitnessVi: the cumulative reward-per-vote index (1e18-scaled).
func (s *DelegationStore) GetWitnessVi(cycle int64, addr []byte) (*big.Int, error) {
	b, err := s.db.Get(cycleKey(dgViPrefix, cycle, addr))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return big.NewInt(0), nil
		}
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func (s *DelegationStore) SetWitnessVi(cycle int64, addr []byte, v *big.Int) error {
	return s.db.Put(cycleKey(dgViPrefix, cycle, addr), v.Bytes())
}

// AccumulateWitnessVi advances the witness's Vi for a cycle: preVi + reward*1e18/voteCount
// (forwarding preVi when there is no reward/vote), matching DelegationStore.accumulateWitnessVi.
func (s *DelegationStore) AccumulateWitnessVi(cycle int64, addr []byte, voteCount int64) error {
	preVi, err := s.GetWitnessVi(cycle-1, addr)
	if err != nil {
		return err
	}
	reward, err := s.GetReward(cycle, addr)
	if err != nil {
		return err
	}
	if reward == 0 || voteCount == 0 {
		if preVi.Sign() != 0 {
			return s.SetWitnessVi(cycle, addr, preVi)
		}
		return nil
	}
	deltaVi := new(big.Int).Mul(big.NewInt(reward), DecimalOfViReward)
	deltaVi.Div(deltaVi, big.NewInt(voteCount))
	return s.SetWitnessVi(cycle, addr, new(big.Int).Add(preVi, deltaVi))
}

// GetBrokerageAt returns the witness's brokerage percent for a cycle (default 20), matching
// DelegationStore.getBrokerage(cycle,address).
func (s *DelegationStore) GetBrokerageAt(cycle int64, addr []byte) (int, error) {
	v, err := s.getInt64(cycleKey(dgBrokeragePrefix, cycle, addr), DefaultBrokerage)
	return int(v), err
}

// SetBrokerageAt sets the witness's brokerage percent for a cycle.
func (s *DelegationStore) SetBrokerageAt(cycle int64, addr []byte, pct int) error {
	return s.putInt64(cycleKey(dgBrokeragePrefix, cycle, addr), int64(pct))
}

// GetBrokerage / SetBrokerage read/write the witness's "current" brokerage — java-tron keeps it
// at the sentinel cycle -1 (getBrokerage(address) == getBrokerage(-1,address)); doMaintenance
// copies it forward into each new cycle's per-cycle slot.
func (s *DelegationStore) GetBrokerage(addr []byte) (int, error) {
	return s.GetBrokerageAt(-1, addr)
}

func (s *DelegationStore) SetBrokerage(addr []byte, pct int) error {
	return s.SetBrokerageAt(-1, addr, pct)
}

// Begin/End cycle cursors for an account's withdraw progress.
func (s *DelegationStore) GetBeginCycle(addr []byte) (int64, error) {
	return s.getInt64(append(append([]byte(nil), dgBeginCyclePrefix...), addr...), 0)
}

func (s *DelegationStore) SetBeginCycle(addr []byte, c int64) error {
	return s.putInt64(append(append([]byte(nil), dgBeginCyclePrefix...), addr...), c)
}

// GetEndCycle defaults to REMARK (-1) when unset, matching DelegationStore.getEndCycle — a
// fresh account has no end cursor, distinct from cycle 0.
func (s *DelegationStore) GetEndCycle(addr []byte) (int64, error) {
	return s.getInt64(append(append([]byte(nil), dgEndCyclePrefix...), addr...), RemarkNoVote)
}

func (s *DelegationStore) SetEndCycle(addr []byte, c int64) error {
	return s.putInt64(append(append([]byte(nil), dgEndCyclePrefix...), addr...), c)
}

// AccountVote snapshots the votes an account held at a withdraw boundary (java-tron stores the
// whole account; only the votes list is read by computeReward, so a compact list suffices).
type AccountVote struct {
	Addresses [][]byte
	Counts    []int64
}

// GetAccountVote returns the snapshot, or nil when none.
func (s *DelegationStore) GetAccountVote(cycle int64, addr []byte) (*AccountVote, error) {
	b, err := s.db.Get(cycleKey(dgAccountVotePrefix, cycle, addr))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return decodeAccountVote(b)
}

// SetAccountVote stores a votes snapshot for the cycle.
func (s *DelegationStore) SetAccountVote(cycle int64, addr []byte, av *AccountVote) error {
	return s.db.Put(cycleKey(dgAccountVotePrefix, cycle, addr), encodeAccountVote(av))
}

// encode/decodeAccountVote is a tiny length-prefixed encoding: count, then per-entry
// (21-byte address, 8-byte BE count).
func encodeAccountVote(av *AccountVote) []byte {
	out := make([]byte, 0, 8+len(av.Addresses)*29)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(av.Addresses)))
	out = append(out, n[:]...)
	for i, a := range av.Addresses {
		out = append(out, a...)
		var c [8]byte
		binary.BigEndian.PutUint64(c[:], uint64(av.Counts[i]))
		out = append(out, c[:]...)
	}
	return out
}

func decodeAccountVote(b []byte) (*AccountVote, error) {
	if len(b) < 8 {
		return nil, errors.New("delegation: short account-vote record")
	}
	n := int(binary.BigEndian.Uint64(b[:8]))
	av := &AccountVote{Addresses: make([][]byte, 0, n), Counts: make([]int64, 0, n)}
	off := 8
	for i := 0; i < n; i++ {
		if off+29 > len(b) {
			return nil, errors.New("delegation: truncated account-vote record")
		}
		av.Addresses = append(av.Addresses, append([]byte(nil), b[off:off+21]...))
		av.Counts = append(av.Counts, int64(binary.BigEndian.Uint64(b[off+21:off+29])))
		off += 29
	}
	return av, nil
}
