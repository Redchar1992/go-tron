package db

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
)

// PebbleKV is the durable committed KV engine used by a running node. Pebble owns its
// internal concurrency; the mutex here only serializes Close against operations so callers get
// a deterministic error instead of a use-after-close race during shutdown.
type PebbleKV struct {
	mu     sync.RWMutex
	store  *pebble.DB
	closed bool
}

var _ KV = (*PebbleKV)(nil)

// OpenPebble opens (or creates) a Pebble database at dir. Writes use pebble.Sync so a successful
// Put/Delete is durable before the revoking database reports a committed session.
func OpenPebble(dir string) (*PebbleKV, error) {
	if dir == "" {
		return nil, fmt.Errorf("db: pebble directory is empty")
	}
	store, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("db: open pebble %q: %w", dir, err)
	}
	return &PebbleKV{store: store}, nil
}

// Open selects the configured committed engine. "memory" is useful for diagnostics and tests;
// "pebble" is the production default. Other engine names fail closed until their exact
// java-tron-compatible adapters are implemented.
func Open(dir, engine string) (KV, error) {
	switch engine {
	case "", "pebble":
		return OpenPebble(dir)
	case "memory", "mem":
		return NewMemKV(), nil
	default:
		return nil, fmt.Errorf("db: unsupported storage engine %q", engine)
	}
}

func (d *PebbleKV) Get(key []byte) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, ErrClosed
	}
	value, closer, err := d.store.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), nil
}

func (d *PebbleKV) Has(key []byte) (bool, error) {
	_, err := d.Get(key)
	if err == nil {
		return true, nil
	}
	if err == ErrNotFound {
		return false, nil
	}
	return false, err
}

func (d *PebbleKV) Put(key, value []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return ErrClosed
	}
	return d.store.Set(key, value, pebble.Sync)
}

func (d *PebbleKV) Delete(key []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return ErrClosed
	}
	return d.store.Delete(key, pebble.Sync)
}

func (d *PebbleKV) Scan(prefix []byte) ([]KVPair, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, ErrClosed
	}
	iter, err := d.store.NewIter(nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var output []KVPair
	for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
		if !bytes.HasPrefix(iter.Key(), prefix) {
			break
		}
		output = append(output, KVPair{
			Key:   append([]byte(nil), iter.Key()...),
			Value: append([]byte(nil), iter.Value()...),
		})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return output, nil
}

func (d *PebbleKV) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.store.Close()
}
