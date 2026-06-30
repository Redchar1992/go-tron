package merkle

import (
	"bytes"
	"testing"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

func TestRootEmpty(t *testing.T) {
	if Root(nil) != nil {
		t.Fatal("Root(nil) should be nil")
	}
}

func TestRootSingleIsIdentity(t *testing.T) {
	h := crypto.Sha256([]byte("only"))
	if !bytes.Equal(Root([][]byte{h}), h) {
		t.Fatal("single-leaf root must equal the leaf")
	}
}

func TestRootTwo(t *testing.T) {
	a, b := crypto.Sha256([]byte("a")), crypto.Sha256([]byte("b"))
	want := crypto.Sha256(a, b)
	if !bytes.Equal(Root([][]byte{a, b}), want) {
		t.Fatal("two-leaf root mismatch")
	}
}

func TestRootThreeOddPromote(t *testing.T) {
	a, b, c := crypto.Sha256([]byte("a")), crypto.Sha256([]byte("b")), crypto.Sha256([]byte("c"))
	// level1: [SHA(a||b), c(promoted)]; root: SHA(SHA(a||b) || c)
	want := crypto.Sha256(crypto.Sha256(a, b), c)
	if !bytes.Equal(Root([][]byte{a, b, c}), want) {
		t.Fatal("three-leaf odd-promote root mismatch")
	}
}

func TestRootFour(t *testing.T) {
	h := make([][]byte, 4)
	for i := range h {
		h[i] = crypto.Sha256([]byte{byte(i)})
	}
	want := crypto.Sha256(crypto.Sha256(h[0], h[1]), crypto.Sha256(h[2], h[3]))
	if !bytes.Equal(Root(h), want) {
		t.Fatal("four-leaf root mismatch")
	}
}

func TestRootFiveOddPromoteAcrossLevels(t *testing.T) {
	h := make([][]byte, 5)
	for i := range h {
		h[i] = crypto.Sha256([]byte{byte(i)})
	}
	// level1: [01, 23, 4]; level2: [SHA(01||23), 4]; root: SHA(SHA(01||23) || 4)
	l01 := crypto.Sha256(h[0], h[1])
	l23 := crypto.Sha256(h[2], h[3])
	want := crypto.Sha256(crypto.Sha256(l01, l23), h[4])
	if !bytes.Equal(Root(h), want) {
		t.Fatal("five-leaf multi-level odd-promote mismatch")
	}
}
