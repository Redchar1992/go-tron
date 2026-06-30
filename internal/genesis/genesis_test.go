package genesis

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Redchar1992/go-tron/internal/address"
	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

const testAddr = "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb" // known TRON address

func testConfig() *Config {
	return &Config{
		Timestamp:  0,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		Number:     0,
		Assets:     []Asset{{Name: "x", Address: testAddr, Balance: 1000}},
		Witnesses:  []Witness{{Address: testAddr, URL: "http://t", VoteCount: 3}},
	}
}

func TestNewGenesisTransactionShape(t *testing.T) {
	a, _ := address.FromBase58(testAddr)
	tx, err := NewGenesisTransaction(a.Bytes(), 42)
	if err != nil {
		t.Fatal(err)
	}
	c := tx.GetRawData().GetContract()
	if len(c) != 1 {
		t.Fatalf("contracts = %d, want 1", len(c))
	}
	if c[0].GetType() != core.Transaction_Contract_TransferContract {
		t.Fatalf("type = %v", c[0].GetType())
	}
	const wantURL = "type.googleapis.com/protocol.TransferContract"
	if got := c[0].GetParameter().GetTypeUrl(); got != wantURL {
		t.Fatalf("type_url = %q, want %q", got, wantURL)
	}
	var tc core.TransferContract
	if err := c[0].GetParameter().UnmarshalTo(&tc); err != nil {
		t.Fatal(err)
	}
	if tc.GetAmount() != 42 {
		t.Errorf("amount = %d, want 42", tc.GetAmount())
	}
	if !bytes.Equal(tc.GetToAddress(), a.Bytes()) {
		t.Errorf("to = %x, want %x", tc.GetToAddress(), a.Bytes())
	}
	if string(tc.GetOwnerAddress()) != "0x000000000000000000000" {
		t.Errorf("owner = %q", tc.GetOwnerAddress())
	}
}

func TestTxTrieRootSingleEqualsLeaf(t *testing.T) {
	c := testConfig()
	txs, _ := c.Transactions()
	leaf, _ := TxMerkleHash(txs[0])
	root, err := c.TxTrieRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, leaf) {
		t.Fatal("single-tx txTrieRoot must equal the tx merkle hash")
	}
}

func TestBlockIDDeterministicAndSensitive(t *testing.T) {
	c := testConfig()
	id1, err := c.BlockID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id1) != 32 {
		t.Fatalf("block id len = %d, want 32", len(id1))
	}
	id2, _ := c.BlockID()
	if !bytes.Equal(id1, id2) {
		t.Fatal("BlockID not deterministic")
	}
	// changing the timestamp must change the id
	c2 := testConfig()
	c2.Timestamp = 1
	id3, _ := c2.BlockID()
	if bytes.Equal(id1, id3) {
		t.Fatal("BlockID insensitive to timestamp")
	}
}

func TestLoadState(t *testing.T) {
	c := testConfig()
	st := state.New(db.NewDatabase(db.NewMemKV()))
	if err := c.Load(st); err != nil {
		t.Fatal(err)
	}
	a, _ := address.FromBase58(testAddr)
	acc, err := st.Accounts.Get(a.Bytes())
	if err != nil || acc.GetBalance() != 1000 {
		t.Fatalf("account = %+v, err %v", acc, err)
	}
	w, err := st.Witnesses.Get(a.Bytes())
	if err != nil || w.GetVoteCount() != 3 {
		t.Fatalf("witness = %+v, err %v", w, err)
	}
}

// mainnetFixture is the canonical TRON mainnet block-0, captured from TronGrid
// (getblockbynum num=0) into testdata/mainnet_genesis.json.
type mainnetFixture struct {
	BlockID    string `json:"blockID"`
	TxTrieRoot string `json:"txTrieRoot"`
	ParentHash string `json:"parentHash"`
	WitnessHex string `json:"witnessHex"`
	Timestamp  int64  `json:"timestamp"`
	Number     int64  `json:"number"`
	Txs        []struct {
		To     string `json:"to"`
		Amount int64  `json:"amount"`
	} `json:"txs"`
}

// TestMainnetGenesisHash is the M1 exit criterion: rebuilding the real TRON mainnet
// genesis from its transactions must reproduce the canonical block-0 txTrieRoot AND
// block id, byte-for-byte.
func TestMainnetGenesisHash(t *testing.T) {
	raw, err := os.ReadFile("testdata/mainnet_genesis.json")
	if err != nil {
		t.Fatal(err)
	}
	var f mainnetFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}

	// Sanity: our genesis witness note matches the on-chain witness_address bytes.
	if got := hex.EncodeToString([]byte(WitnessNote)); got != f.WitnessHex {
		t.Fatalf("witness note hex mismatch:\n got  %s\n want %s", got, f.WitnessHex)
	}

	// Rebuild the genesis transactions in order and check txTrieRoot.
	txs := make([]*core.Transaction, 0, len(f.Txs))
	for i, r := range f.Txs {
		to, err := hex.DecodeString(r.To)
		if err != nil {
			t.Fatalf("tx %d to: %v", i, err)
		}
		tx, err := NewGenesisTransaction(to, r.Amount)
		if err != nil {
			t.Fatalf("tx %d: %v", i, err)
		}
		txs = append(txs, tx)
	}
	root, err := TxTrieRootOf(txs)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(root); got != f.TxTrieRoot {
		t.Fatalf("txTrieRoot mismatch:\n got  %s\n want %s", got, f.TxTrieRoot)
	}

	// Build the genesis header and check the block id.
	parent, err := hex.DecodeString(f.ParentHash)
	if err != nil {
		t.Fatal(err)
	}
	header := &core.BlockHeaderRaw{
		Timestamp:      f.Timestamp,
		ParentHash:     parent,
		Number:         f.Number,
		TxTrieRoot:     root,
		WitnessAddress: []byte(WitnessNote),
	}
	id, err := BlockID(header)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(id); got != f.BlockID {
		t.Fatalf("genesis block id mismatch:\n got  %s\n want %s", got, f.BlockID)
	}
}
