package address

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestFromHashRoundTrip(t *testing.T) {
	h := make([]byte, HashLength)
	for i := range h {
		h[i] = byte(i + 1)
	}
	a, err := FromHash(h)
	if err != nil {
		t.Fatal(err)
	}
	if a[0] != Prefix {
		t.Fatalf("prefix = 0x%02x, want 0x41", a[0])
	}
	if !bytes.Equal(a.Hash(), h) {
		t.Fatalf("Hash() = %x, want %x", a.Hash(), h)
	}
	// canonical bytes -> base58 -> back
	got, err := FromBase58(a.Base58())
	if err != nil {
		t.Fatalf("FromBase58: %v", err)
	}
	if got != a {
		t.Fatalf("base58 round-trip mismatch: %x vs %x", got, a)
	}
}

// TestZeroAddressVector is an independent check: the canonical TRON "black-hole"
// address (0x41 followed by 20 zero bytes) is the well-known T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb.
func TestZeroAddressVector(t *testing.T) {
	const zeroB58 = "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"
	a, err := FromBase58(zeroB58)
	if err != nil {
		t.Fatalf("decode zero address: %v", err)
	}
	want, _ := hex.DecodeString("410000000000000000000000000000000000000000")
	if !bytes.Equal(a.Bytes(), want) {
		t.Fatalf("zero address = %s, want %x", a.Hex(), want)
	}
	if a.Base58() != zeroB58 {
		t.Fatalf("re-encode = %s, want %s", a.Base58(), zeroB58)
	}
}

func TestFromPublicKeyShapes(t *testing.T) {
	xy := make([]byte, 64) // bare X||Y
	a1, err := FromPublicKey(xy)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed := append([]byte{0x04}, xy...) // 0x04||X||Y
	a2, err := FromPublicKey(uncompressed)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatal("64-byte and 65-byte forms of same key produced different addresses")
	}
	if a1[0] != Prefix {
		t.Fatalf("prefix = 0x%02x, want 0x41", a1[0])
	}

	for _, bad := range [][]byte{make([]byte, 33), make([]byte, 65)} {
		if len(bad) == 65 {
			bad[0] = 0x02 // wrong leading byte
		}
		if _, err := FromPublicKey(bad); err == nil {
			t.Errorf("FromPublicKey(len=%d) want error, got nil", len(bad))
		}
	}
}

func TestFromBytesValidation(t *testing.T) {
	if _, err := FromBytes(make([]byte, 20)); err == nil {
		t.Error("want length error")
	}
	bad := make([]byte, Length)
	bad[0] = 0x42
	if _, err := FromBytes(bad); err == nil {
		t.Error("want prefix error")
	}
}

func TestBadChecksumRejected(t *testing.T) {
	// flip the last character of a valid address
	good := "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"
	bad := good[:len(good)-1] + "c"
	if _, err := FromBase58(bad); err == nil {
		t.Error("want checksum error for corrupted address")
	}
}
