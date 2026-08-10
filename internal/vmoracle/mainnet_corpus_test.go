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

// mainnetContractCorpus is a checked-in snapshot.  Keeping the ABI alongside runtime code
// makes every calldata vector auditable and lets the fixture be refreshed without changing the
// execution harness.
type mainnetContractCorpus struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Network       string                   `json:"network"`
	CapturedAt    string                   `json:"capturedAt"`
	Contracts     []mainnetContractFixture `json:"contracts"`
}

type mainnetContractFixture struct {
	Name            string            `json:"name"`
	Address         string            `json:"address"`
	AddressHex      string            `json:"addressHex"`
	OriginAddress   string            `json:"originAddress"`
	CodeHash        string            `json:"codeHash"`
	RuntimeBytecode string            `json:"runtimeBytecode"`
	ABI             mainnetABI        `json:"abi"`
	Calldata        []mainnetCalldata `json:"calldata"`
}

type mainnetABI struct {
	Entrys []mainnetABIEntry `json:"entrys"`
}

type mainnetABIEntry struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	StateMutability string            `json:"stateMutability"`
	Inputs          []mainnetABIInput `json:"inputs"`
}

type mainnetABIInput struct {
	Type string `json:"type"`
}

type mainnetCalldata struct {
	Method string   `json:"method"`
	Args   []string `json:"args"`
	Data   string   `json:"data"`
}

func loadMainnetContractCorpus(t testing.TB) mainnetContractCorpus {
	t.Helper()
	path := filepath.Join(repoRoot(t), "test", "differential", "testdata", "mainnet_contracts.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mainnet contract corpus: %v", err)
	}
	var corpus mainnetContractCorpus
	if err := json.Unmarshal(payload, &corpus); err != nil {
		t.Fatalf("decode mainnet contract corpus: %v", err)
	}
	return corpus
}

func TestMainnetContractCorpusFixture(t *testing.T) {
	corpus := loadMainnetContractCorpus(t)
	if corpus.SchemaVersion != 1 || corpus.Network != "mainnet" || len(corpus.Contracts) == 0 {
		t.Fatalf("invalid mainnet corpus header: %+v", corpus)
	}
	seen := make(map[string]bool, len(corpus.Contracts))
	for _, contract := range corpus.Contracts {
		if seen[contract.AddressHex] {
			t.Fatalf("duplicate contract address %s", contract.AddressHex)
		}
		seen[contract.AddressHex] = true
		if len(contract.AddressHex) != 42 || !strings.HasPrefix(contract.AddressHex, "41") {
			t.Fatalf("%s has invalid hex address %q", contract.Name, contract.AddressHex)
		}
		decodedAddress, err := address.FromBase58(contract.Address)
		if err != nil || decodedAddress.Hex() != contract.AddressHex {
			t.Fatalf("%s address %q does not match %s: %v", contract.Name, contract.Address, contract.AddressHex, err)
		}
		if _, err := hex.DecodeString(contract.RuntimeBytecode); err != nil || len(contract.RuntimeBytecode) < 2 {
			t.Fatalf("%s has invalid runtime bytecode: %v", contract.Name, err)
		}
		if len(contract.ABI.Entrys) == 0 || len(contract.Calldata) == 0 {
			t.Fatalf("%s is missing ABI or calldata", contract.Name)
		}
		for _, call := range contract.Calldata {
			if len(call.Data) < 8 || len(call.Data)%2 != 0 {
				t.Fatalf("%s %s has malformed calldata %q", contract.Name, call.Method, call.Data)
			}
			if _, err := hex.DecodeString(call.Data); err != nil {
				t.Fatalf("%s %s calldata is not hex: %v", contract.Name, call.Method, err)
			}
			selector := hex.EncodeToString(crypto.Keccak256([]byte(call.Method))[:4])
			if selector != call.Data[:8] {
				t.Fatalf("%s %s selector=%s, want %s", contract.Name, call.Method, call.Data[:8], selector)
			}
			if !abiHasMethod(contract.ABI.Entrys, call.Method) {
				t.Fatalf("%s calldata method %s is absent from ABI", contract.Name, call.Method)
			}
		}
	}
}

func abiHasMethod(entries []mainnetABIEntry, signature string) bool {
	open := strings.IndexByte(signature, '(')
	if open < 1 || !strings.HasSuffix(signature, ")") {
		return false
	}
	name, types := signature[:open], strings.TrimSuffix(signature[open+1:], ")")
	want := []string{}
	if types != "" {
		want = strings.Split(types, ",")
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Type, "function") && entry.Name == name && len(entry.Inputs) == len(want) {
			match := true
			for index := range want {
				if entry.Inputs[index].Type != want[index] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// TestJavaOracleMainnetContractCorpus runs ABI calldata against captured deployed runtime code
// in both VMs.  It is gated because the Java side starts a Gradle-built java-tron oracle.
func TestJavaOracleMainnetContractCorpus(t *testing.T) {
	if os.Getenv("JTRON_ORACLE_INTEGRATION") != "1" {
		t.Skip("set JTRON_ORACLE_INTEGRATION=1 to run mainnet contract corpus")
	}
	corpus := loadMainnetContractCorpus(t)
	client, err := NewOracleClientFromEnv()
	if err != nil {
		t.Fatalf("start java oracle: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Ping(); err != nil {
		t.Fatalf("java oracle ping: %v", err)
	}

	const owner = "410000000000000000000000000000000000000001"
	world := World{
		Version: 23,
		DynamicProps: DynamicProps{
			TotalEnergyWeight:       1_000_000_000,
			TotalEnergyCurrentLimit: 1_000_000_000,
			EnergyFee:               100,
		},
		Block:    Block{Number: 72_000_000, Timestamp: 1_720_000_000_000, Witness: owner},
		Accounts: map[string]Account{owner: {Balance: 1_000_000_000_000}},
	}
	for _, contract := range corpus.Contracts {
		world.Accounts[contract.AddressHex] = Account{Code: contract.RuntimeBytecode}
	}

	checked := 0
	for _, contract := range corpus.Contracts {
		for index, call := range contract.Calldata {
			tx := Tx{
				Type:     "TriggerSmartContract",
				Owner:    owner,
				Contract: contract.AddressHex,
				Data:     call.Data,
				FeeLimit: 1_000_000_000,
				TxID:     fmt.Sprintf("%064x", checked+1),
			}
			goResult, err := Execute(world, tx)
			if err != nil {
				t.Fatalf("%s call %d %s Go execute: %v", contract.Name, index, call.Method, err)
			}
			javaResult, err := client.Execute(world, tx)
			if err != nil {
				t.Fatalf("%s call %d %s Java execute: %v", contract.Name, index, call.Method, err)
			}
			if differences := Diff(goResult, javaResult); len(differences) != 0 {
				t.Fatalf("%s call %d %s diverged: %+v\nGo=%+v\nJava=%+v", contract.Name, index, call.Method, differences, goResult, javaResult)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("mainnet contract corpus is empty")
	}
	t.Logf("checked %d mainnet runtime/ABI calldata vectors across %d contracts", checked, len(corpus.Contracts))
}
