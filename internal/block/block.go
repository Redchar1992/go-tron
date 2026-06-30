// Package block holds the consensus-critical block primitives shared by genesis,
// the node Manager, and the differential replay harness: the per-transaction Merkle
// leaf hash, the transaction Merkle root (txTrieRoot), and the canonical block id.
//
// Verified against java-tron (chainbase capsule/BlockCapsule.java, capsule/utils/
// MerkleTree.java):
//   - tx merkle leaf  = SHA-256 of the FULL serialized Transaction (getMerkleHash).
//   - txTrieRoot      = SHA-256 binary Merkle over the leaves; an EMPTY block has
//     txTrieRoot = Sha256Hash.ZERO_HASH (32 zero bytes), NOT the empty/nil root.
//   - block id        = SHA-256 of the serialized BlockHeader.raw, with the first 8
//     bytes overwritten by the block number (big-endian) — generateBlockId.
//
// CONSENSUS-CRITICAL.
package block

import (
	"encoding/binary"

	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/crypto"
	"github.com/Redchar1992/go-tron/internal/merkle"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// HashLen is the byte length of a SHA-256 hash / TRON block id.
const HashLen = 32

// ZeroHash is java-tron's Sha256Hash.ZERO_HASH — the txTrieRoot of an empty block.
func ZeroHash() []byte { return make([]byte, HashLen) }

// TxMerkleHash returns SHA-256 of the full serialized transaction
// (java-tron TransactionCapsule.getMerkleHash).
func TxMerkleHash(tx *core.Transaction) ([]byte, error) {
	b, err := proto.Marshal(tx)
	if err != nil {
		return nil, err
	}
	return crypto.Sha256(b), nil
}

// CalcTxTrieRoot computes the transaction Merkle root over the given transactions.
// An empty transaction list yields ZERO_HASH, matching java-tron's setMerkleRoot.
func CalcTxTrieRoot(txs []*core.Transaction) ([]byte, error) {
	if len(txs) == 0 {
		return ZeroHash(), nil
	}
	leaves := make([][]byte, len(txs))
	for i, tx := range txs {
		h, err := TxMerkleHash(tx)
		if err != nil {
			return nil, err
		}
		leaves[i] = h
	}
	return merkle.Root(leaves), nil
}

// HeaderRawHash returns the plain SHA-256 of the serialized BlockHeader.raw
// (java-tron BlockCapsule.getRawHash), before the block-number prefix is applied.
func HeaderRawHash(raw *core.BlockHeaderRaw) ([]byte, error) {
	b, err := proto.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return crypto.Sha256(b), nil
}

// IDFromHeaderRaw returns the canonical block id for a header raw: SHA-256 of the
// serialized BlockHeader.raw with the first 8 bytes overwritten by the block number
// (big-endian). Matches Sha256Hash.generateBlockId / BlockCapsule.getBlockId.
func IDFromHeaderRaw(raw *core.BlockHeaderRaw) ([]byte, error) {
	h, err := HeaderRawHash(raw)
	if err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint64(h[0:8], uint64(raw.GetNumber()))
	return h, nil
}

// ID returns the canonical block id of a full block.
func ID(b *core.Block) ([]byte, error) {
	return IDFromHeaderRaw(b.GetBlockHeader().GetRawData())
}

// Number returns the block height.
func Number(b *core.Block) int64 {
	return b.GetBlockHeader().GetRawData().GetNumber()
}

// ParentHash returns the parent block id carried in the header.
func ParentHash(b *core.Block) []byte {
	return b.GetBlockHeader().GetRawData().GetParentHash()
}

// NumberFromID extracts the block number encoded in the first 8 bytes of a block id.
func NumberFromID(id []byte) int64 {
	if len(id) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(id[0:8]))
}
