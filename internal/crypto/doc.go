// Package crypto provides secp256k1 sign/recover and the hash primitives TRON uses.
//
// Plan: reuse gotron-sdk's client-grade crypto (pinned). CONSENSUS-CRITICAL: signature
// recovery and hashing must match java-tron exactly. M0: placeholder.
package crypto
