package actuator

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

func transferTx(t *testing.T, owner, to []byte, amount int64) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.TransferContract{OwnerAddress: owner, ToAddress: to, Amount: amount})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type:      core.Transaction_Contract_TransferContract,
			Parameter: param,
		}},
	}}
}

func TestTransferApplyAndRollback(t *testing.T) {
	owner := []byte{0x41, 1, 2, 3}
	to := []byte{0x41, 9, 9, 9}
	d := db.NewDatabase(db.NewMemKV())
	st := state.New(d)
	if err := st.Accounts.Put(&core.Account{Address: owner, Balance: 1000}); err != nil {
		t.Fatal(err)
	}

	// Apply inside a revoking session, then revoke — state must be unchanged.
	d.BuildSession()
	if _, err := Apply(st, transferTx(t, owner, to, 300), BlockContext{}); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Accounts.Get(owner); a.GetBalance() != 700 {
		t.Fatalf("in-session owner balance = %d, want 700", a.GetBalance())
	}
	if a, _ := st.Accounts.Get(to); a.GetBalance() != 300 {
		t.Fatalf("in-session recipient balance = %d, want 300", a.GetBalance())
	}
	d.Revoke()
	if a, _ := st.Accounts.Get(owner); a.GetBalance() != 1000 {
		t.Fatalf("after revoke owner balance = %d, want 1000", a.GetBalance())
	}
	if has, _ := st.Accounts.Has(to); has {
		t.Fatal("recipient must not exist after revoke")
	}

	// Apply and commit — state persists.
	d.BuildSession()
	if _, err := Apply(st, transferTx(t, owner, to, 300), BlockContext{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Accounts.Get(owner); a.GetBalance() != 700 {
		t.Fatalf("after commit owner balance = %d, want 700", a.GetBalance())
	}
	if a, _ := st.Accounts.Get(to); a.GetBalance() != 300 {
		t.Fatalf("after commit recipient balance = %d, want 300", a.GetBalance())
	}
}

func TestTransferInsufficientBalance(t *testing.T) {
	owner := []byte{0x41, 1, 2, 3}
	st := state.New(db.NewDatabase(db.NewMemKV()))
	st.Accounts.Put(&core.Account{Address: owner, Balance: 100})
	if _, err := Apply(st, transferTx(t, owner, []byte{0x41, 5}, 500), BlockContext{}); err == nil {
		t.Fatal("expected insufficient-balance error")
	}
}

func TestUnhandledContractIsNoOp(t *testing.T) {
	st := state.New(db.NewDatabase(db.NewMemKV()))
	param, _ := anypb.New(&core.VoteWitnessContract{OwnerAddress: []byte{0x41, 1}})
	tx := &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{
			Type:      core.Transaction_Contract_VoteWitnessContract,
			Parameter: param,
		}},
	}}
	res, err := Apply(st, tx, BlockContext{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unhandled != 1 {
		t.Fatalf("unhandled count = %d, want 1", res.Unhandled)
	}
}
