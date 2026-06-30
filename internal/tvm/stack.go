package tvm

import (
	"errors"

	"github.com/holiman/uint256"
)

// MaxStack is the EVM/TVM operand-stack depth limit (java-tron Program: 1024).
const MaxStack = 1024

var (
	// ErrStackUnderflow is returned when an opcode pops more items than are present.
	ErrStackUnderflow = errors.New("tvm: stack underflow")
	// ErrStackOverflow is returned when a push would exceed MaxStack.
	ErrStackOverflow = errors.New("tvm: stack overflow")
)

// Stack is the TVM operand stack of 256-bit words. The top of the stack is the last
// element. Mirrors java-tron core/vm/program/Stack (a Deque of DataWord) with the same
// 1024-item limit and underflow/overflow checks.
type Stack struct {
	data []uint256.Int
}

func newStack() *Stack { return &Stack{data: make([]uint256.Int, 0, 16)} }

// Len returns the number of items on the stack.
func (s *Stack) Len() int { return len(s.data) }

// push appends v (by value) to the top of the stack.
func (s *Stack) push(v uint256.Int) error {
	if len(s.data) >= MaxStack {
		return ErrStackOverflow
	}
	s.data = append(s.data, v)
	return nil
}

// pop removes and returns the top item.
func (s *Stack) pop() (uint256.Int, error) {
	n := len(s.data)
	if n == 0 {
		return uint256.Int{}, ErrStackUnderflow
	}
	v := s.data[n-1]
	s.data = s.data[:n-1]
	return v, nil
}

// peek returns a pointer to the item n positions below the top (0 = top) for in-place
// ops. Callers must ensure require(n+1) was checked first.
func (s *Stack) peek(n int) *uint256.Int {
	return &s.data[len(s.data)-1-n]
}

// require verifies the stack holds at least n items (underflow guard) and that pushing
// `push` more after popping `n` would not overflow.
func (s *Stack) require(pop, push int) error {
	if len(s.data) < pop {
		return ErrStackUnderflow
	}
	if len(s.data)-pop+push > MaxStack {
		return ErrStackOverflow
	}
	return nil
}

// dup duplicates the item n-from-top (DUPn: n in 1..16) onto the top.
func (s *Stack) dup(n int) error {
	if len(s.data) < n {
		return ErrStackUnderflow
	}
	if len(s.data) >= MaxStack {
		return ErrStackOverflow
	}
	s.data = append(s.data, s.data[len(s.data)-n])
	return nil
}

// swap exchanges the top item with the item n-from-top (SWAPn: n in 1..16).
func (s *Stack) swap(n int) error {
	if len(s.data) < n+1 {
		return ErrStackUnderflow
	}
	top := len(s.data) - 1
	s.data[top], s.data[top-n] = s.data[top-n], s.data[top]
	return nil
}
