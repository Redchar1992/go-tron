package vmoracle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestShrinkSeedPreservesPredicate(t *testing.T) {
	seed := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	shrunk := shrinkSeed(seed, func(candidate []byte) bool {
		return len(candidate) >= 2 && candidate[len(candidate)-1] == 0x06
	})
	if len(shrunk) != 2 || shrunk[1] != 0x06 {
		t.Fatalf("shrinkSeed = %x, want a minimal two-byte reproducer ending in 06", shrunk)
	}
}

// TestJavaOracleRegressionVectors replays every minimized vector committed under
// test/differential/vectors. It is gated for the same reason as TestJavaOracleCorpus.
func TestJavaOracleRegressionVectors(t *testing.T) {
	if os.Getenv("JTRON_ORACLE_INTEGRATION") != "1" {
		t.Skip("set JTRON_ORACLE_INTEGRATION=1 to replay Java regression vectors")
	}
	directory := filepath.Join(repoRoot(t), "test", "differential", "vectors")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		t.Skip("no Java regression vectors")
	}
	if err != nil {
		t.Fatalf("read regression vector directory: %v", err)
	}
	client, err := NewOracleClientFromEnv()
	if err != nil {
		t.Fatalf("start Java oracle: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Ping(); err != nil {
		t.Fatalf("Java oracle ping: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var request Request
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if request.World == nil || request.Tx == nil {
			t.Fatalf("%s does not contain world and tx", path)
		}
		goResult, err := Execute(*request.World, *request.Tx)
		if err != nil {
			t.Fatalf("%s Go execute: %v", path, err)
		}
		javaResult, err := client.Execute(*request.World, *request.Tx)
		if err != nil {
			t.Fatalf("%s Java execute: %v", path, err)
		}
		if differences := Diff(goResult, javaResult); len(differences) != 0 {
			t.Fatalf("%s diverged: %+v", path, differences)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("no JSON regression vectors")
	}
}
