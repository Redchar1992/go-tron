package crypto

import (
	"crypto/sha256"

	"golang.org/x/crypto/sha3"
)

// Keccak256 returns the Keccak-256 (legacy, pre-NIST-SHA3) digest of the concatenated
// inputs — the hash function used by TRON/Ethereum for address derivation, event topics,
// and TVM. Note this is NOT the same as SHA3-256.
func Keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

// Sha256 returns the SHA-256 digest of the concatenated inputs.
func Sha256(data ...[]byte) []byte {
	h := sha256.New()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}
