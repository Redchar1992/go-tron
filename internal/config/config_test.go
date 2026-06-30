package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultMatchesJavaTron(t *testing.T) {
	c := Default()
	if c.Net.P2PPort != 18888 {
		t.Errorf("P2PPort = %d, want 18888", c.Net.P2PPort)
	}
	if c.Net.P2PVersion != 11111 {
		t.Errorf("P2PVersion = %d, want 11111", c.Net.P2PVersion)
	}
	if c.HTTP.Port != 8090 || !c.HTTP.Enable {
		t.Errorf("HTTP = %+v, want {Enable:true Port:8090}", c.HTTP)
	}
	if c.GRPC.Port != 50051 || !c.GRPC.Enable {
		t.Errorf("GRPC = %+v, want {Enable:true Port:50051}", c.GRPC)
	}
	// JSON-RPC must be disabled by default, matching java-tron.
	if c.JSONRPC.Enable {
		t.Errorf("JSONRPC.Enable = true, want false (off by default)")
	}
	if c.JSONRPC.Port != 8545 {
		t.Errorf("JSONRPC.Port = %d, want 8545", c.JSONRPC.Port)
	}
}

func TestLoadEmptyPathReturnsDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error: %v", err)
	}
	if c.Net.P2PPort != 18888 {
		t.Errorf("default P2PPort = %d, want 18888", c.Net.P2PPort)
	}
}

func TestLoadOverridesFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(p, []byte(`{"http":{"enable":false,"port":9090}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if c.HTTP.Enable || c.HTTP.Port != 9090 {
		t.Errorf("HTTP = %+v, want {Enable:false Port:9090}", c.HTTP)
	}
	// untouched fields keep defaults
	if c.GRPC.Port != 50051 {
		t.Errorf("GRPC.Port = %d, want 50051 (default preserved)", c.GRPC.Port)
	}
}
