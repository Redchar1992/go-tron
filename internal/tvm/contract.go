package tvm

import (
	"encoding/binary"

	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// addrPrefix is TRON's mainnet address prefix byte (0x41); every account/contract address
// is 21 bytes = 0x41 ++ 20-byte body.
const addrPrefix = 0x41

// wordToAddr converts a 256-bit stack word to a 21-byte TRON address: the prefix byte
// followed by the word's last 20 bytes (java-tron toTronAddress / getLast20Bytes).
func wordToAddr(w *uint256.Int) []byte {
	b := w.Bytes32()
	addr := make([]byte, 21)
	addr[0] = addrPrefix
	copy(addr[1:], b[12:32])
	return addr
}

// sha3omit12 derives a 21-byte TRON address from a preimage: Keccak-256, take the last 21
// bytes, then force the prefix byte to 0x41 (java-tron Hash.sha3omit12).
func sha3omit12(data ...[]byte) []byte {
	h := crypto.Keccak256(data...)
	addr := make([]byte, 21)
	copy(addr, h[11:32])
	addr[0] = addrPrefix
	return addr
}

// createAddress derives a CREATE contract address: sha3omit12(rootTxID ++ nonce_be8).
// TRON-specific — NOT Ethereum's RLP(sender,nonce). See TransactionUtil.getCreateAddress.
func createAddress(rootTxID []byte, nonce uint64) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], nonce)
	return sha3omit12(rootTxID, n[:])
}

// create2Address derives a CREATE2 address: sha3omit12(sender ++ salt ++ keccak(initCode)).
func create2Address(sender, salt, initCode []byte) []byte {
	return sha3omit12(sender, salt, crypto.Keccak256(initCode))
}

// BlockContext supplies the block-scoped values the environment opcodes read. TRON
// deviations: DIFFICULTY and GASLIMIT always yield 0 (handled in the interpreter).
type BlockContext struct {
	Number    int64
	Timestamp int64
	Coinbase  []byte // 20-byte address
	ChainID   *uint256.Int
}

// Contract is the code-execution frame: the code under execution, its 20-byte address,
// the caller/origin, the call value, and the input (calldata). Storage is reached through
// the EVM's StateDB keyed by Self (so nested frames share one journaled state).
//
// CodeAddr is the address the code was loaded from; for DELEGATECALL/CALLCODE it differs
// from Self (code runs in the caller's storage context). For a normal frame CodeAddr == Self.
type Contract struct {
	Self     []byte // 20-byte storage/context address
	CodeAddr []byte // 20-byte address the code came from (== Self unless DELEGATE/CALLCODE)
	Caller   []byte // 20-byte address that invoked this frame
	Origin   []byte // 20-byte tx origin
	Value    *uint256.Int
	Input    []byte // calldata
	Code     []byte
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
