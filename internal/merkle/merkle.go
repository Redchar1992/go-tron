// Package merkle computes TRON's transaction Merkle root.
//
// It is a binary tree where each internal node = SHA256(left || right). A trailing
// odd node at any level is promoted to the next level UNCHANGED (no Bitcoin-style
// duplication). A single-element input yields that element unchanged. This matches
// java-tron's chainbase MerkleTree (capsule/utils/MerkleTree.java). CONSENSUS-CRITICAL.
package merkle

import "github.com/Redchar1992/go-tron/internal/crypto"

// Root returns the Merkle root of the given leaf hashes. Returns nil for empty input
// (TRON blocks always carry at least one transaction, so callers should treat empty
// as a programming error).
func Root(leaves [][]byte) []byte {
	if len(leaves) == 0 {
		return nil
	}
	level := make([][]byte, len(leaves))
	copy(level, leaves)
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, crypto.Sha256(level[i], level[i+1]))
			} else {
				next = append(next, level[i]) // odd node promoted unchanged
			}
		}
		level = next
	}
	return level[0]
}
