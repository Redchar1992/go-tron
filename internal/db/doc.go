// Package db is the key-value storage abstraction plus the revocable snapshot layer
// (the go-tron analog of java-tron's SnapshotManager / RevokingDatabase): in-memory
// revoking sessions stacked over a committed KV engine.
//
// Files:
//   - kv.go        KV interface + MemKV (in-memory) implementation
//   - snapshot.go  Database: a stack of revoking sessions over a base KV
//
// Default committed engine will be Pebble (pure Go, no cgo); LevelDB/RocksDB selectable
// for parity testing. CONSENSUS-CRITICAL: snapshot/rollback semantics drive fork
// switching, and the eventual state-root derivation sits on top of this. Milestone: M1.
package db
