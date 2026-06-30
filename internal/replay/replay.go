// Package replay loads captured-block fixtures (see test/differential/capture_fixtures.py)
// and reconstructs them into core.Block / core.Transaction objects, with a root check
// against the on-chain values. It is the single reconstruction code path shared by the
// differential test harness and the live `gotron --replay` diagnostic, so both verify
// the same way.
//
// A fixture's header txTrieRoot is intentionally recomputed from the transaction bytes
// (not copied from the fixture), so that checking the resulting block id against the
// on-chain id is an end-to-end check of merkle + header serialization + number prefix.
package replay

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/block"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// TxFixture is one captured transaction.
type TxFixture struct {
	TxID       string   `json:"txID"`
	RawDataHex string   `json:"rawDataHex"`
	Signatures []string `json:"signatures"`
}

// BlockFixture is one captured block plus its on-chain id/root (the oracle values).
type BlockFixture struct {
	Number           int64       `json:"number"`
	BlockID          string      `json:"blockID"`
	Timestamp        int64       `json:"timestamp"`
	ParentHash       string      `json:"parentHash"`
	TxTrieRoot       string      `json:"txTrieRoot"`
	WitnessAddress   string      `json:"witnessAddress"`
	WitnessID        int64       `json:"witnessId"`
	Version          int32       `json:"version"`
	AccountStateRoot string      `json:"accountStateRoot"`
	Transactions     []TxFixture `json:"transactions"`
}

// File is a captured fixture file ({"blocks": [...]}).
type File struct {
	Blocks []BlockFixture `json:"blocks"`
}

// Load reads and parses a fixture file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("replay: parse %s: %w", path, err)
	}
	if len(f.Blocks) == 0 {
		return nil, fmt.Errorf("replay: %s has no blocks", path)
	}
	return &f, nil
}

func decodeHex(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}

// BuildTx reconstructs a full transaction (raw_data + signatures) from a fixture.
func (tf TxFixture) BuildTx() (*core.Transaction, error) {
	rawBytes, err := decodeHex(tf.RawDataHex)
	if err != nil {
		return nil, fmt.Errorf("replay: tx %s rawDataHex: %w", tf.TxID, err)
	}
	var tr core.TransactionRaw
	if err := proto.Unmarshal(rawBytes, &tr); err != nil {
		return nil, fmt.Errorf("replay: tx %s unmarshal raw: %w", tf.TxID, err)
	}
	full := &core.Transaction{RawData: &tr}
	for _, s := range tf.Signatures {
		sb, err := decodeHex(s)
		if err != nil {
			return nil, fmt.Errorf("replay: tx %s signature: %w", tf.TxID, err)
		}
		full.Signature = append(full.Signature, sb)
	}
	return full, nil
}

// Build reconstructs the core.Block. The header's txTrieRoot is set to the root computed
// from the transactions (see package doc).
func (bf BlockFixture) Build() (*core.Block, error) {
	txs := make([]*core.Transaction, 0, len(bf.Transactions))
	for _, tf := range bf.Transactions {
		tx, err := tf.BuildTx()
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}
	root, err := block.CalcTxTrieRoot(txs)
	if err != nil {
		return nil, err
	}
	parent, err := decodeHex(bf.ParentHash)
	if err != nil {
		return nil, fmt.Errorf("replay: block %d parentHash: %w", bf.Number, err)
	}
	witness, err := decodeHex(bf.WitnessAddress)
	if err != nil {
		return nil, fmt.Errorf("replay: block %d witnessAddress: %w", bf.Number, err)
	}
	asr, err := decodeHex(bf.AccountStateRoot)
	if err != nil {
		return nil, fmt.Errorf("replay: block %d accountStateRoot: %w", bf.Number, err)
	}
	return &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{
			Timestamp:        bf.Timestamp,
			Number:           bf.Number,
			ParentHash:       parent,
			TxTrieRoot:       root,
			WitnessAddress:   witness,
			WitnessId:        bf.WitnessID,
			Version:          bf.Version,
			AccountStateRoot: asr,
		}},
		Transactions: txs,
	}, nil
}

// Check asserts the reconstructed block's recomputed txTrieRoot and block id equal the
// on-chain fixture values, byte-for-byte.
func (bf BlockFixture) Check(b *core.Block) error {
	gotRoot := hex.EncodeToString(b.GetBlockHeader().GetRawData().GetTxTrieRoot())
	if gotRoot != bf.TxTrieRoot {
		return fmt.Errorf("replay: block %d txTrieRoot mismatch: got %s want %s", bf.Number, gotRoot, bf.TxTrieRoot)
	}
	id, err := block.ID(b)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(id); got != bf.BlockID {
		return fmt.Errorf("replay: block %d id mismatch: got %s want %s", bf.Number, got, bf.BlockID)
	}
	return nil
}
