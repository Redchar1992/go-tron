package tvm

// Memory is the TVM's byte-addressable, lazily-zero-filled execution memory. It grows in
// 32-byte words; reads/writes past the current end first expand (zero-filled) to cover
// the access. Mirrors java-tron core/vm/program/Memory. The ENERGY for expansion is
// charged by the interpreter (see memoryEnergyCost); this type only models the bytes.
type Memory struct {
	store []byte
}

func newMemory() *Memory { return &Memory{} }

// Len returns the current memory size in bytes (always a multiple of 32).
func (m *Memory) Len() int { return len(m.store) }

// words returns the current size in 32-byte words.
func (m *Memory) words() uint64 { return uint64(len(m.store) / 32) }

// wordsForBytes returns the number of 32-byte words needed to hold n bytes.
func wordsForBytes(n uint64) uint64 { return (n + 31) / 32 }

// resize grows memory so it can hold at least `size` bytes, rounded up to a word
// boundary. Never shrinks. New bytes are zero.
func (m *Memory) resize(size uint64) {
	need := wordsForBytes(size) * 32
	if uint64(len(m.store)) >= need {
		return
	}
	m.store = append(m.store, make([]byte, need-uint64(len(m.store)))...)
}

// set writes value at offset, expanding memory if needed.
func (m *Memory) set(offset uint64, value []byte) {
	if len(value) == 0 {
		return
	}
	m.resize(offset + uint64(len(value)))
	copy(m.store[offset:], value)
}

// set32 writes a 32-byte word at offset, expanding if needed.
func (m *Memory) set32(offset uint64, value [32]byte) {
	m.resize(offset + 32)
	copy(m.store[offset:offset+32], value[:])
}

// get returns a copy of `size` bytes starting at offset, zero-filling past the end
// (and expanding memory to cover the read, matching java-tron semantics).
func (m *Memory) get(offset, size uint64) []byte {
	if size == 0 {
		return nil
	}
	m.resize(offset + size)
	out := make([]byte, size)
	copy(out, m.store[offset:offset+size])
	return out
}
