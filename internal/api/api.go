// Package api hosts the node's external interfaces: gRPC (Wallet/WalletSolidity/
// Database/Monitor, generated from api.proto), HTTP REST, and ETH-compatible JSON-RPC.
// Read paths and submit paths only; the api layer is a thin adapter over node/state.
//
// M0: lifecycle stub only — no servers bound yet (target milestone: M5).
package api

import (
	"context"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/config"
)

// Server aggregates the API endpoints.
type Server struct {
	cfg *config.Config
	log *slog.Logger
}

// New constructs the API server.
func New(cfg *config.Config, log *slog.Logger) *Server {
	return &Server{cfg: cfg, log: log}
}

// Start binds the enabled endpoints. No-op in M0.
func (s *Server) Start(_ context.Context) error {
	s.log.Info("api: stub start (M0 — servers not implemented)",
		"http", s.cfg.HTTP.Enable, "grpc", s.cfg.GRPC.Enable, "jsonrpc", s.cfg.JSONRPC.Enable)
	return nil
}

// Stop shuts the endpoints down. No-op in M0.
func (s *Server) Stop() error {
	s.log.Info("api: stub stop")
	return nil
}
