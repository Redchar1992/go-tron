package block

import (
	"bytes"
	"encoding/binary"
	"testing"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func TestCalcTxTrieRootEmptyIsZeroHash(t *testing.T) {
	root, err := CalcTxTrieRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != HashLen {
		t.Fatalf("root len = %d, want %d", len(root), HashLen)
	}
	if !bytes.Equal(root, make([]byte, HashLen)) {
		t.Fatalf("empty-block txTrieRoot must be 32 zero bytes, got %x", root)
	}
}

func TestIDEncodesNumberPrefix(t *testing.T) {
	raw := &core.BlockHeaderRaw{Number: 1000000, Timestamp: 123}
	id, err := IDFromHeaderRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint64(id[0:8]); got != 1000000 {
		t.Fatalf("id num prefix = %d, want 1000000", got)
	}
	if NumberFromID(id) != 1000000 {
		t.Fatalf("NumberFromID = %d, want 1000000", NumberFromID(id))
	}
}
