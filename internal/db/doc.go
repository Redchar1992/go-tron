// Package db is the key-value storage abstraction plus the revocable snapshot layer
// (the go-tron analog of java-tron's SnapshotManager / RevokingDatabase): in-memory
// revoking sessions stacked over a committed KV engine, with checkpoint recovery/flush.
//
// Default engine: Pebble (pure Go, no cgo); LevelDB/RocksDB selectable for parity testing.
// CONSENSUS-CRITICAL: snapshot/rollback semantics drive fork switching. Target: M1.
// M0: placeholder.
package db
