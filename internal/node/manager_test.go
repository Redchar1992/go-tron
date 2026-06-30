package node

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/genesis"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// addrA/B/C are raw 21-byte (0x41-prefixed) addresses for the synthetic fork test.
var (
	addrA = append([]byte{0x41}, make([]byte, 20)...)
	addrB = append([]byte{0x41, 0xBB}, make([]byte, 19)...)
	addrC = append([]byte{0x41, 0xCC}, make([]byte, 19)...)
)

func transfer(t *testing.T, from, to []byte, amount int64) *core.Transaction {
	t.Helper()
	p, err := anypb.New(&core.TransferContract{OwnerAddress: from, ToAddress: to, Amount: amount})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type: core.Transaction_Contract_TransferContract, Parameter: p,
		}},
	}}
}

// mkBlock builds a block with a correct txTrieRoot in its header (so validateBlock passes).
func mkBlock(t *testing.T, num int64, parent []byte, ts int64, txs ...*core.Transaction) *core.Block {
	t.Helper()
	root, err := block.CalcTxTrieRoot(txs)
	if err != nil {
		t.Fatal(err)
	}
	return &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{
			Number: num, ParentHash: parent, Timestamp: ts, TxTrieRoot: root,
		}},
		Transactions: txs,
	}
}

func bal(t *testing.T, m *Manager, addr []byte) int64 {
	t.Helper()
	a, err := m.State().Accounts.Get(addr)
	if err != nil {
		return -1 // absent
	}
	return a.GetBalance()
}

// newManagerWithA seeds genesis with account A funded to 1000 and returns the genesis id.
func newManagerWithA(t *testing.T) (*Manager, []byte) {
	t.Helper()
	// Use a config that loads A directly via a raw-address asset. genesis.Config takes
	// Base58 addresses, so load state through a small custom genesis instead.
	m := NewManager(db.NewDatabase(db.NewMemKV()), 0)
	// Hand-seed genesis: load A, then seed khaos with an empty genesis block.
	if err := m.State().Accounts.Put(&core.Account{Address: addrA, Balance: 1000}); err != nil {
		t.Fatal(err)
	}
	gcfg := &genesis.Config{Timestamp: 0, ParentHash: "00", Number: 0}
	gb, err := gcfg.Block()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.khaos.Start(gb); err != nil {
		t.Fatal(err)
	}
	gid, err := block.ID(gb)
	if err != nil {
		t.Fatal(err)
	}
	return m, gid
}

func TestLinearReplayAppliesState(t *testing.T) {
	m, gid := newManagerWithA(t)
	b1 := mkBlock(t, 1, gid, 1000, transfer(t, addrA, addrB, 100))
	if err := m.PushBlock(b1); err != nil {
		t.Fatal(err)
	}
	if bal(t, m, addrA) != 900 || bal(t, m, addrB) != 100 {
		t.Fatalf("A=%d B=%d, want 900,100", bal(t, m, addrA), bal(t, m, addrB))
	}
	if m.Head().Num != 1 {
		t.Fatalf("head num = %d, want 1", m.Head().Num)
	}
}

// TestSwitchForkRollsBackAndReapplies builds a main branch, then a longer side branch,
// and verifies the Manager revokes main's state and applies the side branch's state.
func TestSwitchForkRollsBackAndReapplies(t *testing.T) {
	m, gid := newManagerWithA(t)

	// main: genesis <- 1a (A->B 100)
	b1a := mkBlock(t, 1, gid, 1001, transfer(t, addrA, addrB, 100))
	if err := m.PushBlock(b1a); err != nil {
		t.Fatal(err)
	}
	id1a, _ := block.ID(b1a)
	if bal(t, m, addrB) != 100 {
		t.Fatal("main branch should have credited B")
	}

	// side: genesis <- 1b (A->C 200), shorter — stored, not applied.
	b1b := mkBlock(t, 1, gid, 2002, transfer(t, addrA, addrC, 200))
	if err := m.PushBlock(b1b); err != nil {
		t.Fatal(err)
	}
	id1b, _ := block.ID(b1b)
	if bal(t, m, addrB) != 100 || bal(t, m, addrC) != -1 {
		t.Fatal("shorter side branch must not change applied state")
	}

	// extend side: genesis <- 1b <- 2b (A->C 50). Now side is longer -> switchFork.
	b2b := mkBlock(t, 2, id1b, 2003, transfer(t, addrA, addrC, 50))
	if err := m.PushBlock(b2b); err != nil {
		t.Fatal(err)
	}
	id2b, _ := block.ID(b2b)

	// Head must be 2b; B's credit from the abandoned main branch must be rolled back;
	// C must hold the side branch's two credits (200 + 50); A debited 250.
	if !bytesEqual(m.Head().ID, id2b) {
		t.Fatalf("head = %x, want 2b %x", m.Head().ID, id2b)
	}
	if bal(t, m, addrB) != -1 {
		t.Fatalf("B = %d, want absent after fork switch", bal(t, m, addrB))
	}
	if bal(t, m, addrC) != 250 {
		t.Fatalf("C = %d, want 250", bal(t, m, addrC))
	}
	if bal(t, m, addrA) != 750 {
		t.Fatalf("A = %d, want 750", bal(t, m, addrA))
	}
	_ = id1a // (kept for clarity of the abandoned branch)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
