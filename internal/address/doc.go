// Package address handles TRON address encodings and derivation:
//   - the 21-byte canonical form: a 0x41 prefix byte followed by 20 bytes;
//   - Base58Check ("T..."), the user-facing form;
//   - the 20-byte ABI form (the 21-byte value with the 0x41 prefix stripped).
//
// Implemented thin (Keccak-256 via internal/crypto + an inline base58check) rather than
// vendoring go-ethereum. CONSENSUS-CRITICAL: derivation and encoding must match java-tron.
package address
