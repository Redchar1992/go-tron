package address

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

const (
	// Prefix is the TRON mainnet address prefix byte (0x41).
	Prefix byte = 0x41
	// Length is the byte length of a canonical TRON address (prefix + 20).
	Length = 21
	// HashLength is the length of the 20-byte ABI form.
	HashLength = 20
)

// Address is a canonical 21-byte TRON address (0x41 || 20 bytes).
type Address [Length]byte

// FromBytes validates a 21-byte canonical address.
func FromBytes(b []byte) (Address, error) {
	var a Address
	if len(b) != Length {
		return a, fmt.Errorf("address: want %d bytes, got %d", Length, len(b))
	}
	if b[0] != Prefix {
		return a, fmt.Errorf("address: want prefix 0x%02x, got 0x%02x", Prefix, b[0])
	}
	copy(a[:], b)
	return a, nil
}

// FromHash builds an address from a 20-byte ABI-form hash by prepending the 0x41 prefix.
func FromHash(h []byte) (Address, error) {
	var a Address
	if len(h) != HashLength {
		return a, fmt.Errorf("address: want %d-byte hash, got %d", HashLength, len(h))
	}
	a[0] = Prefix
	copy(a[1:], h)
	return a, nil
}

// FromPublicKey derives an address from an secp256k1 public key. It accepts either the
// 65-byte uncompressed form (0x04 || X || Y) or the bare 64-byte X || Y form. The address
// is 0x41 || last-20-bytes(Keccak256(X || Y)).
func FromPublicKey(pub []byte) (Address, error) {
	var a Address
	switch len(pub) {
	case 65:
		if pub[0] != 0x04 {
			return a, errors.New("address: 65-byte pubkey must start with 0x04 (uncompressed)")
		}
		pub = pub[1:]
	case 64:
		// bare X||Y
	default:
		return a, fmt.Errorf("address: want 64- or 65-byte pubkey, got %d", len(pub))
	}
	h := crypto.Keccak256(pub)
	return FromHash(h[len(h)-HashLength:])
}

// FromBase58 decodes a Base58Check ("T...") address and validates it.
func FromBase58(s string) (Address, error) {
	payload, err := b58CheckDecode(s)
	if err != nil {
		return Address{}, err
	}
	return FromBytes(payload)
}

// Bytes returns the 21-byte canonical form.
func (a Address) Bytes() []byte { return append([]byte(nil), a[:]...) }

// Hash returns the 20-byte ABI form (prefix stripped).
func (a Address) Hash() []byte { return append([]byte(nil), a[1:]...) }

// Hex returns the lowercase hex of the 21-byte form (e.g. "41...").
func (a Address) Hex() string { return hex.EncodeToString(a[:]) }

// Base58 returns the Base58Check ("T...") form.
func (a Address) Base58() string { return b58CheckEncode(a[:]) }

// String returns the Base58Check form.
func (a Address) String() string { return a.Base58() }
