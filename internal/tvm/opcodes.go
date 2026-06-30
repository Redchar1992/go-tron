package tvm

// OpCode is a single TVM instruction byte. Byte values are identical to the EVM (TRON's
// TVM inherited the EVM opcode space); see java-tron core/vm/Op.java.
type OpCode byte

// Opcode bytes. Grouped as in the EVM/TVM spec. Only the opcodes implemented in M3.0
// (compute / stack / memory / storage / flow / push-dup-swap / halt) are given names
// here; CALL/CREATE/LOG and other call-frame ops arrive in M3.1+.
const (
	// 0x00 range — stop and arithmetic.
	STOP       OpCode = 0x00
	ADD        OpCode = 0x01
	MUL        OpCode = 0x02
	SUB        OpCode = 0x03
	DIV        OpCode = 0x04
	SDIV       OpCode = 0x05
	MOD        OpCode = 0x06
	SMOD       OpCode = 0x07
	ADDMOD     OpCode = 0x08
	MULMOD     OpCode = 0x09
	EXP        OpCode = 0x0a
	SIGNEXTEND OpCode = 0x0b

	// 0x10 range — comparison and bitwise.
	LT     OpCode = 0x10
	GT     OpCode = 0x11
	SLT    OpCode = 0x12
	SGT    OpCode = 0x13
	EQ     OpCode = 0x14
	ISZERO OpCode = 0x15
	AND    OpCode = 0x16
	OR     OpCode = 0x17
	XOR    OpCode = 0x18
	NOT    OpCode = 0x19
	BYTE   OpCode = 0x1a
	SHL    OpCode = 0x1b
	SHR    OpCode = 0x1c
	SAR    OpCode = 0x1d

	// 0x20 range — KECCAK256 (a.k.a. SHA3).
	KECCAK256 OpCode = 0x20

	// 0x30 range — environmental information.
	ADDRESS        OpCode = 0x30
	BALANCE        OpCode = 0x31
	ORIGIN         OpCode = 0x32
	CALLER         OpCode = 0x33
	CALLVALUE      OpCode = 0x34
	CALLDATALOAD   OpCode = 0x35
	CALLDATASIZE   OpCode = 0x36
	CALLDATACOPY   OpCode = 0x37
	CODESIZE       OpCode = 0x38
	CODECOPY       OpCode = 0x39
	GASPRICE       OpCode = 0x3a
	EXTCODESIZE    OpCode = 0x3b
	EXTCODECOPY    OpCode = 0x3c
	RETURNDATASIZE OpCode = 0x3d
	RETURNDATACOPY OpCode = 0x3e
	EXTCODEHASH    OpCode = 0x3f

	// 0x40 range — block information.
	BLOCKHASH   OpCode = 0x40
	COINBASE    OpCode = 0x41
	TIMESTAMP   OpCode = 0x42
	NUMBER      OpCode = 0x43
	DIFFICULTY  OpCode = 0x44
	GASLIMIT    OpCode = 0x45
	CHAINID     OpCode = 0x46
	SELFBALANCE OpCode = 0x47

	// 0x50 range — stack, memory, storage and flow.
	POP      OpCode = 0x50
	MLOAD    OpCode = 0x51
	MSTORE   OpCode = 0x52
	MSTORE8  OpCode = 0x53
	SLOAD    OpCode = 0x54
	SSTORE   OpCode = 0x55
	JUMP     OpCode = 0x56
	JUMPI    OpCode = 0x57
	PC       OpCode = 0x58
	MSIZE    OpCode = 0x59
	GAS      OpCode = 0x5a
	JUMPDEST OpCode = 0x5b

	// 0x60–0x7f — PUSH1..PUSH32.
	PUSH1  OpCode = 0x60
	PUSH32 OpCode = 0x7f

	// 0x80–0x8f — DUP1..DUP16.
	DUP1  OpCode = 0x80
	DUP16 OpCode = 0x8f

	// 0x90–0x9f — SWAP1..SWAP16.
	SWAP1  OpCode = 0x90
	SWAP16 OpCode = 0x9f

	// 0xd0 range — TRON token (TRC10) operations.
	CALLTOKEN      OpCode = 0xd0
	TOKENBALANCE   OpCode = 0xd1
	CALLTOKENVALUE OpCode = 0xd2
	CALLTOKENID    OpCode = 0xd3
	ISCONTRACT     OpCode = 0xd4

	// 0xf0 range — call frames, create, and halts.
	CREATE       OpCode = 0xf0
	CALL         OpCode = 0xf1
	CALLCODE     OpCode = 0xf2
	RETURN       OpCode = 0xf3
	DELEGATECALL OpCode = 0xf4
	CREATE2      OpCode = 0xf5
	STATICCALL   OpCode = 0xfa
	REVERT       OpCode = 0xfd
	INVALID      OpCode = 0xfe
	SELFDESTRUCT OpCode = 0xff
)

// isPush reports whether op is in the PUSH1..PUSH32 range and, if so, how many immediate
// bytes follow it in the code stream.
func (op OpCode) pushBytes() (n int, ok bool) {
	if op >= PUSH1 && op <= PUSH32 {
		return int(op-PUSH1) + 1, true
	}
	return 0, false
}
