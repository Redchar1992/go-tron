// Package tvm is the TRON Virtual Machine: interpreter, energy meter, precompiles,
// TRC10-in-call rules, and hardfork/TIP-gated behaviors (e.g. ModExp canonicalization,
// historical block hashes, CREATE2 depth under Osaka).
//
// HIGHEST-RISK package. Strategy: faithfully port java-tron's TVM (not reinvent), gate
// every hardfork behavior by flag, and fuzz against java-tron as an oracle.
//
// Status: M3.0 — EVM-core interpreter + energy meter. Implemented: 256-bit operand stack
// (holiman/uint256), expandable memory with quadratic expansion energy, the energy meter,
// and the compute opcode set — arithmetic, comparison, bitwise, KECCAK256, the
// context-free environment ops, SLOAD/SSTORE over a Storage interface, JUMP/JUMPI/
// JUMPDEST, PUSH/DUP/SWAP, RETURN/REVERT. Energy costs are byte-faithful to java-tron's
// EnergyCost.java (tiers 0/1/2/3/5/8/10, SSTORE 20000/5000, SLOAD 50, SHA3 30+6/word,
// EXP 10+10/byte, memory f(w)=3w+w^2/512). NOT yet implemented (later sub-milestones):
// CALL/CREATE frames + TRC10-in-call (M3.1), precompiles (M3.2), full energy/resource
// accounting (M3.3), hardfork/TIP gates (M3.4), differential VM replay + fuzzing (M3.5).
package tvm
