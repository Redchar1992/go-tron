package state

import (
	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// VotesStore persists core.Votes keyed by the voter's 21-byte address — java-tron's
// VotesStore. A Votes entry carries an account's OLD votes (as tallied at the last
// maintenance) and NEW votes (the pending set from its latest VoteWitnessContract); the
// maintenance window applies (new - old) to each witness's vote count. go-tron maintains the
// store faithfully; the maintenance-window tally + reward distribution is a later slice.
type VotesStore struct{ db *db.Database }

var votesPrefix = []byte("vo/")

// Get returns the voter's Votes entry, or db.ErrNotFound.
func (s *VotesStore) Get(owner []byte) (*core.Votes, error) {
	b, err := s.db.Get(nsKey(votesPrefix, owner))
	if err != nil {
		return nil, err
	}
	v := new(core.Votes)
	if err := proto.Unmarshal(b, v); err != nil {
		return nil, err
	}
	return v, nil
}

// Put stores the Votes entry under its own Address.
func (s *VotesStore) Put(v *core.Votes) error {
	b, err := marshal.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(votesPrefix, v.GetAddress()), b)
}

// Has reports whether the voter has a Votes entry.
func (s *VotesStore) Has(owner []byte) (bool, error) {
	return s.db.Has(nsKey(votesPrefix, owner))
}
