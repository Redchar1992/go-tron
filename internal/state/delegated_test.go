package state

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func newTestState(t *testing.T) *State {
	t.Helper()
	return New(db.NewDatabase(db.NewMemKV()))
}

func addr21b(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	a[20] = b
	return a
}

func hasEdge(t *testing.T, st *State, prefix byte, a, b []byte) bool {
	t.Helper()
	_, err := st.DB.Get(nsKey(delegatedIndexPrefix, edgeKey(prefix, a, b)))
	return err == nil
}

func TestDelegatedResourceStore(t *testing.T) {
	st := newTestState(t)
	from, to := addr21b(0x01), addr21b(0x02)

	if _, err := st.Delegated.Get(from, to); err == nil {
		t.Fatal("absent delegation should error")
	}
	if err := st.Delegated.Put(&core.DelegatedResource{
		From: from, To: to, FrozenBalanceForEnergy: 5_000_000, ExpireTimeForEnergy: 999,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Delegated.Get(from, to)
	if err != nil || got.GetFrozenBalanceForEnergy() != 5_000_000 {
		t.Fatalf("get = %+v (err %v)", got, err)
	}
	if err := st.Delegated.Delete(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Delegated.Get(from, to); err == nil {
		t.Fatal("deleted delegation should error")
	}
}

// TestIndexOptimizedEdges: Delegate writes the two per-edge keys carrying the far endpoint +
// timestamp; UnDelegate removes them.
func TestIndexOptimizedEdges(t *testing.T) {
	st := newTestState(t)
	from, to := addr21b(0x01), addr21b(0x02)

	if err := st.DelegatedIndex.Delegate(from, to, 12345); err != nil {
		t.Fatal(err)
	}
	if !hasEdge(t, st, idxFromPrefix, from, to) || !hasEdge(t, st, idxToPrefix, to, from) {
		t.Fatal("both optimized edges must exist after Delegate")
	}
	// value on the from-edge carries 'to' + the timestamp.
	b, _ := st.DB.Get(nsKey(delegatedIndexPrefix, edgeKey(idxFromPrefix, from, to)))
	c := new(core.DelegatedResourceAccountIndex)
	if err := proto.Unmarshal(b, c); err != nil {
		t.Fatal(err)
	}
	if string(c.GetAccount()) != string(to) || c.GetTimestamp() != 12345 {
		t.Fatalf("from-edge value = %x/%d, want to/12345", c.GetAccount(), c.GetTimestamp())
	}

	if err := st.DelegatedIndex.UnDelegate(from, to); err != nil {
		t.Fatal(err)
	}
	if hasEdge(t, st, idxFromPrefix, from, to) || hasEdge(t, st, idxToPrefix, to, from) {
		t.Fatal("edges must be gone after UnDelegate")
	}
}

// TestIndexConvert migrates a legacy list-form entry to per-edge keys and deletes the legacy.
func TestIndexConvert(t *testing.T) {
	st := newTestState(t)
	a, b1, b2 := addr21b(0x0a), addr21b(0x0b), addr21b(0x0c)

	// legacy: a delegates to b1 and b2.
	if err := st.DelegatedIndex.Put(&core.DelegatedResourceAccountIndex{
		Account: a, ToAccounts: [][]byte{b1, b2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.DelegatedIndex.Convert(a); err != nil {
		t.Fatal(err)
	}
	// legacy key gone; per-edge keys present with index+1 timestamps.
	if _, err := st.DelegatedIndex.Get(a); err == nil {
		t.Fatal("legacy entry must be deleted after Convert")
	}
	if !hasEdge(t, st, idxFromPrefix, a, b1) || !hasEdge(t, st, idxFromPrefix, a, b2) {
		t.Fatal("converted from-edges missing")
	}
	if !hasEdge(t, st, idxToPrefix, b1, a) || !hasEdge(t, st, idxToPrefix, b2, a) {
		t.Fatal("converted to-edges missing")
	}
	// Convert on an already-converted / absent address is a no-op.
	if err := st.DelegatedIndex.Convert(a); err != nil {
		t.Fatalf("re-convert should be a no-op, got %v", err)
	}
}
