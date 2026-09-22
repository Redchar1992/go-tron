package node

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Redchar1992/go-tron/internal/config"
	"github.com/Redchar1992/go-tron/internal/genesis"
)

func TestNodeOpensPersistentManagerBeforeServices(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Dir = t.TempDir()
	cfg.Storage.Engine = "pebble"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	n := New(cfg, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n.Manager() == nil || n.database == nil || n.committed == nil {
		t.Fatalf("Start did not construct storage/manager: manager=%p database=%p store=%T",
			n.Manager(), n.database, n.committed)
	}
	if err := n.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n.Manager() != nil || n.committed != nil {
		t.Fatal("Stop left node storage or manager attached")
	}
}

func TestNodeStartRejectsDuplicateStart(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Dir = t.TempDir()
	cfg.Storage.Engine = "memory"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	n := New(cfg, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	if err := n.Start(context.Background()); err == nil {
		t.Fatal("duplicate Start unexpectedly succeeded")
	}
}

func TestNodeBootstrapsAndRestoresGenesisRoot(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Dir = t.TempDir()
	cfg.Storage.Engine = "pebble"
	cfg.Genesis = &genesis.Config{Timestamp: 1234, ParentHash: "00", Number: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	n := New(cfg, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("initial Start: %v", err)
	}
	first := n.Manager().Head()
	if first == nil || first.Num != 0 {
		t.Fatalf("initial head = %+v, want genesis", first)
	}
	firstID := append([]byte(nil), first.ID...)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}

	n = New(cfg, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("restore Start: %v", err)
	}
	defer n.Stop()
	restored := n.Manager().Head()
	if restored == nil || !bytesEqual(restored.ID, firstID) {
		t.Fatalf("restored head = %+v, want id %x", restored, firstID)
	}
}

func TestNodeRejectsGenesisNetworkMismatch(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	good := config.Default()
	good.Storage.Dir, good.Storage.Engine = dir, "pebble"
	good.Genesis = &genesis.Config{Timestamp: 1, ParentHash: "00", Number: 0}
	n := New(good, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = n.Stop()

	bad := config.Default()
	bad.Storage.Dir, bad.Storage.Engine = dir, "pebble"
	bad.Genesis = &genesis.Config{Timestamp: 2, ParentHash: "00", Number: 0}
	n = New(bad, Options{Mode: ModeSolidity, P2PDisabled: true}, logger)
	if err := n.Start(context.Background()); err == nil {
		_ = n.Stop()
		t.Fatal("Start accepted a mismatched persisted genesis")
	}
}
