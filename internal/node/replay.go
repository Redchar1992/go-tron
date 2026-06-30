package node

import (
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/replay"
)

// ReplayFile drives a differential replay of a captured-block fixture through a fresh
// Manager: it seeds the Manager from the first block, then pushes the rest, verifying
// each block's recomputed id + txTrieRoot against the on-chain oracle values. It is a
// diagnostic entrypoint (`gotron --replay <fixture>`) that exercises the M2/M2.5 pipeline
// end-to-end without networking. Returns the number of blocks replayed.
//
// Replay-provisioning is enabled so a fixture that starts mid-chain can still apply its
// TransferContract transactions (balances are not the property under test here; roots
// are). See Manager.EnableReplayProvisioning.
func ReplayFile(path string, log *slog.Logger) (int, error) {
	f, err := replay.Load(path)
	if err != nil {
		return 0, err
	}

	m := NewManager(db.NewDatabase(db.NewMemKV()), 0)
	m.EnableReplayProvisioning()

	root, err := f.Blocks[0].Build()
	if err != nil {
		return 0, err
	}
	if err := f.Blocks[0].Check(root); err != nil {
		return 0, err
	}
	if err := m.Start(root); err != nil {
		return 0, err
	}
	rootID, err := block.ID(root)
	if err != nil {
		return 0, err
	}
	log.Info("replay: seeded root", "num", block.Number(root), "id", hex.EncodeToString(rootID))

	for _, bf := range f.Blocks[1:] {
		b, err := bf.Build()
		if err != nil {
			return 0, err
		}
		if err := bf.Check(b); err != nil {
			return 0, err
		}
		if err := m.PushBlock(b); err != nil {
			return 0, fmt.Errorf("replay: push block %d: %w", bf.Number, err)
		}
		log.Info("replay: block ok", "num", m.Head().Num,
			"id", hex.EncodeToString(m.Head().ID), "txs", len(bf.Transactions))
	}

	log.Info("replay: complete (all roots match chain)",
		"blocks", len(f.Blocks), "head", m.Head().Num)
	return len(f.Blocks), nil
}
