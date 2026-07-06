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
// M3.2 — precompiles: a precompile registry + CALL dispatch (run natively, charge the
// precompile's energy, fail-to-0 on error). Implemented and vector-tested: ecrecover
// (0x01, secp256k1 recovery -> 20-byte Ethereum address), sha256 (0x02), TRON's 0x03
// (sha256(sha256(x)[:20]) — a deliberate deviation; the real RIPEMD-160 is at 0x20003),
// identity (0x04), modexp (0x05, with TRON's zero-modulus -> empty rule and the
// multComplexity/adjExpLen/20 energy formula). Energy is byte-faithful to
// PrecompiledContracts.java.
//
// M3.4 — hardfork/TIP gates: VMConfig carries the allowTvm* flags, each opcode introduced
// by a fork has an `enabled(VMConfig)` predicate, and dispatch faults (invalid opcode) on
// an op whose fork is not active. Gated: SHL/SHR/SAR + CREATE2 + EXTCODEHASH
// (Constantinople), ISCONTRACT (Solidity059), CHAINID + SELFBALANCE (Istanbul), the TRC10
// read ops (TransferTrc10). Presets: LatestVMConfig (all on) / ConstantinopleVMConfig /
// the zero value (pre-everything). Mapping committee-proposal state -> these flags is the
// node's job (M3.5/integration).
//
// Implemented in M3.5d: bn128 add/mul/pairing (0x06-0x08, incl. the Istanbul re-pricing
// 500/40000 -> 150/6000) + blake2F (0x20009) via gnark-crypto; TRON batchvalidatesign /
// validatemultisign (0x09/0x0a, see multisig.go); the shielded-TRC-20 precompile addresses
// (0x1000001..4) wired behind their default-off gate (see shielded.go — the zk proof
// verification itself is a deferred Sapling/Groth16 milestone, unreachable while the gate is
// off); and the staking/voting opcodes (0xd5..0xdf) wired behind their default-off proposal
// gates (see staking_ops.go — the Stake2.0 write-side + reward/vote state machine is a
// deferred milestone, so enabled execution fails closed). NOT yet implemented: TRC10
// token-transfer mechanics. Remaining milestone: differential VM replay + fuzzing (M3.5e).
// M3.0-M3.4 are verified by vector tests; the real-block energy oracle is M3.5.
package tvm
