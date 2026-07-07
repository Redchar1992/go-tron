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

// Optimized (ALLOW_DELEGATE_OPTIMIZATION) index layout: one KV per delegation edge instead
// of a list per account, keyed prefix||a||b with the value carrying the other endpoint +
// an ordering timestamp. Mirrors DelegatedResourceAccountIndexStore.delegate/unDelegate/
// convert. Prefix bytes match java-tron (0x01/0x02); V2 edges (0x03/0x04) arrive with the
// Stake2.0 delegation write-side. Query (prefix scan) is deferred with the RPC layer / db
// iteration — this is the write side, which is all the actuators need.
const (
	idxFromPrefix   = 0x01
	idxToPrefix     = 0x02
	idxV2FromPrefix = 0x03
	idxV2ToPrefix   = 0x04
)

func edgeKey(prefix byte, a, b []byte) []byte {
	k := make([]byte, 0, 1+len(a)+len(b))
	k = append(k, prefix)
	k = append(k, a...)
	return append(k, b...)
}

func (s *DelegatedResourceIndexStore) putEdge(prefix byte, a, b, other []byte, time int64) error {
	c := &core.DelegatedResourceAccountIndex{Account: other, Timestamp: time}
	v, err := marshal.Marshal(c)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(delegatedIndexPrefix, edgeKey(prefix, a, b)), v)
}

func (s *DelegatedResourceIndexStore) deleteEdge(prefix byte, a, b []byte) error {
	return s.db.Delete(nsKey(delegatedIndexPrefix, edgeKey(prefix, a, b)))
}

// Delegate writes the optimized from->to edges (from-side keyed by 'to' carrying 'to',
// to-side keyed by 'from' carrying 'from'), both stamped with time.
func (s *DelegatedResourceIndexStore) Delegate(from, to []byte, time int64) error {
	if err := s.putEdge(idxFromPrefix, from, to, to, time); err != nil {
		return err
	}
	return s.putEdge(idxToPrefix, to, from, from, time)
}

// UnDelegate removes the optimized from->to edges.
func (s *DelegatedResourceIndexStore) UnDelegate(from, to []byte) error {
	if err := s.deleteEdge(idxFromPrefix, from, to); err != nil {
		return err
	}
	return s.deleteEdge(idxToPrefix, to, from)
}

// HasEdge reports whether the optimized index edge prefix||a||b is present. Query/test
// support (the prefix-scan aggregation query is deferred with the RPC layer).
func (s *DelegatedResourceIndexStore) HasEdge(prefix byte, a, b []byte) bool {
	ok, _ := s.db.Has(nsKey(delegatedIndexPrefix, edgeKey(prefix, a, b)))
	return ok
}

// Convert migrates a legacy (list-form) index entry for addr to the optimized per-edge
// layout, then deletes it — a no-op if addr was already converted or never delegated. Uses
// each list index+1 as the ordering timestamp (DelegatedResourceAccountIndexStore.convert).
func (s *DelegatedResourceIndexStore) Convert(addr []byte) error {
	idx, err := s.Get(addr)
	if err != nil {
		return nil // already converted (no legacy entry) or never delegated
	}
	for i, to := range idx.GetToAccounts() {
		if err := s.Delegate(addr, to, int64(i)+1); err != nil {
			return err
		}
	}
	for i, from := range idx.GetFromAccounts() {
		if err := s.Delegate(from, addr, int64(i)+1); err != nil {
			return err
		}
	}
	return s.db.Delete(nsKey(delegatedIndexPrefix, addr))
}
