// Package crypto provides the hash and signature primitives TRON uses.
//
// We implement the minimal set on top of lightweight deps (golang.org/x/crypto/sha3 for
// Keccak-256) rather than pulling all of go-ethereum. secp256k1 sign/recover (via
// btcec/v2) will be added when transaction signing lands (actuator path).
// CONSENSUS-CRITICAL: hashing/recovery must match java-tron exactly.
package crypto
