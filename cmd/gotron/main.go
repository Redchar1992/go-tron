// Command gotron is the TRON full-node entrypoint.
//
// M0: parses flags, resolves config, and runs the node lifecycle skeleton
// (subsystems are stubs). It blocks until SIGINT/SIGTERM, then shuts down cleanly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Redchar1992/go-tron/internal/config"
	"github.com/Redchar1992/go-tron/internal/node"
	"github.com/Redchar1992/go-tron/internal/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		configPath  = flag.String("c", "", "path to config file (JSON; defaults used if empty)")
		witness     = flag.Bool("witness", false, "run as a Super Representative (block producer)")
		solidity    = flag.Bool("solidity", false, "run as a solidity (confirmed-state) node")
		p2pDisable  = flag.Bool("p2p-disable", false, "disable P2P networking (isolated API/diagnostics)")
		replayPath  = flag.String("replay", "", "differential-replay a captured-block fixture and exit (diagnostic)")
		logLevel    = flag.String("log-level", "info", "log level: debug|info|warn|error")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("gotron", version.String())
		return
	}

	log := newLogger(*logLevel)
	log.Info("gotron starting", "version", version.String())

	// Diagnostic mode: replay a fixture through the block pipeline and exit. Does not
	// start networking or the API — it verifies block id + txTrieRoot against the chain.
	if *replayPath != "" {
		n, err := node.ReplayFile(*replayPath, log)
		if err != nil {
			log.Error("replay failed", "err", err)
			os.Exit(1)
		}
		log.Info("replay finished", "blocks", n)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	mode := node.ModeFullNode
	switch {
	case *witness && *solidity:
		log.Error("flags --witness and --solidity are mutually exclusive")
		os.Exit(2)
	case *witness:
		mode = node.ModeWitness
	case *solidity:
		mode = node.ModeSolidity
	}

	n := node.New(cfg, node.Options{Mode: mode, P2PDisabled: *p2pDisable}, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := n.Start(ctx); err != nil {
		log.Error("node start failed", "err", err)
		os.Exit(1)
	}

	<-ctx.Done()
	log.Info("shutdown signal received")
	if err := n.Stop(); err != nil {
		log.Error("node stop failed", "err", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
