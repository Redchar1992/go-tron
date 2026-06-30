// Package tvm is the TRON Virtual Machine: interpreter, energy meter, precompiles,
// TRC10-in-call rules, and hardfork/TIP-gated behaviors (e.g. ModExp canonicalization,
// historical block hashes, CREATE2 depth under Osaka).
//
// HIGHEST-RISK package. Strategy: faithfully port java-tron's TVM (not reinvent), gate
// every hardfork behavior by flag, and fuzz against java-tron as an oracle. Target: M3.
// M0: placeholder.
package tvm
