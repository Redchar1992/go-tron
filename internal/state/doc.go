// Package state holds the chain's mutable stores (accounts, witnesses, …) layered over
// the revocable db.Database. Each store namespaces its keys with a short prefix in the
// shared KV (java-tron uses separate column families; the on-disk layout is not
// consensus — only the stored value bytes and the eventual optional state-trie are).
//
// Keys are the raw 21-byte 0x41-prefixed address; values are protobuf-serialized
// (core.Account / core.Witness). Milestone: M1.
package state
