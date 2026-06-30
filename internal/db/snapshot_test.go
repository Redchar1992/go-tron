package db

import (
	"errors"
	"testing"
)

func mustGet(t *testing.T, d *Database, key string) string {
	t.Helper()
	v, err := d.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q) error: %v", key, err)
	}
	return string(v)
}

func assertMissing(t *testing.T, d *Database, key string) {
	t.Helper()
	if _, err := d.Get([]byte(key)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q): want ErrNotFound, got %v", key, err)
	}
}

func TestNoSessionWritesGoToBase(t *testing.T) {
	d := NewDatabase(NewMemKV())
	if err := d.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, d, "a"); got != "1" {
		t.Fatalf("a = %q, want 1", got)
	}
	if d.Depth() != 0 {
		t.Fatalf("depth = %d, want 0", d.Depth())
	}
}

func TestRevokeDiscardsSessionWrites(t *testing.T) {
	base := NewMemKV()
	d := NewDatabase(base)
	_ = d.Put([]byte("a"), []byte("1")) // committed to base

	d.BuildSession()
	_ = d.Put([]byte("a"), []byte("2")) // overlay
	_ = d.Put([]byte("b"), []byte("9"))
	if got := mustGet(t, d, "a"); got != "2" {
		t.Fatalf("in-session a = %q, want 2", got)
	}
	if !d.Revoke() {
		t.Fatal("Revoke returned false")
	}
	if got := mustGet(t, d, "a"); got != "1" {
		t.Fatalf("after revoke a = %q, want 1 (base)", got)
	}
	assertMissing(t, d, "b")
}

func TestCommitFlushesToBase(t *testing.T) {
	base := NewMemKV()
	d := NewDatabase(base)
	d.BuildSession()
	_ = d.Put([]byte("a"), []byte("1"))
	_ = d.Delete([]byte("ghost")) // tombstone of absent key — harmless
	ok, err := d.Commit()
	if !ok || err != nil {
		t.Fatalf("Commit = (%v,%v)", ok, err)
	}
	if d.Depth() != 0 {
		t.Fatalf("depth = %d, want 0", d.Depth())
	}
	if v, err := base.Get([]byte("a")); err != nil || string(v) != "1" {
		t.Fatalf("base a = (%q,%v), want (1,nil)", v, err)
	}
}

func TestDeleteTombstoneHidesBaseValue(t *testing.T) {
	base := NewMemKV()
	_ = base.Put([]byte("a"), []byte("1"))
	d := NewDatabase(base)
	d.BuildSession()
	_ = d.Delete([]byte("a"))
	assertMissing(t, d, "a") // tombstone hides base value
	if has, _ := d.Has([]byte("a")); has {
		t.Fatal("Has(a) = true through tombstone, want false")
	}
	d.Revoke()
	if got := mustGet(t, d, "a"); got != "1" {
		t.Fatalf("after revoke a = %q, want 1", got)
	}
}

func TestNestedSessionsCommitMergesIntoParent(t *testing.T) {
	base := NewMemKV()
	d := NewDatabase(base)

	d.BuildSession() // outer
	_ = d.Put([]byte("a"), []byte("outer"))

	d.BuildSession() // inner
	_ = d.Put([]byte("a"), []byte("inner"))
	_ = d.Put([]byte("b"), []byte("innerB"))
	if got := mustGet(t, d, "a"); got != "inner" {
		t.Fatalf("a = %q, want inner", got)
	}

	if ok, _ := d.Commit(); !ok { // merge inner -> outer
		t.Fatal("inner commit false")
	}
	if d.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", d.Depth())
	}
	// base untouched while outer still open
	if base.Len() != 0 {
		t.Fatalf("base len = %d, want 0 (outer not committed)", base.Len())
	}
	if got := mustGet(t, d, "a"); got != "inner" {
		t.Fatalf("a after inner-commit = %q, want inner (merged into outer)", got)
	}

	if ok, _ := d.Commit(); !ok { // flush outer -> base
		t.Fatal("outer commit false")
	}
	if got, _ := base.Get([]byte("a")); string(got) != "inner" {
		t.Fatalf("base a = %q, want inner", got)
	}
	if got, _ := base.Get([]byte("b")); string(got) != "innerB" {
		t.Fatalf("base b = %q, want innerB", got)
	}
}

func TestNestedRevokeInnerKeepsOuter(t *testing.T) {
	d := NewDatabase(NewMemKV())
	d.BuildSession()
	_ = d.Put([]byte("a"), []byte("outer"))
	d.BuildSession()
	_ = d.Put([]byte("a"), []byte("inner"))
	d.Revoke() // drop inner only
	if got := mustGet(t, d, "a"); got != "outer" {
		t.Fatalf("a = %q, want outer (inner revoked, outer kept)", got)
	}
	if d.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", d.Depth())
	}
}

func TestCommitRevokeOnEmptyStackReportFalse(t *testing.T) {
	d := NewDatabase(NewMemKV())
	if ok, _ := d.Commit(); ok {
		t.Fatal("Commit on empty stack returned true")
	}
	if d.Revoke() {
		t.Fatal("Revoke on empty stack returned true")
	}
}
