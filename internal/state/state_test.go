package state

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func newState() *State { return New(db.NewDatabase(db.NewMemKV())) }

func addr(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	a[20] = b
	return a
}

func TestAccountRoundTrip(t *testing.T) {
	s := newState()
	a := &core.Account{Address: addr(7), Balance: 12345}
	if err := s.Accounts.Put(a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Accounts.Get(addr(7))
	if err != nil {
		t.Fatal(err)
	}
	if got.GetBalance() != 12345 || !bytes.Equal(got.GetAddress(), addr(7)) {
		t.Fatalf("got %+v", got)
	}
	if has, _ := s.Accounts.Has(addr(7)); !has {
		t.Fatal("Has = false, want true")
	}
}

func TestAccountMissing(t *testing.T) {
	s := newState()
	if _, err := s.Accounts.Get(addr(9)); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if has, _ := s.Accounts.Has(addr(9)); has {
		t.Fatal("Has = true, want false")
	}
}

func TestAccountAndWitnessNamespacesDoNotCollide(t *testing.T) {
	s := newState()
	if err := s.Accounts.Put(&core.Account{Address: addr(1), Balance: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.Witnesses.Put(&core.Witness{Address: addr(1), VoteCount: 5}); err != nil {
		t.Fatal(err)
	}
	// same address byte, different stores — must not clobber each other
	a, err := s.Accounts.Get(addr(1))
	if err != nil || a.GetBalance() != 100 {
		t.Fatalf("account = %+v, err %v", a, err)
	}
	w, err := s.Witnesses.Get(addr(1))
	if err != nil || w.GetVoteCount() != 5 {
		t.Fatalf("witness = %+v, err %v", w, err)
	}
}

func TestRevokeRollsBackStateWrites(t *testing.T) {
	s := newState()
	_ = s.Accounts.Put(&core.Account{Address: addr(2), Balance: 1})
	s.DB.BuildSession()
	_ = s.Accounts.Put(&core.Account{Address: addr(2), Balance: 999})
	s.DB.Revoke()
	got, _ := s.Accounts.Get(addr(2))
	if got.GetBalance() != 1 {
		t.Fatalf("balance after revoke = %d, want 1", got.GetBalance())
	}
}
