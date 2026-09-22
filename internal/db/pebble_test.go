package db

import (
	"errors"
	"testing"
)

func TestPebbleKVPersistsAndScans(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenPebble(dir)
	if err != nil {
		t.Fatalf("OpenPebble: %v", err)
	}
	if err := store.Put([]byte("a/1"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put([]byte("a/2"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put([]byte("b/1"), []byte("other")); err != nil {
		t.Fatal(err)
	}
	pairs, err := store.Scan([]byte("a/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || string(pairs[0].Key) != "a/1" || string(pairs[1].Value) != "two" {
		t.Fatalf("Scan(a/) = %+v", pairs)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenPebble(dir)
	if err != nil {
		t.Fatalf("reopen Pebble: %v", err)
	}
	defer store.Close()
	value, err := store.Get([]byte("a/1"))
	if err != nil || string(value) != "one" {
		t.Fatalf("persisted Get = %q, %v", value, err)
	}
	if err := store.Delete([]byte("a/1")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get([]byte("a/1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key error = %v, want ErrNotFound", err)
	}
}

func TestOpenEngineSelection(t *testing.T) {
	memory, err := Open("", "memory")
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	if _, err := Open(t.TempDir(), "unknown"); err == nil {
		t.Fatal("unknown engine unexpectedly opened")
	}
}
