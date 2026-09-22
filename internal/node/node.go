// Package node is the top-level orchestration object — the go-tron analog of
// java-tron's ApplicationImpl + Manager wiring. It owns subsystem lifecycle
// (start in dependency order, stop in reverse).
//
// The node opens a committed KV engine and constructs the Manager before external services
// start. P2P/API/consensus remain separate milestones; bootstrap and sync will own block flow.
package node

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/api"
	"github.com/Redchar1992/go-tron/internal/config"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/p2p"
)

// Mode is the role this node runs as.
type Mode string

const (
	ModeFullNode Mode = "fullnode"
	ModeWitness  Mode = "witness"  // full node + block production (SR)
	ModeSolidity Mode = "solidity" // serves confirmed state via a trusted full node
)

// Options are run-time toggles resolved from CLI flags.
type Options struct {
	Mode        Mode
	P2PDisabled bool
}

// Node wires configuration and subsystems together.
type Node struct {
	cfg  *config.Config
	opts Options
	log  *slog.Logger

	p2p *p2p.Service
	api *api.Server

	committed db.KV
	database  *db.Database
	manager   *Manager
}

// New constructs a Node from config and options.
func New(cfg *config.Config, opts Options, log *slog.Logger) *Node {
	return &Node{
		cfg:  cfg,
		opts: opts,
		log:  log,
		p2p:  p2p.New(&cfg.Net, log.With("sys", "p2p")),
		api:  api.New(cfg, log.With("sys", "api")),
	}
}

// Start brings subsystems up in dependency order.
func (n *Node) Start(ctx context.Context) error {
	n.log.Info("node: starting", "mode", n.opts.Mode, "p2pDisabled", n.opts.P2PDisabled,
		"storage", n.cfg.Storage.Engine)

	if n.committed != nil {
		return fmt.Errorf("node: already started")
	}
	committed, err := db.Open(n.cfg.Storage.Dir, n.cfg.Storage.Engine)
	if err != nil {
		return fmt.Errorf("node: open storage: %w", err)
	}
	n.committed = committed
	n.database = db.NewDatabase(committed)
	n.manager = NewManager(n.database, 0)
	n.log.Info("node: storage and manager ready", "engine", n.cfg.Storage.Engine,
		"dir", n.cfg.Storage.Dir)

	if !n.opts.P2PDisabled && n.opts.Mode != ModeSolidity {
		if err := n.p2p.Start(ctx); err != nil {
			_ = n.committed.Close()
			n.committed = nil
			n.database = nil
			n.manager = nil
			return err
		}
	} else {
		n.log.Info("node: p2p disabled for this mode")
	}

	if err := n.api.Start(ctx); err != nil {
		_ = n.committed.Close()
		n.committed = nil
		n.database = nil
		n.manager = nil
		return err
	}

	n.log.Info("node: started (P2P/API/consensus services remain under construction)")
	return nil
}

// Stop tears subsystems down in reverse order. Best-effort; logs and continues.
func (n *Node) Stop() error {
	n.log.Info("node: stopping")
	if err := n.api.Stop(); err != nil {
		n.log.Error("api stop", "err", err)
	}
	if err := n.p2p.Stop(); err != nil {
		n.log.Error("p2p stop", "err", err)
	}
	if n.committed != nil {
		if err := n.committed.Close(); err != nil {
			n.log.Error("storage close", "err", err)
		}
		n.committed = nil
		n.database = nil
		n.manager = nil
	}
	n.log.Info("node: stopped")
	return nil
}

// Manager returns the chain manager constructed during Start, or nil before Start.
func (n *Node) Manager() *Manager { return n.manager }
