package db

import (
	"errors"
	"sync"
)

// ErrNotFound is returned by Get when a key is absent.
var ErrNotFound = errors.New("db: key not found")

// KV is the committed key-value store interface. Real engines (Pebble/LevelDB/RocksDB)
// and the in-memory MemKV implement it. Returned byte slices must not be mutated by
// callers; implementations return copies where needed.
type KV interface {
	Get(key []byte) ([]byte, error) // ErrNotFound if absent
	Has(key []byte) (bool, error)
	Put(key, value []byte) error
	Delete(key []byte) error
	Close() error
}

// MemKV is a goroutine-safe in-memory KV, used for tests and as the M1 placeholder
// until the Pebble engine lands.
type MemKV struct {
	mu sync.RWMutex
	m  map[string][]byte
}

// NewMemKV returns an empty in-memory KV.
func NewMemKV() *MemKV {
	return &MemKV{m: make(map[string][]byte)}
}

// Get returns a copy of the stored value, or ErrNotFound.
func (d *MemKV) Get(key []byte) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v, ok := d.m[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

// Has reports whether key exists.
func (d *MemKV) Has(key []byte) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.m[string(key)]
	return ok, nil
}

// Put stores a copy of value under key.
func (d *MemKV) Put(key, value []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[string(key)] = append([]byte(nil), value...)
	return nil
}

// Delete removes key (no error if absent).
func (d *MemKV) Delete(key []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.m, string(key))
	return nil
}

// Close is a no-op for MemKV.
func (d *MemKV) Close() error { return nil }

// Len reports the number of committed keys (test helper).
func (d *MemKV) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.m)
}
