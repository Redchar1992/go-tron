// Package config holds node configuration: defaults that mirror java-tron, plus
// optional overrides loaded from a file. M0 uses a minimal JSON loader (stdlib only,
// zero external deps); a HOCON/YAML loader matching java-tron's config.conf comes later.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Net is the P2P networking config.
type Net struct {
	P2PPort        int `json:"p2pPort"`
	P2PVersion     int `json:"p2pVersion"`
	MaxConnections int `json:"maxConnections"`
}

// Endpoint is a generic enable+port API endpoint.
type Endpoint struct {
	Enable bool `json:"enable"`
	Port   int  `json:"port"`
}

// Storage configures the KV engine and data directory.
type Storage struct {
	Dir    string `json:"dir"`
	Engine string `json:"engine"` // "leveldb" | "rocksdb" | "pebble"
}

// Config is the root node configuration.
type Config struct {
	Net     Net      `json:"net"`
	HTTP    Endpoint `json:"http"`
	GRPC    Endpoint `json:"grpc"`
	JSONRPC Endpoint `json:"jsonrpc"`
	Storage Storage  `json:"storage"`
}

// Default returns configuration matching java-tron mainnet defaults.
func Default() *Config {
	return &Config{
		Net:     Net{P2PPort: 18888, P2PVersion: 11111, MaxConnections: 30},
		HTTP:    Endpoint{Enable: true, Port: 8090},
		GRPC:    Endpoint{Enable: true, Port: 50051},
		JSONRPC: Endpoint{Enable: false, Port: 8545}, // off by default, matching java-tron
		Storage: Storage{Dir: "output-directory", Engine: "pebble"},
	}
}

// Load returns Default() merged with overrides from a JSON file at path.
// An empty path returns the defaults unchanged.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}
