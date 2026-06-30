// Package p2p is the networking layer. The transport is intended to run on go-libp2p,
// but the on-wire message format and protocol version MUST match java-tron so that
// java-tron peers accept go-tron (consensus-adjacent: wire-compatible, impl-replaceable).
//
// M0: lifecycle stub only — no real networking yet (target milestone: M4).
package p2p

import (
	"context"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/config"
)

// Service is the P2P networking service.
type Service struct {
	cfg *config.Net
	log *slog.Logger
}

// New constructs a P2P service.
func New(cfg *config.Net, log *slog.Logger) *Service {
	return &Service{cfg: cfg, log: log}
}

// Start brings up the networking service. No-op in M0.
func (s *Service) Start(_ context.Context) error {
	s.log.Info("p2p: stub start (M0 — networking not implemented)", "port", s.cfg.P2PPort, "version", s.cfg.P2PVersion)
	return nil
}

// Stop tears down the networking service. No-op in M0.
func (s *Service) Stop() error {
	s.log.Info("p2p: stub stop")
	return nil
}
