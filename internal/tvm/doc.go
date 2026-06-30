// Package tvm is the TRON Virtual Machine: interpreter, energy meter, precompiles,
// TRC10-in-call rules, and hardfork/TIP-gated behaviors (e.g. ModExp canonicalization,
// historical block hashes, CREATE2 depth under Osaka).
//
// HIGHEST-RISK package. Strategy: faithfully port java-tron's TVM (not reinvent), gate
// every hardfork behavior by flag, and fuzz against java-tron as an oracle.
//
// Status: M3.0 + M3.1.
//
// M3.0 — EVM-core interpreter + energy meter: 256-bit operand stack (holiman/uint256),
// expandable memory with quadratic expansion energy, the energy meter, and the compute
// opcode set (arithmetic, comparison, bitwise, KECCAK256, context-free environment,
// SLOAD/SSTORE, JUMP/JUMPI/JUMPDEST, PUSH/DUP/SWAP, RETURN/REVERT). Energy costs are
// byte-faithful to java-tron's EnergyCost.java (tiers 0/1/2/3/5/8/10, SSTORE 20000/5000,
// SLOAD 50, SHA3 30+6/word, EXP 10+10/byte, memory f(w)=3w+w^2/512).
//
// M3.1 — call frames + CREATE: a StateDB account model (balance/code/nonce/storage +
// snapshot/revert), the EVM cross-frame runner, the CALL family (CALL/CALLCODE/
// DELEGATECALL/STATICCALL), CREATE/CREATE2 with TRON's sha3omit12 address derivation
// (0x41-prefixed), the 64-deep call limit, gated 63/64 energy forwarding, the 2300
// value-transfer stipend, value transfer with snapshot rollback on failure, the
// returndata buffer + RETURNDATACOPY, account-access ops (BALANCE/EXTCODESIZE/
// EXTCODECOPY/EXTCODEHASH/SELFBALANCE), STATICCALL write-protection, and read-side TRC10
// token opcode plumbing.
//
// NOT yet implemented: TRC10 token transfer mechanics + precompiles (M3.2), full
// energy/resource accounting vs receipts (M3.3), hardfork/TIP gates (M3.4), differential
// VM replay of contract-bearing mainnet blocks + fuzzing (M3.5). M3.1 is verified by
// vector tests (no real-block oracle yet — that needs historical contract state, M3.5).
package tvm
