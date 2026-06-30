package address

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// Bitcoin/TRON base58 alphabet.
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var bigRadix = big.NewInt(58)

// b58Encode encodes bytes to a base58 string (no checksum).
func b58Encode(input []byte) string {
	var zeros int
	for zeros < len(input) && input[zeros] == 0 {
		zeros++
	}
	num := new(big.Int).SetBytes(input)
	mod := new(big.Int)
	var out []byte
	for num.Sign() > 0 {
		num.DivMod(num, bigRadix, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, b58Alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// b58Decode decodes a base58 string (no checksum).
func b58Decode(s string) ([]byte, error) {
	num := new(big.Int)
	for _, r := range s {
		idx := strings.IndexRune(b58Alphabet, r)
		if idx < 0 {
			return nil, fmt.Errorf("address: invalid base58 character %q", r)
		}
		num.Mul(num, bigRadix)
		num.Add(num, big.NewInt(int64(idx)))
	}
	dec := num.Bytes()
	var zeros int
	for zeros < len(s) && s[zeros] == b58Alphabet[0] {
		zeros++
	}
	return append(make([]byte, zeros), dec...), nil
}

// checksum returns the first 4 bytes of sha256(sha256(payload)).
func checksum(payload []byte) []byte {
	return crypto.Sha256(crypto.Sha256(payload))[:4]
}

// b58CheckEncode appends a 4-byte double-sha256 checksum and base58-encodes.
func b58CheckEncode(payload []byte) string {
	return b58Encode(append(append([]byte(nil), payload...), checksum(payload)...))
}

// b58CheckDecode base58-decodes and verifies the trailing 4-byte checksum.
func b58CheckDecode(s string) ([]byte, error) {
	dec, err := b58Decode(s)
	if err != nil {
		return nil, err
	}
	if len(dec) < 4 {
		return nil, errors.New("address: base58 payload too short")
	}
	payload, sum := dec[:len(dec)-4], dec[len(dec)-4:]
	if !bytes.Equal(checksum(payload), sum) {
		return nil, errors.New("address: bad base58check checksum")
	}
	return payload, nil
}
