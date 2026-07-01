package state

import (
	"github.com/Redchar1992/go-tron/internal/db"
)

var (
	codePrefix    = []byte("c/")
	storagePrefix = []byte("s/")
)

// ContractStore persists deployed contract runtime code, keyed by 21-byte 0x41 address.
// This is the go-tron analog of java-tron's CodeStore. The SmartContract metadata (ABI,
// origin, consume_user_resource_percent, ...) is a separate concern deferred to a later
// M3.5 slice; M3.5a stores only the runtime code the TVM needs to execute.
type ContractStore struct{ db *db.Database }

// PutCode stores runtime code at addr.
func (s *ContractStore) PutCode(addr, code []byte) error {
	return s.db.Put(nsKey(codePrefix, addr), code)
}

// GetCode returns the runtime code at addr, or db.ErrNotFound.
func (s *ContractStore) GetCode(addr []byte) ([]byte, error) {
	return s.db.Get(nsKey(codePrefix, addr))
}

// Has reports whether addr has code.
func (s *ContractStore) Has(addr []byte) (bool, error) {
	return s.db.Has(nsKey(codePrefix, addr))
}

// StorageStore persists contract storage slots, keyed by 21-byte 0x41 address ++ 32-byte
// slot -> 32-byte word (java-tron StorageRowStore analog). Both key halves are fixed-width,
// so the concatenation is unambiguous.
type StorageStore struct{ db *db.Database }

func storageRowKey(addr []byte, slot [32]byte) []byte {
	k := make([]byte, 0, len(addr)+32)
	k = append(k, addr...)
	return append(k, slot[:]...)
}

// Put writes a storage slot value.
func (s *StorageStore) Put(addr []byte, slot [32]byte, value [32]byte) error {
	return s.db.Put(nsKey(storagePrefix, storageRowKey(addr, slot)), value[:])
}

// Get returns the value at (addr, slot) and whether the slot has ever been written.
func (s *StorageStore) Get(addr []byte, slot [32]byte) (value [32]byte, present bool, err error) {
	b, err := s.db.Get(nsKey(storagePrefix, storageRowKey(addr, slot)))
	if err == db.ErrNotFound {
		return [32]byte{}, false, nil
	}
	if err != nil {
		return [32]byte{}, false, err
	}
	copy(value[:], b)
	return value, true, nil
}
