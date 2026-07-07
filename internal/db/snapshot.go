package db

import (
	"bytes"
	"sort"
	"strings"
	"sync"
)

// Database layers a stack of in-memory revoking sessions over a base KV, mirroring
// java-tron's SnapshotManager / RevokingDatabase.
//
//   - BuildSession pushes a new overlay onto the stack.
//   - Put/Delete write into the top overlay (or directly to base if no session is open).
//   - Get/Has read top-down through the overlays, then fall through to base.
//   - Commit merges the top overlay into its parent (or flushes to base if it is the
//     only session), then pops it.
//   - Revoke discards the top overlay (rollback), then pops it.
//
// This is the rollback primitive that block/transaction execution and fork switching
// build on: open a session before applying a tx/block, Commit on success, Revoke on
// failure; switchFork pops sessions back to the fork point.
//
// Database is goroutine-safe at the API boundary. Like java-tron's revoking DB it is
// intended to be driven by the single chain-processing goroutine; the mutex guards
// against accidental concurrent access rather than enabling parallel mutation.
type Database struct {
	mu       sync.RWMutex
	base     KV
	sessions []*session
}

type entry struct {
	value   []byte
	deleted bool
}

type session struct {
	writes map[string]entry
}

func newSession() *session { return &session{writes: make(map[string]entry)} }

// NewDatabase wraps a committed base KV with the revoking-session machinery.
func NewDatabase(base KV) *Database {
	return &Database{base: base}
}

// Depth reports the number of open (uncommitted) sessions.
func (d *Database) Depth() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sessions)
}

// BuildSession pushes a new revoking session and returns the resulting depth.
func (d *Database) BuildSession() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions = append(d.sessions, newSession())
	return len(d.sessions)
}

// Get reads top-down through open sessions, then the base store.
func (d *Database) Get(key []byte) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	k := string(key)
	for i := len(d.sessions) - 1; i >= 0; i-- {
		if e, ok := d.sessions[i].writes[k]; ok {
			if e.deleted {
				return nil, ErrNotFound
			}
			return append([]byte(nil), e.value...), nil
		}
	}
	return d.base.Get(key)
}

// Has reports whether key is visible (respecting overlay tombstones).
func (d *Database) Has(key []byte) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	k := string(key)
	for i := len(d.sessions) - 1; i >= 0; i-- {
		if e, ok := d.sessions[i].writes[k]; ok {
			return !e.deleted, nil
		}
	}
	return d.base.Has(key)
}

// Scan returns every visible entry whose key starts with prefix, sorted by key. It merges
// the base store with the open sessions bottom-up (so an inner session's write or tombstone
// overrides the base / an outer session), giving the same top-down visibility as Get.
func (d *Database) Scan(prefix []byte) ([]KVPair, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	merged := map[string][]byte{}
	base, err := d.base.Scan(prefix)
	if err != nil {
		return nil, err
	}
	for _, p := range base {
		merged[string(p.Key)] = p.Value
	}
	p := string(prefix)
	for i := 0; i < len(d.sessions); i++ {
		for k, e := range d.sessions[i].writes {
			if !strings.HasPrefix(k, p) {
				continue
			}
			if e.deleted {
				delete(merged, k)
			} else {
				merged[k] = e.value
			}
		}
	}
	out := make([]KVPair, 0, len(merged))
	for k, v := range merged {
		out = append(out, KVPair{Key: []byte(k), Value: append([]byte(nil), v...)})
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Key, out[j].Key) < 0 })
	return out, nil
}

// Put writes into the top session, or to base if no session is open.
func (d *Database) Put(key, value []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if top := d.top(); top != nil {
		top.writes[string(key)] = entry{value: append([]byte(nil), value...)}
		return nil
	}
	return d.base.Put(key, value)
}

// Delete tombstones the key in the top session, or deletes from base if none is open.
func (d *Database) Delete(key []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if top := d.top(); top != nil {
		top.writes[string(key)] = entry{deleted: true}
		return nil
	}
	return d.base.Delete(key)
}

// Commit merges the top session into its parent, or flushes to base if it is the only
// open session. Returns false if there is no open session.
func (d *Database) Commit() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.sessions)
	if n == 0 {
		return false, nil
	}
	top := d.sessions[n-1]
	d.sessions = d.sessions[:n-1]
	if parent := d.top(); parent != nil {
		for k, e := range top.writes {
			parent.writes[k] = e
		}
		return true, nil
	}
	// flush to base
	for k, e := range top.writes {
		if e.deleted {
			if err := d.base.Delete([]byte(k)); err != nil {
				return true, err
			}
			continue
		}
		if err := d.base.Put([]byte(k), e.value); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Revoke discards the top session (rollback). Returns false if none is open.
func (d *Database) Revoke() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.sessions)
	if n == 0 {
		return false
	}
	d.sessions = d.sessions[:n-1]
	return true
}

// top returns the innermost session, or nil. Caller holds the lock.
func (d *Database) top() *session {
	if n := len(d.sessions); n > 0 {
		return d.sessions[n-1]
	}
	return nil
}
