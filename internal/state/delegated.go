package state

import (
	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// V1 resource-delegation stores, the go-tron analogs of java-tron's DelegatedResourceStore
// and DelegatedResourceAccountIndexStore. A DelegatedResource entry records what `from` has
// frozen FOR `to` (bandwidth/energy sides with their expire times), keyed by from||to —
// DelegatedResourceCapsule.createDbKey. The index store keeps, per account, who it delegates
// to and who delegates to it (the pre-"delegate optimization" layout; the optimized
// timestamp-paged layout is proposal-gated and deferred with proposal processing).

var (
	delegatedPrefix      = []byte("dr/")
	delegatedIndexPrefix = []byte("di/")
)

// DelegatedResourceKey is DelegatedResourceCapsule.createDbKey: from || to (42 bytes).
func DelegatedResourceKey(from, to []byte) []byte {
	k := make([]byte, 0, len(from)+len(to))
	k = append(k, from...)
	return append(k, to...)
}

// DelegatedResourceStore persists core.DelegatedResource keyed by from||to.
type DelegatedResourceStore struct{ db *db.Database }

// Get returns the delegation entry for (from,to), or db.ErrNotFound.
func (s *DelegatedResourceStore) Get(from, to []byte) (*core.DelegatedResource, error) {
	b, err := s.db.Get(nsKey(delegatedPrefix, DelegatedResourceKey(from, to)))
	if err != nil {
		return nil, err
	}
	d := new(core.DelegatedResource)
	if err := proto.Unmarshal(b, d); err != nil {
		return nil, err
	}
	return d, nil
}

// Put stores the entry under its own From||To.
func (s *DelegatedResourceStore) Put(d *core.DelegatedResource) error {
	b, err := marshal.Marshal(d)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(delegatedPrefix, DelegatedResourceKey(d.GetFrom(), d.GetTo())), b)
}

// Delete removes the (from,to) entry (no error if absent).
func (s *DelegatedResourceStore) Delete(from, to []byte) error {
	return s.db.Delete(nsKey(delegatedPrefix, DelegatedResourceKey(from, to)))
}

// DelegatedResourceIndexStore persists core.DelegatedResourceAccountIndex keyed by the
// 21-byte account address.
type DelegatedResourceIndexStore struct{ db *db.Database }

// Get returns the account's index, or db.ErrNotFound.
func (s *DelegatedResourceIndexStore) Get(addr []byte) (*core.DelegatedResourceAccountIndex, error) {
	b, err := s.db.Get(nsKey(delegatedIndexPrefix, addr))
	if err != nil {
		return nil, err
	}
	idx := new(core.DelegatedResourceAccountIndex)
	if err := proto.Unmarshal(b, idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// Put stores the index under its own Account address.
func (s *DelegatedResourceIndexStore) Put(idx *core.DelegatedResourceAccountIndex) error {
	b, err := marshal.Marshal(idx)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(delegatedIndexPrefix, idx.GetAccount()), b)
}
