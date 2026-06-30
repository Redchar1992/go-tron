// Package node is the top-level orchestration object — the go-tron analog of
// java-tron's ApplicationImpl + Manager wiring. It owns subsystem lifecycle
// (start in dependency order, stop in reverse).
//
// M0: lifecycle skeleton only. Subsystems (p2p, api) are stubs; the consensus
// core (state, db, actuator, tvm, consensus, the Manager state machine) is not
// wired yet — that arrives across M1–M4.
package node

import (
	"context"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/api"
	"github.com/Redchar1992/go-tron/internal/config"
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

	// TODO(M1–M4): open db -> state -> actuator/tvm -> consensus -> Manager before networking.

	if !n.opts.P2PDisabled && n.opts.Mode != ModeSolidity {
		if err := n.p2p.Start(ctx); err != nil {
			return err
		}
	} else {
		n.log.Info("node: p2p disabled for this mode")
	}

	if err := n.api.Start(ctx); err != nil {
		return err
	}

	n.log.Info("node: started (M0 scaffold — no chain processing yet)")
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
	n.log.Info("node: stopped")
	return nil
}
