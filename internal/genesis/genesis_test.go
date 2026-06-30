package genesis

import (
	"bytes"
	"encoding/hex"
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

// TestMainnetGenesisHash is the real M1 exit criterion: our computed genesis block id
// must equal the known TRON mainnet block-0 hash. It needs the mainnet genesis config
// (tron-deployment/main_net_config.conf) and the canonical block-0 hash. Deferred until
// those are wired in.
func TestMainnetGenesisHash(t *testing.T) {
	t.Skip("M1 exit: needs main_net_config.conf genesis values + canonical block-0 hash")
	_ = hex.EncodeToString
}
