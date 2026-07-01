package actuator

import (
	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/crypto"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// vmStateDB adapts the node's chain stores (state.State over the revoking db) to the TVM's
// tvm.StateDB interface. It is the M3.5a bridge that lets the finished internal/tvm engine
// execute a real transaction against durable state.
//
// It is a per-transaction working copy in the spirit of java-tron's Deposit/Repository:
// reads fall through to the node stores; writes accumulate in an in-memory dirty layer;
// Snapshot/RevertToSnapshot clone that layer (matching MemStateDB's model) so the VM's
// nested CALL/CREATE frames roll back exactly what they touched; Flush persists the dirty
// layer to the node stores at the end (only when the transaction succeeds — the caller
// decides). Block-level rollback still rides the db revoking session the Manager opened.
//
// Addresses are the 21-byte 0x41 TRON form the interpreter already produces (wordToAddr /
// sha3omit12), which is also the node stores' key form — no 20/21-byte remap is needed.
type vmStateDB struct {
	st    *state.State
	dirty map[string]*vmAccount
	snaps []map[string]*vmAccount
}

// vmAccount is one working-copy account in the dirty layer. Loaded fields (balance, code,
// storage slots) are pulled lazily from the node stores; the *Dirty flags mark what Flush
// must write back.
type vmAccount struct {
	balance    uint256.Int
	nonce      uint64
	code       []byte
	storage    map[[32]byte][32]byte // loaded-clean AND written slots
	loadedSlot map[[32]byte]bool     // slots already looked up (so absence is cached)
	dirtySlot  map[[32]byte]bool     // slots Flush must persist
	balDirty   bool
	codeDirty  bool
	exists     bool
}

func newVMStateDB(st *state.State) *vmStateDB {
	return &vmStateDB{st: st, dirty: make(map[string]*vmAccount)}
}

func (a *vmAccount) clone() *vmAccount {
	c := &vmAccount{
		balance:   a.balance, // uint256.Int is a [4]uint64 value — copied by assignment
		nonce:     a.nonce,
		balDirty:  a.balDirty,
		codeDirty: a.codeDirty,
		exists:    a.exists,
	}
	if a.code != nil {
		c.code = append([]byte(nil), a.code...)
	}
	c.storage = make(map[[32]byte][32]byte, len(a.storage))
	for k, v := range a.storage {
		c.storage[k] = v
	}
	c.loadedSlot = make(map[[32]byte]bool, len(a.loadedSlot))
	for k := range a.loadedSlot {
		c.loadedSlot[k] = true
	}
	c.dirtySlot = make(map[[32]byte]bool, len(a.dirtySlot))
	for k := range a.dirtySlot {
		c.dirtySlot[k] = true
	}
	return c
}

// load returns the working account for addr, pulling balance (AccountStore) and code
// (ContractStore) from the node stores on first touch. Storage is loaded per-slot lazily.
func (s *vmStateDB) load(addr []byte) *vmAccount {
	k := string(addr)
	if a := s.dirty[k]; a != nil {
		return a
	}
	a := &vmAccount{
		storage:    make(map[[32]byte][32]byte),
		loadedSlot: make(map[[32]byte]bool),
		dirtySlot:  make(map[[32]byte]bool),
	}
	if acct, err := s.st.Accounts.Get(addr); err == nil {
		a.balance.SetUint64(uint64(acct.GetBalance()))
		a.exists = true
	}
	if code, err := s.st.Contracts.GetCode(addr); err == nil {
		a.code = code
		a.exists = true
	}
	s.dirty[k] = a
	return a
}

// Exist reports whether the account exists in the working copy or node stores.
func (s *vmStateDB) Exist(addr []byte) bool { return s.load(addr).exists }

// CreateAccount marks an (empty) account present at addr.
func (s *vmStateDB) CreateAccount(addr []byte) {
	a := s.load(addr)
	a.exists = true
	a.balDirty = true // ensure a record is materialized on Flush
}

// GetBalance returns a copy of the account balance (zero if absent).
func (s *vmStateDB) GetBalance(addr []byte) *uint256.Int {
	return s.load(addr).balance.Clone()
}

// AddBalance credits amount to addr.
func (s *vmStateDB) AddBalance(addr []byte, amount *uint256.Int) {
	a := s.load(addr)
	a.balance.Add(&a.balance, amount)
	a.balDirty = true
	a.exists = true
}

// SubBalance debits amount from addr.
func (s *vmStateDB) SubBalance(addr []byte, amount *uint256.Int) {
	a := s.load(addr)
	a.balance.Sub(&a.balance, amount)
	a.balDirty = true
	a.exists = true
}

// GetNonce returns the account nonce. Note: TRON accounts carry no persisted EVM nonce, so
// the nonce lives only in the per-tx working copy (sufficient for in-tx CREATE derivation).
func (s *vmStateDB) GetNonce(addr []byte) uint64 { return s.load(addr).nonce }

// SetNonce sets the (in-memory, non-persisted) account nonce.
func (s *vmStateDB) SetNonce(addr []byte, nonce uint64) { s.load(addr).nonce = nonce }

// GetCode returns the account code (nil if absent).
func (s *vmStateDB) GetCode(addr []byte) []byte { return s.load(addr).code }

// SetCode sets the account code and marks it for persistence.
func (s *vmStateDB) SetCode(addr []byte, code []byte) {
	a := s.load(addr)
	a.code = code
	a.codeDirty = true
	a.exists = true
}

// GetCodeHash returns Keccak-256 of the code, or all-zero for an absent account.
func (s *vmStateDB) GetCodeHash(addr []byte) [32]byte {
	a := s.load(addr)
	if !a.exists {
		return [32]byte{}
	}
	var h [32]byte
	copy(h[:], crypto.Keccak256(a.code))
	return h
}

// GetCodeSize returns the byte length of the account code.
func (s *vmStateDB) GetCodeSize(addr []byte) int { return len(s.load(addr).code) }

// GetStorage returns the value at (addr, key) and whether the slot has ever been written,
// falling through to StorageStore on first lookup.
func (s *vmStateDB) GetStorage(addr []byte, key [32]byte) ([32]byte, bool) {
	a := s.load(addr)
	if v, ok := a.storage[key]; ok {
		return v, true
	}
	if a.loadedSlot[key] {
		return [32]byte{}, false
	}
	a.loadedSlot[key] = true
	v, present, err := s.st.Storage.Get(addr, key)
	if err != nil || !present {
		return [32]byte{}, false
	}
	a.storage[key] = v
	return v, true
}

// SetStorage writes a storage slot into the working copy and marks it for persistence.
func (s *vmStateDB) SetStorage(addr []byte, key [32]byte, value [32]byte) {
	a := s.load(addr)
	a.storage[key] = value
	a.loadedSlot[key] = true
	a.dirtySlot[key] = true
	a.exists = true
}

// Snapshot clones the dirty layer and returns its index (matches MemStateDB semantics).
func (s *vmStateDB) Snapshot() int {
	clone := make(map[string]*vmAccount, len(s.dirty))
	for k, a := range s.dirty {
		clone[k] = a.clone()
	}
	s.snaps = append(s.snaps, clone)
	return len(s.snaps) - 1
}

// RevertToSnapshot restores the dirty layer to snapshot id and discards later snapshots.
func (s *vmStateDB) RevertToSnapshot(id int) {
	if id < 0 || id >= len(s.snaps) {
		return
	}
	s.dirty = s.snaps[id]
	s.snaps = s.snaps[:id]
}

// Flush persists the dirty layer to the node stores. It must be called inside the block's
// open revoking session so the writes commit/revoke with the block. Balances are written
// read-modify-write to preserve other Account fields; only dirty slots and changed code
// are written.
func (s *vmStateDB) Flush() error {
	for k, a := range s.dirty {
		addr := []byte(k)
		if a.balDirty {
			acct, err := s.st.Accounts.Get(addr)
			if err != nil {
				acct = &core.Account{Address: addr, Type: core.AccountType_Normal}
			}
			if len(a.code) > 0 {
				acct.Type = core.AccountType_Contract
			}
			acct.Balance = int64(a.balance.Uint64())
			if err := s.st.Accounts.Put(acct); err != nil {
				return err
			}
		}
		if a.codeDirty {
			if err := s.st.Contracts.PutCode(addr, a.code); err != nil {
				return err
			}
		}
		for slot := range a.dirtySlot {
			if err := s.st.Storage.Put(addr, slot, a.storage[slot]); err != nil {
				return err
			}
		}
	}
	return nil
}
