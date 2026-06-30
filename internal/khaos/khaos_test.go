package khaos

import (
	"bytes"
	"testing"

	"github.com/Redchar1992/go-tron/internal/block"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// mkBlock builds a minimal block with the given number and parent id. A distinct
// timestamp lets sibling blocks at the same height get distinct ids (fork branches).
func mkBlock(num int64, parent []byte, ts int64) *core.Block {
	return &core.Block{
		BlockHeader: &core.BlockHeader{
			RawData: &core.BlockHeaderRaw{
				Number:     num,
				ParentHash: parent,
				Timestamp:  ts,
			},
		},
	}
}

func id(t *testing.T, b *core.Block) []byte {
	t.Helper()
	h, err := block.ID(b)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLinearExtension(t *testing.T) {
	k := New(0)
	g := mkBlock(0, make([]byte, 32), 0)
	if err := k.Start(g); err != nil {
		t.Fatal(err)
	}
	prev := id(t, g)
	for n := int64(1); n <= 5; n++ {
		b := mkBlock(n, prev, n*1000)
		node, err := k.Push(b)
		if err != nil {
			t.Fatalf("push %d: %v", n, err)
		}
		prev = node.ID
	}
	if k.Head().Num != 5 {
		t.Fatalf("head num = %d, want 5", k.Head().Num)
	}
}

func TestUnlinkedRejected(t *testing.T) {
	k := New(0)
	k.Start(mkBlock(0, make([]byte, 32), 0))
	orphan := mkBlock(2, bytes.Repeat([]byte{0xAA}, 32), 1)
	if _, err := k.Push(orphan); err != ErrUnlinked {
		t.Fatalf("err = %v, want ErrUnlinked", err)
	}
}

// TestForkBranch builds a fork and checks GetBranch returns the two divergent sides
// back to the common ancestor, and that the head only switches on a strictly longer chain.
func TestForkBranch(t *testing.T) {
	k := New(0)
	g := mkBlock(0, make([]byte, 32), 0)
	k.Start(g)

	// main: 0 <- 1a <- 2a
	b1a := mkBlock(1, id(t, g), 1001)
	n1a, _ := k.Push(b1a)
	b2a := mkBlock(2, n1a.ID, 2001)
	n2a, _ := k.Push(b2a)
	if !bytes.Equal(k.Head().ID, n2a.ID) {
		t.Fatal("head should be 2a")
	}

	// side: 0 <- 1b (shorter; head must NOT switch)
	b1b := mkBlock(1, id(t, g), 1002)
	n1b, _ := k.Push(b1b)
	if !bytes.Equal(k.Head().ID, n2a.ID) {
		t.Fatal("head must stay 2a for a shorter side branch")
	}

	// extend side past main: 0 <- 1b <- 2b <- 3b (now strictly longer; head switches)
	b2b := mkBlock(2, n1b.ID, 2002)
	n2b, _ := k.Push(b2b)
	b3b := mkBlock(3, n2b.ID, 3002)
	n3b, _ := k.Push(b3b)
	if !bytes.Equal(k.Head().ID, n3b.ID) {
		t.Fatal("head should switch to longer branch 3b")
	}

	// branch from new head (3b) vs old head (2a): common ancestor = genesis(0).
	newBr, oldBr, err := k.GetBranch(n3b.ID, n2a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// new side: 3b,2b,1b ; old side: 2a,1a
	if len(newBr) != 3 || len(oldBr) != 2 {
		t.Fatalf("branch lens = %d,%d want 3,2", len(newBr), len(oldBr))
	}
	if !bytes.Equal(newBr[0].ID, n3b.ID) || !bytes.Equal(oldBr[0].ID, n2a.ID) {
		t.Fatal("branches must be tip-first")
	}
}
