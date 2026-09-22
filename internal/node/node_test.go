package node

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Redchar1992/go-tron/internal/config"
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
