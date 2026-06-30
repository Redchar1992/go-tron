package tvm

import (
	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// StateDB is the cross-contract account state the TVM reads and mutates: balances, code,
// nonces, and per-contract storage, plus snapshot/revert so a failed CALL/CREATE frame
// rolls back exactly the state it touched. Addresses are 20-byte (the TVM DataWord form);
// the node-level adapter maps these to TRON's 21-byte 0x41 account keys.
//
// This is the go-tron analog of java-tron's contract-state view (Program.getContractState
// / Repository). Snapshot()/RevertToSnapshot() mirror the revoking-session rollback the
// Manager already uses at the block level.
type StateDB interface {
	Exist(addr []byte) bool
	CreateAccount(addr []byte)

	GetBalance(addr []byte) *uint256.Int
	AddBalance(addr []byte, amount *uint256.Int)
	SubBalance(addr []byte, amount *uint256.Int)

	GetNonce(addr []byte) uint64
	SetNonce(addr []byte, nonce uint64)

	GetCode(addr []byte) []byte
	SetCode(addr []byte, code []byte)
	GetCodeHash(addr []byte) [32]byte
	GetCodeSize(addr []byte) int

	GetStorage(addr []byte, key [32]byte) (value [32]byte, present bool)
	SetStorage(addr []byte, key [32]byte, value [32]byte)

	Snapshot() int
	RevertToSnapshot(id int)
}

// account is one entry in MemStateDB.
type account struct {
	balance uint256.Int
	nonce   uint64
	code    []byte
	storage map[[32]byte][32]byte
}

func (a *account) clone() *account {
	c := &account{balance: a.balance, nonce: a.nonce}
	if a.code != nil {
		c.code = append([]byte(nil), a.code...)
	}
	c.storage = make(map[[32]byte][32]byte, len(a.storage))
	for k, v := range a.storage {
		c.storage[k] = v
	}
	return c
}

// MemStateDB is an in-memory StateDB for tests and isolated execution. Snapshot/revert is
// implemented by cloning the full account set — simple and correct at test scale; the
// production StateDB will instead ride the db revoking layer.
type MemStateDB struct {
	accounts map[string]*account
	snaps    []map[string]*account
}

// NewMemStateDB returns an empty in-memory state.
func NewMemStateDB() *MemStateDB {
	return &MemStateDB{accounts: make(map[string]*account)}
}

func key(addr []byte) string { return string(addr) }

func (s *MemStateDB) get(addr []byte) *account {
	return s.accounts[key(addr)]
}

func (s *MemStateDB) getOrCreate(addr []byte) *account {
	k := key(addr)
	a := s.accounts[k]
	if a == nil {
		a = &account{storage: make(map[[32]byte][32]byte)}
		s.accounts[k] = a
	}
	return a
}

// Exist reports whether the account is present.
func (s *MemStateDB) Exist(addr []byte) bool { return s.get(addr) != nil }

// CreateAccount ensures an (empty) account exists at addr.
func (s *MemStateDB) CreateAccount(addr []byte) { s.getOrCreate(addr) }

// GetBalance returns a copy of the account balance (zero if absent).
func (s *MemStateDB) GetBalance(addr []byte) *uint256.Int {
	a := s.get(addr)
	if a == nil {
		return new(uint256.Int)
	}
	return a.balance.Clone()
}

// AddBalance credits amount to addr (creating the account if needed).
func (s *MemStateDB) AddBalance(addr []byte, amount *uint256.Int) {
	a := s.getOrCreate(addr)
	a.balance.Add(&a.balance, amount)
}

// SubBalance debits amount from addr.
func (s *MemStateDB) SubBalance(addr []byte, amount *uint256.Int) {
	a := s.getOrCreate(addr)
	a.balance.Sub(&a.balance, amount)
}

// GetNonce returns the account nonce (0 if absent).
func (s *MemStateDB) GetNonce(addr []byte) uint64 {
	if a := s.get(addr); a != nil {
		return a.nonce
	}
	return 0
}

// SetNonce sets the account nonce.
func (s *MemStateDB) SetNonce(addr []byte, nonce uint64) { s.getOrCreate(addr).nonce = nonce }

// GetCode returns the account code (nil if absent).
func (s *MemStateDB) GetCode(addr []byte) []byte {
	if a := s.get(addr); a != nil {
		return a.code
	}
	return nil
}

// SetCode sets the account code.
func (s *MemStateDB) SetCode(addr []byte, code []byte) { s.getOrCreate(addr).code = code }

// GetCodeHash returns the Keccak-256 of the account code, or the empty-code hash. An
// absent account hashes to all-zero (java-tron / EVM EXTCODEHASH-of-nonexistent rule).
func (s *MemStateDB) GetCodeHash(addr []byte) [32]byte {
	a := s.get(addr)
	if a == nil {
		return [32]byte{}
	}
	var h [32]byte
	copy(h[:], crypto.Keccak256(a.code))
	return h
}

// GetCodeSize returns the byte length of the account code.
func (s *MemStateDB) GetCodeSize(addr []byte) int { return len(s.GetCode(addr)) }

// GetStorage returns the value at (addr,key) and whether the slot has ever been set.
func (s *MemStateDB) GetStorage(addr []byte, k [32]byte) ([32]byte, bool) {
	a := s.get(addr)
	if a == nil {
		return [32]byte{}, false
	}
	v, ok := a.storage[k]
	return v, ok
}

// SetStorage writes a storage slot.
func (s *MemStateDB) SetStorage(addr []byte, k [32]byte, value [32]byte) {
	s.getOrCreate(addr).storage[k] = value
}

// Snapshot clones the account set and returns its index.
func (s *MemStateDB) Snapshot() int {
	clone := make(map[string]*account, len(s.accounts))
	for k, a := range s.accounts {
		clone[k] = a.clone()
	}
	s.snaps = append(s.snaps, clone)
	return len(s.snaps) - 1
}

// RevertToSnapshot restores state to snapshot id and discards later snapshots.
func (s *MemStateDB) RevertToSnapshot(id int) {
	if id < 0 || id >= len(s.snaps) {
		return
	}
	s.accounts = s.snaps[id]
	s.snaps = s.snaps[:id]
}
