package state

import (
	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

var (
	accountPrefix = []byte("a/")
	witnessPrefix = []byte("w/")
)

func nsKey(prefix, k []byte) []byte {
	out := make([]byte, 0, len(prefix)+len(k))
	out = append(out, prefix...)
	return append(out, k...)
}

var marshal = proto.MarshalOptions{Deterministic: true}

// State aggregates the chain stores over a single revocable database.
type State struct {
	DB        *db.Database
	Accounts  *AccountStore
	Witnesses *WitnessStore
}

// New builds the store set over the given database.
func New(d *db.Database) *State {
	return &State{
		DB:        d,
		Accounts:  &AccountStore{d},
		Witnesses: &WitnessStore{d},
	}
}

// AccountStore persists core.Account keyed by 21-byte address.
type AccountStore struct{ db *db.Database }

// Put stores the account under its own Address.
func (s *AccountStore) Put(a *core.Account) error {
	b, err := marshal.Marshal(a)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(accountPrefix, a.GetAddress()), b)
}

// Get returns the account at addr, or db.ErrNotFound.
func (s *AccountStore) Get(addr []byte) (*core.Account, error) {
	b, err := s.db.Get(nsKey(accountPrefix, addr))
	if err != nil {
		return nil, err
	}
	a := new(core.Account)
	if err := proto.Unmarshal(b, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Has reports whether addr has an account.
func (s *AccountStore) Has(addr []byte) (bool, error) {
	return s.db.Has(nsKey(accountPrefix, addr))
}

// WitnessStore persists core.Witness keyed by 21-byte address.
type WitnessStore struct{ db *db.Database }

// Put stores the witness under its own Address.
func (s *WitnessStore) Put(w *core.Witness) error {
	b, err := marshal.Marshal(w)
	if err != nil {
		return err
	}
	return s.db.Put(nsKey(witnessPrefix, w.GetAddress()), b)
}

// Get returns the witness at addr, or db.ErrNotFound.
func (s *WitnessStore) Get(addr []byte) (*core.Witness, error) {
	b, err := s.db.Get(nsKey(witnessPrefix, addr))
	if err != nil {
		return nil, err
	}
	w := new(core.Witness)
	if err := proto.Unmarshal(b, w); err != nil {
		return nil, err
	}
	return w, nil
}

// Has reports whether addr has a witness.
func (s *WitnessStore) Has(addr []byte) (bool, error) {
	return s.db.Has(nsKey(witnessPrefix, addr))
}
