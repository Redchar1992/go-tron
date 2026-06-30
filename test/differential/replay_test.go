// Package differential is the M2 replay oracle: it reconstructs real TRON mainnet blocks
// from committed fixtures (captured via capture_fixtures.py) and asserts that go-tron's
// recomputed block id and txTrieRoot equal the on-chain values, byte-for-byte, for every
// block — and that the contiguous run replays through the node Manager with correct
// parent linkage and an advancing head.
//
// This is the M2 exit criterion: "replay the first N mainnet blocks (no smart contracts)
// with matching roots." The blocks captured here carry no smart-contract executions
// (TVM is M3); they exercise empty blocks (ZERO txTrieRoot), genesis allocations, and
// TransferContract/VoteWitnessContract transactions.
package differential

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/node"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

type txFixture struct {
	RawDataHex string   `json:"rawDataHex"`
	Signatures []string `json:"signatures"`
}

type blockFixture struct {
	Number           int64       `json:"number"`
	BlockID          string      `json:"blockID"`
	Timestamp        int64       `json:"timestamp"`
	ParentHash       string      `json:"parentHash"`
	TxTrieRoot       string      `json:"txTrieRoot"`
	WitnessAddress   string      `json:"witnessAddress"`
	WitnessID        int64       `json:"witnessId"`
	Version          int32       `json:"version"`
	AccountStateRoot string      `json:"accountStateRoot"`
	Transactions     []txFixture `json:"transactions"`
}

type fixtureFile struct {
	Blocks []blockFixture `json:"blocks"`
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func loadFixtures(t *testing.T, name string) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s (run capture_fixtures.py): %v", name, err)
	}
	var f fixtureFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) == 0 {
		t.Fatalf("%s has no blocks", name)
	}
	return f
}

// buildBlock reconstructs a core.Block from a fixture. The header's txTrieRoot is set to
// OUR recomputed root (from the transaction bytes), not the fixture's — so that asserting
// the resulting block id against the on-chain id is an end-to-end check of merkle +
// header serialization + the block-number prefix.
func buildBlock(t *testing.T, fx blockFixture) *core.Block {
	t.Helper()
	txs := make([]*core.Transaction, 0, len(fx.Transactions))
	for i, tf := range fx.Transactions {
		var tr core.TransactionRaw
		if err := proto.Unmarshal(mustHex(t, tf.RawDataHex), &tr); err != nil {
			t.Fatalf("block %d tx %d unmarshal raw: %v", fx.Number, i, err)
		}
		full := &core.Transaction{RawData: &tr}
		for _, s := range tf.Signatures {
			full.Signature = append(full.Signature, mustHex(t, s))
		}
		txs = append(txs, full)
	}
	root, err := block.CalcTxTrieRoot(txs)
	if err != nil {
		t.Fatal(err)
	}
	return &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{
			Timestamp:        fx.Timestamp,
			Number:           fx.Number,
			ParentHash:       mustHex(t, fx.ParentHash),
			TxTrieRoot:       root,
			WitnessAddress:   mustHex(t, fx.WitnessAddress),
			WitnessId:        fx.WitnessID,
			Version:          fx.Version,
			AccountStateRoot: mustHex(t, fx.AccountStateRoot),
		}},
		Transactions: txs,
	}
}

// assertRoots checks that our recomputed txTrieRoot and block id match the on-chain
// fixture values byte-for-byte.
func assertRoots(t *testing.T, fx blockFixture, b *core.Block) {
	t.Helper()
	gotRoot := hex.EncodeToString(b.GetBlockHeader().GetRawData().GetTxTrieRoot())
	if gotRoot != fx.TxTrieRoot {
		t.Fatalf("block %d txTrieRoot mismatch:\n got  %s\n want %s", fx.Number, gotRoot, fx.TxTrieRoot)
	}
	id, err := block.ID(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(id); got != fx.BlockID {
		t.Fatalf("block %d id mismatch:\n got  %s\n want %s", fx.Number, got, fx.BlockID)
	}
}

// TestContiguousReplay replays a contiguous run from genesis through the node Manager,
// asserting matching roots, parent linkage, and an advancing head at every height.
func TestContiguousReplay(t *testing.T) {
	f := loadFixtures(t, "chain.json")

	// Root (block 0): verify roots, then seed the Manager from it.
	root := buildBlock(t, f.Blocks[0])
	if f.Blocks[0].Number != 0 {
		t.Fatalf("chain.json must start at block 0, starts at %d", f.Blocks[0].Number)
	}
	assertRoots(t, f.Blocks[0], root)

	m := node.NewManager(db.NewDatabase(db.NewMemKV()), 0)
	if err := m.Start(root); err != nil {
		t.Fatal(err)
	}

	prevID, _ := block.ID(root)
	for _, fx := range f.Blocks[1:] {
		b := buildBlock(t, fx)
		assertRoots(t, fx, b)

		// Parent linkage: the header's parentHash must equal the prior block's id.
		if !bytes.Equal(block.ParentHash(b), prevID) {
			t.Fatalf("block %d parentHash %x != prev id %x", fx.Number, block.ParentHash(b), prevID)
		}

		if err := m.PushBlock(b); err != nil {
			t.Fatalf("block %d PushBlock: %v", fx.Number, err)
		}
		if m.Head().Num != fx.Number {
			t.Fatalf("after block %d head num = %d", fx.Number, m.Head().Num)
		}
		if got := hex.EncodeToString(m.Head().ID); got != fx.BlockID {
			t.Fatalf("after block %d head id = %s, want %s", fx.Number, got, fx.BlockID)
		}
		prevID = m.Head().ID
	}
	t.Logf("replayed blocks 0..%d through the Manager with matching roots", f.Blocks[len(f.Blocks)-1].Number)
}

// TestSpotBlockRoots verifies root equivalence for individual transaction-bearing blocks
// (multi-tx Merkle over real TransferContract/VoteWitnessContract bytes). These are not
// contiguous with genesis, so they are checked for roots only, not replayed through state.
func TestSpotBlockRoots(t *testing.T) {
	f := loadFixtures(t, "spot.json")
	for _, fx := range f.Blocks {
		if len(fx.Transactions) == 0 {
			t.Fatalf("spot block %d has no transactions", fx.Number)
		}
		b := buildBlock(t, fx)
		assertRoots(t, fx, b)
		t.Logf("block %d: %d txs, txTrieRoot + id match chain", fx.Number, len(fx.Transactions))
	}
}
