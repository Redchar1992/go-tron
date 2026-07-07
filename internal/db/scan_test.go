package db

import (
	"bytes"
	"testing"
)

func keys(pairs []KVPair) string {
	var b bytes.Buffer
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(p.Key)
	}
	return b.String()
}

func TestMemKVScan(t *testing.T) {
	m := NewMemKV()
	m.Put([]byte("a/1"), []byte("x"))
	m.Put([]byte("a/3"), []byte("z"))
	m.Put([]byte("a/2"), []byte("y"))
	m.Put([]byte("b/1"), []byte("w"))

	got, err := m.Scan([]byte("a/"))
	if err != nil {
		t.Fatal(err)
	}
	if s := keys(got); s != "a/1,a/2,a/3" { // sorted, prefix-filtered
		t.Fatalf("scan a/ = %q, want a/1,a/2,a/3", s)
	}
}

// TestDatabaseScanOverlays: a scan sees session writes and tombstones with the same top-down
// visibility as Get, and revoking a session restores the prior view.
func TestDatabaseScanOverlays(t *testing.T) {
	base := NewMemKV()
	base.Put([]byte("v/1"), []byte("a"))
	base.Put([]byte("v/2"), []byte("b"))
	d := NewDatabase(base)

	d.BuildSession()
	d.Put([]byte("v/3"), []byte("c")) // new in session
	d.Delete([]byte("v/1"))           // tombstone a base key
	d.Put([]byte("v/2"), []byte("B")) // override a base value

	got, _ := d.Scan([]byte("v/"))
	if s := keys(got); s != "v/2,v/3" {
		t.Fatalf("in-session scan = %q, want v/2,v/3 (v/1 tombstoned)", s)
	}
	for _, p := range got {
		if bytes.Equal(p.Key, []byte("v/2")) && string(p.Value) != "B" {
			t.Fatalf("v/2 value = %q, want overridden B", p.Value)
		}
	}

	d.Revoke()
	got, _ = d.Scan([]byte("v/"))
	if s := keys(got); s != "v/1,v/2" {
		t.Fatalf("after revoke scan = %q, want v/1,v/2", s)
	}
}
