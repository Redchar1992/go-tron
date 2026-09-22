package vmoracle

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Redchar1992/go-tron/internal/address"
	"github.com/Redchar1992/go-tron/internal/crypto"
)

type mainnetLiveFixture struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Network       string              `json:"network"`
	Contract      mainnetLiveContract `json:"contract"`
	Calls         []mainnetLiveCall   `json:"calls"`
}

type mainnetLiveContract struct {
	Name            string            `json:"name"`
	Address         string            `json:"address"`
	AddressHex      string            `json:"addressHex"`
	RuntimeBytecode string            `json:"runtimeBytecode"`
	CodeHash        string            `json:"codeHash"`
	Storage         map[string]string `json:"storage"`
}

type mainnetLiveCall struct {
	Method  string   `json:"method"`
	Args    []string `json:"args"`
	Data    string   `json:"data"`
	FromHex string   `json:"fromHex"`
	Return  string   `json:"return"`
}

func loadMainnetLiveFixture(t testing.TB) mainnetLiveFixture {
	t.Helper()
	path := filepath.Join(repoRoot(t), "test", "differential", "testdata", "mainnet_live_usdt.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live mainnet fixture: %v", err)
	}
	var fixture mainnetLiveFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatalf("decode live mainnet fixture: %v", err)
	}
	return fixture
}

func mainnetLiveWorld(t testing.TB, fixture mainnetLiveFixture) (World, string) {
	t.Helper()
	if fixture.SchemaVersion != 1 || fixture.Network != "mainnet" {
		t.Fatalf("invalid live fixture header: schema=%d network=%q", fixture.SchemaVersion, fixture.Network)
	}
	if len(fixture.Calls) == 0 || len(fixture.Contract.RuntimeBytecode) == 0 {
		t.Fatalf("live fixture has no calls or runtime code")
	}
	if len(fixture.Contract.AddressHex) != 42 || !strings.HasPrefix(fixture.Contract.AddressHex, "41") {
		t.Fatalf("invalid live contract address %q", fixture.Contract.AddressHex)
	}
	decoded, err := address.FromBase58(fixture.Contract.Address)
	if err != nil || decoded.Hex() != fixture.Contract.AddressHex {
		t.Fatalf("live contract address mismatch: %q / %q (%v)", fixture.Contract.Address, fixture.Contract.AddressHex, err)
	}
	code, err := hex.DecodeString(fixture.Contract.RuntimeBytecode)
	if err != nil || len(code) == 0 {
		t.Fatalf("invalid live runtime code: %v", err)
	}
	if got := hex.EncodeToString(crypto.Sha256(code)); got != fixture.Contract.CodeHash {
		t.Fatalf("runtime code hash = %s, want fixture %s", got, fixture.Contract.CodeHash)
	}
	for slot, value := range fixture.Contract.Storage {
		if len(slot) != 64 || len(value) != 64 {
			t.Fatalf("storage word %q=%q is not 32 bytes", slot, value)
		}
		if _, err := hex.DecodeString(slot + value); err != nil {
			t.Fatalf("invalid storage word %q=%q: %v", slot, value, err)
		}
	}
	owner := fixture.Calls[0].FromHex
	if len(owner) != 42 || !strings.HasPrefix(owner, "41") {
		t.Fatalf("invalid live caller %q", owner)
	}
	world := World{
		Version: 23,
		DynamicProps: DynamicProps{
			TotalEnergyWeight:       1_000_000_000,
			TotalEnergyCurrentLimit: 1_000_000_000,
			EnergyFee:               100,
		},
		Block: Block{Number: 86_000_000, Timestamp: 1_790_000_000_000, Witness: owner},
		Accounts: map[string]Account{
			owner: {
				Balance: 1_000_000_000_000,
			},
			fixture.Contract.AddressHex: {
				Code:    fixture.Contract.RuntimeBytecode,
				Storage: fixture.Contract.Storage,
			},
		},
	}
	return world, owner
}

// TestMainnetLiveUSDTFixture replays ABI calls captured from the latest TRON mainnet state.
// The fixture records eth_call return bytes and the exact storage words needed to reproduce
// those calls, so this test remains offline and deterministic after capture.
func TestMainnetLiveUSDTFixture(t *testing.T) {
	fixture := loadMainnetLiveFixture(t)
	world, owner := mainnetLiveWorld(t, fixture)
	for index, call := range fixture.Calls {
		result, err := Execute(world, Tx{
			Type:     "TriggerSmartContract",
			Owner:    owner,
			Contract: fixture.Contract.AddressHex,
			Data:     call.Data,
			FeeLimit: 1_000_000_000,
			TxID:     fmt.Sprintf("%064x", index+1),
		})
		if err != nil {
			t.Fatalf("call %d %s: %v", index, call.Method, err)
		}
		if result.Result != "SUCCESS" {
			t.Fatalf("call %d %s result=%s error=%s", index, call.Method, result.Result, result.VMError)
		}
		if result.Return != call.Return {
			t.Fatalf("call %d %s return=%s, want captured mainnet %s", index, call.Method, result.Return, call.Return)
		}
		if len(result.StorageWrites) != 0 || len(result.Logs) != 0 {
			t.Fatalf("read-only call %d %s changed state: writes=%v logs=%v", index, call.Method, result.StorageWrites, result.Logs)
		}
	}
	t.Logf("replayed %d captured mainnet USDT eth_call vectors", len(fixture.Calls))
}

// TestJavaOracleMainnetLiveFixture compares the same captured state against java-tron. It is
// gated because starting the Gradle-built oracle is intentionally excluded from fast CI.
func TestJavaOracleMainnetLiveFixture(t *testing.T) {
	if os.Getenv("JTRON_ORACLE_INTEGRATION") != "1" {
		t.Skip("set JTRON_ORACLE_INTEGRATION=1 to run the live mainnet fixture through java-tron")
	}
	fixture := loadMainnetLiveFixture(t)
	world, owner := mainnetLiveWorld(t, fixture)
	client, err := NewOracleClientFromEnv()
	if err != nil {
		t.Fatalf("start java oracle: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Ping(); err != nil {
		t.Fatalf("java oracle ping: %v", err)
	}
	for index, call := range fixture.Calls {
		tx := Tx{
			Type:     "TriggerSmartContract",
			Owner:    owner,
			Contract: fixture.Contract.AddressHex,
			Data:     call.Data,
			FeeLimit: 1_000_000_000,
			TxID:     fmt.Sprintf("%064x", index+1),
		}
		goResult, err := Execute(world, tx)
		if err != nil {
			t.Fatalf("call %d %s Go execute: %v", index, call.Method, err)
		}
		javaResult, err := client.Execute(world, tx)
		if err != nil {
			t.Fatalf("call %d %s Java execute: %v", index, call.Method, err)
		}
		if differences := Diff(goResult, javaResult); len(differences) != 0 {
			t.Fatalf("call %d %s diverged: %+v", index, call.Method, differences)
		}
	}
	t.Logf("compared %d captured mainnet USDT calls across Go and java-tron", len(fixture.Calls))
}
