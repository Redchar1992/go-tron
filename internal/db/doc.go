// Package db is the key-value storage abstraction plus the revocable snapshot layer
// (the go-tron analog of java-tron's SnapshotManager / RevokingDatabase): in-memory
// revoking sessions stacked over a committed KV engine.
//
// Files:
//   - kv.go        KV interface + MemKV (in-memory) implementation
//   - pebble.go    durable Pebble implementation + engine selector
//   - snapshot.go  Database: a stack of revoking sessions over a base KV
//
// Pebble is the durable default (pure Go, no cgo); MemKV remains available for tests and
// offline replay. CONSENSUS-CRITICAL: snapshot/rollback semantics drive fork switching, and
// the eventual state-root derivation sits on top of this. LevelDB/RocksDB adapters are not
// registered until their compatibility semantics are specified.
package db
