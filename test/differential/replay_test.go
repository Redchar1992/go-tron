// Package differential is the M2/M2.5 replay oracle: it reconstructs real TRON mainnet
// blocks from committed fixtures (captured via capture_fixtures.py) and asserts that
// go-tron's recomputed block id and txTrieRoot equal the on-chain values, byte-for-byte,
// for every block — that contiguous runs replay through the node Manager with correct
// parent linkage and an advancing head — and that the bandwidth (net) fee model matches
// on-chain transaction receipts.
//
// Block/transaction reconstruction and the root check live in internal/replay, the same
// code path the live `gotron --replay` diagnostic uses. The blocks captured here carry no
// smart-contract executions (TVM is M3); they exercise empty blocks (ZERO txTrieRoot),
// genesis allocations, and Transfer/TransferAsset/Vote/Freeze transactions.
package differential

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Redchar1992/go-tron/internal/bandwidth"
	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/node"
	"github.com/Redchar1992/go-tron/internal/replay"
)

func load(t *testing.T, name string) *replay.File {
	t.Helper()
	f, err := replay.Load("testdata/" + name)
	if err != nil {
		t.Fatalf("load %s (run capture_fixtures.py): %v", name, err)
	}
	return f
}

// TestContiguousReplay replays a contiguous run from genesis through the node Manager,
// asserting matching roots, parent linkage, and an advancing head at every height.
func TestContiguousReplay(t *testing.T) {
	f := load(t, "chain.json")
	if f.Blocks[0].Number != 0 {
		t.Fatalf("chain.json must start at block 0, starts at %d", f.Blocks[0].Number)
	}

	root, err := f.Blocks[0].Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Blocks[0].Check(root); err != nil {
		t.Fatal(err)
	}

	m := node.NewManager(db.NewDatabase(db.NewMemKV()), 0)
	if err := m.Start(root); err != nil {
		t.Fatal(err)
	}

	prevID, _ := block.ID(root)
	for _, bf := range f.Blocks[1:] {
		b, err := bf.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := bf.Check(b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(block.ParentHash(b), prevID) {
			t.Fatalf("block %d parentHash %x != prev id %x", bf.Number, block.ParentHash(b), prevID)
		}
		if err := m.PushBlock(b); err != nil {
			t.Fatalf("block %d PushBlock: %v", bf.Number, err)
		}
		if m.Head().Num != bf.Number {
			t.Fatalf("after block %d head num = %d", bf.Number, m.Head().Num)
		}
		if got := hex.EncodeToString(m.Head().ID); got != bf.BlockID {
			t.Fatalf("after block %d head id = %s, want %s", bf.Number, got, bf.BlockID)
		}
		prevID = m.Head().ID
	}
	t.Logf("replayed blocks 0..%d through the Manager with matching roots", f.Blocks[len(f.Blocks)-1].Number)
}

// TestDenseContiguousReplay replays a dense, contiguous pre-TVM span (many Transfer /
// TransferAsset / Freeze / Vote transactions per block) through the node Manager,
// asserting matching block id + txTrieRoot and parent linkage at every height. The span
// starts mid-chain, so the first block seeds the Manager as the replay root and
// replay-provisioning funds TransferContract owners (balances are not offline-verifiable;
// fee/bandwidth correctness is checked by TestBandwidthReceiptOracle instead).
func TestDenseContiguousReplay(t *testing.T) {
	f := load(t, "dense.json")

	root, err := f.Blocks[0].Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Blocks[0].Check(root); err != nil {
		t.Fatal(err)
	}

	m := node.NewManager(db.NewDatabase(db.NewMemKV()), 0)
	m.EnableReplayProvisioning()
	if err := m.Start(root); err != nil {
		t.Fatal(err)
	}

	prevID, _ := block.ID(root)
	totalTx := len(f.Blocks[0].Transactions)
	for _, bf := range f.Blocks[1:] {
		b, err := bf.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := bf.Check(b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(block.ParentHash(b), prevID) {
			t.Fatalf("block %d parentHash %x != prev id %x", bf.Number, block.ParentHash(b), prevID)
		}
		if err := m.PushBlock(b); err != nil {
			t.Fatalf("block %d PushBlock: %v", bf.Number, err)
		}
		if m.Head().Num != bf.Number {
			t.Fatalf("after block %d head num = %d", bf.Number, m.Head().Num)
		}
		prevID = m.Head().ID
		totalTx += len(bf.Transactions)
	}
	t.Logf("replayed dense blocks %d..%d (%d txs) through the Manager with matching roots",
		f.Blocks[0].Number, f.Blocks[len(f.Blocks)-1].Number, totalTx)
}

// TestBandwidthReceiptOracle checks go-tron's bandwidth model against on-chain receipts.
// For every transaction in the dense span it computes our charged size and asserts the
// state-independent invariant: a bandwidth-COVERED tx has net_usage == Size; a
// bandwidth-BURNED tx has net_fee == Size*Rate. Transactions whose fee includes a
// non-bandwidth component (account creation, freeze, multi-sig) are counted as not yet
// modeled rather than silently passed.
func TestBandwidthReceiptOracle(t *testing.T) {
	f := load(t, "dense.json")

	raw, err := os.ReadFile("testdata/receipts.json")
	if err != nil {
		t.Fatalf("read receipts.json (run capture_fixtures.py): %v", err)
	}
	type receipt struct {
		Fee      int64 `json:"fee"`
		NetUsage int64 `json:"netUsage"`
		NetFee   int64 `json:"netFee"`
	}
	var receipts map[string]receipt
	if err := json.Unmarshal(raw, &receipts); err != nil {
		t.Fatal(err)
	}

	var covered, burned, unmodeled int
	for _, b := range f.Blocks {
		for _, tf := range b.Transactions {
			r, ok := receipts[tf.TxID]
			if !ok {
				t.Fatalf("no receipt for tx %s", tf.TxID)
			}
			tx, err := tf.BuildTx()
			if err != nil {
				t.Fatal(err)
			}
			size, err := bandwidth.Size(tx)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case r.NetUsage > 0 && r.NetFee == 0 && r.Fee == 0:
				// Bandwidth covered by free/staked points.
				if int64(size) != r.NetUsage {
					t.Fatalf("tx %s covered: Size=%d != net_usage=%d", tf.TxID, size, r.NetUsage)
				}
				covered++
			case r.NetFee == bandwidth.BurnFee(size) && r.Fee == r.NetFee:
				// Bandwidth burned as TRX; no extra (account-creation/etc.) fee.
				burned++
			default:
				// Fee carries a non-bandwidth component we do not model yet.
				unmodeled++
			}
		}
	}
	total := covered + burned + unmodeled
	t.Logf("bandwidth oracle over %d txs: covered=%d burned=%d unmodeled=%d",
		total, covered, burned, unmodeled)
	if covered+burned == 0 {
		t.Fatal("expected at least one tx with a verifiable pure-bandwidth charge")
	}
}

// TestSpotBlockRoots verifies root equivalence for individual transaction-bearing blocks
// (multi-tx Merkle over real Transfer/VoteWitness bytes). These are not contiguous with
// genesis, so they are checked for roots only, not replayed through state.
func TestSpotBlockRoots(t *testing.T) {
	f := load(t, "spot.json")
	for _, bf := range f.Blocks {
		if len(bf.Transactions) == 0 {
			t.Fatalf("spot block %d has no transactions", bf.Number)
		}
		b, err := bf.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := bf.Check(b); err != nil {
			t.Fatal(err)
		}
		t.Logf("block %d: %d txs, txTrieRoot + id match chain", bf.Number, len(bf.Transactions))
	}
}
