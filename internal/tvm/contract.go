package tvm

import "github.com/holiman/uint256"

// Storage is the per-contract 32-byte key/value store the TVM reads with SLOAD and
// writes with SSTORE. Load reports `present` so SSTORE can distinguish a never-set slot
// (java-tron storageLoad == null) from a slot holding zero — the two carry different
// energy costs (SET_SSTORE vs CLEAR/RESET).
type Storage interface {
	Load(key [32]byte) (value [32]byte, present bool)
	Store(key [32]byte, value [32]byte)
}

// MemStorage is an in-memory Storage for tests and isolated execution.
type MemStorage struct {
	m map[[32]byte][32]byte
}

// NewMemStorage returns an empty in-memory storage.
func NewMemStorage() *MemStorage { return &MemStorage{m: make(map[[32]byte][32]byte)} }

// Load implements Storage.
func (s *MemStorage) Load(key [32]byte) ([32]byte, bool) {
	v, ok := s.m[key]
	return v, ok
}

// Store implements Storage.
func (s *MemStorage) Store(key, value [32]byte) { s.m[key] = value }

// BlockContext supplies the block-scoped values the environment opcodes read. TRON
// deviations: DIFFICULTY and GASLIMIT always yield 0 (handled in the interpreter).
type BlockContext struct {
	Number    int64
	Timestamp int64
	Coinbase  []byte // 20-byte address
	ChainID   *uint256.Int
}

// Contract is the code-execution frame: the code under execution, its 20-byte address,
// the caller/origin, the call value, and the input (calldata), plus the contract's
// storage. M3.0 executes a single frame — no nested CALL/CREATE (that is M3.1).
type Contract struct {
	Self    []byte // 20-byte address of the executing contract
	Caller  []byte // 20-byte address that invoked this frame
	Origin  []byte // 20-byte tx origin
	Value   *uint256.Int
	Input   []byte // calldata
	Code    []byte
	Storage Storage
}

// Result is the outcome of executing a frame.
type Result struct {
	Return     []byte // RETURN/REVERT payload (nil otherwise)
	Reverted   bool   // true if the frame ended in REVERT
	EnergyUsed uint64 // energy consumed (== limit on a VM exception)
	Err        error  // non-nil on a VM exception (out-of-energy, bad jump, stack, invalid op)
}

// addrWord right-aligns a 20-byte address into a 256-bit word (the TVM DataWord form).
func addrWord(addr []byte) uint256.Int {
	var w uint256.Int
	w.SetBytes(addr)
	return w
}
