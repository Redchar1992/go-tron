package vmoracle

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestJavaOracleCorpus runs the deterministic generator through both VMs. It is gated because
// starting the Java oracle requires the local JDK 17/java-tron toolchain; ordinary CI continues
// to run the fast Go-only determinism corpus.
func TestJavaOracleCorpus(t *testing.T) {
	if os.Getenv("JTRON_ORACLE_INTEGRATION") != "1" {
		t.Skip("set JTRON_ORACLE_INTEGRATION=1 to run cross-VM corpus")
	}
	// Keep the default large enough to cover the generator's opcode families while allowing CI
	// callers to trade runtime for breadth explicitly (for example, JTRON_ORACLE_CORPUS=512).
	count := envInt("JTRON_ORACLE_CORPUS", 64)
	if count < 1 {
		t.Skip("JTRON_ORACLE_CORPUS is less than one")
	}
	client, err := NewOracleClientFromEnv()
	if err != nil {
		t.Fatalf("start java oracle: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Ping(); err != nil {
		t.Fatalf("java oracle ping: %v", err)
	}

	for i := 0; i < count; i++ {
		seed := []byte{byte(i), byte(i >> 8), byte(i >> 16), 0x35, 0xe5}
		world, tx := GenCase(seed)
		goExecution, err := Execute(world, tx)
		if err != nil {
			t.Fatalf("seed %s go execute: %v", hex.EncodeToString(seed), err)
		}
		javaExecution, err := client.Execute(world, tx)
		if err != nil {
			t.Fatalf("seed %s java execute: %v", hex.EncodeToString(seed), err)
		}
		if differences := Diff(goExecution, javaExecution); len(differences) != 0 {
			shrunk := shrinkSeed(seed, func(candidate []byte) bool {
				w, x := GenCase(candidate)
				goResult, goErr := Execute(w, x)
				javaResult, javaErr := client.Execute(w, x)
				if goErr != nil || javaErr != nil {
					return goErr != nil || javaErr != nil
				}
				return len(Diff(goResult, javaResult)) != 0
			})
			path := writeRegressionVector(t, shrunk)
			t.Fatalf("seed %s diverged: %+v; shrunk vector: %s", hex.EncodeToString(seed), differences, path)
		}
	}
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

// shrinkSeed applies a deterministic delta-debugging pass. GenCase maps the seed to a complete
// world, so shrinking the seed is intentionally conservative: every accepted reduction is
// re-run through both VMs by the predicate supplied by the corpus test.
func shrinkSeed(seed []byte, diverges func([]byte) bool) []byte {
	shrunk := append([]byte(nil), seed...)
	for granularity := 2; len(shrunk) >= 2 && granularity <= len(shrunk); {
		chunkSize := (len(shrunk) + granularity - 1) / granularity
		reduced := false
		for start := 0; start < len(shrunk); start += chunkSize {
			end := start + chunkSize
			if end > len(shrunk) {
				end = len(shrunk)
			}
			candidate := append([]byte(nil), shrunk[:start]...)
			candidate = append(candidate, shrunk[end:]...)
			if len(candidate) > 0 && diverges(candidate) {
				shrunk = candidate
				reduced = true
				break
			}
		}
		if !reduced {
			granularity *= 2
		}
	}
	// Finish with a one-byte pass. The chunked pass is fast for large seeds but can stop at a
	// granularity where only a smaller single-byte deletion preserves the predicate.
	for changed := true; changed && len(shrunk) > 1; {
		changed = false
		for index := range shrunk {
			candidate := append([]byte(nil), shrunk[:index]...)
			candidate = append(candidate, shrunk[index+1:]...)
			if len(candidate) > 0 && diverges(candidate) {
				shrunk = candidate
				changed = true
				break
			}
		}
	}
	return shrunk
}

func writeRegressionVector(t testing.TB, seed []byte) string {
	t.Helper()
	world, tx := GenCase(seed)
	request := Request{World: &world, Tx: &tx}
	payload, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		t.Fatalf("marshal regression vector: %v", err)
	}
	directory := filepath.Join(repoRoot(t), "test", "differential", "vectors")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create regression vector directory: %v", err)
	}
	path := filepath.Join(directory, fmt.Sprintf("java-oracle-%s.json", hex.EncodeToString(seed)))
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write regression vector: %v", err)
	}
	return path
}

func repoRoot(t testing.TB) string {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for directory := workingDir; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	t.Fatalf("could not locate go-tron repository root from %s", workingDir)
	return ""
}
