// Package state holds the account/contract/resource/witness stores and the state trie,
// and derives the state root.
//
// CONSENSUS-CRITICAL: the account-state-trie encoding and root derivation MUST match
// java-tron exactly — this is what differential replay verifies. Target: M1. M0: placeholder.
package state
