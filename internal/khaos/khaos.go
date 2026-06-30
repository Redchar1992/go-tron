// Package khaos is the in-memory fork tree of recent blocks — the go-tron analog of
// java-tron's KhaosDatabase. It holds the chain head plus every recent block keyed by
// id, linked to its parent, so the Manager can:
//
//   - detect whether an incoming block extends the head or builds a side branch;
//   - resolve forks by the TRON rule (strictly-longer chain wins, first-seen on ties);
//   - enumerate the two divergent branches back to their common ancestor for switchFork.
//
// It is NOT the durable store — committed state lives in db/state. KhaosDB only tracks
// the unconfirmed tip region and is pruned below the solidified height.
//
// CONSENSUS-RELEVANT (fork choice), but the durable bytes are in state/db.
package khaos

import (
	"encoding/hex"
	"errors"

	"github.com/Redchar1992/go-tron/internal/block"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// ErrUnlinked is returned when pushing a block whose parent is not in the tree.
var ErrUnlinked = errors.New("khaos: parent block not found (unlinked)")

// ErrNoCommonAncestor is returned by GetBranch when two ids do not converge.
var ErrNoCommonAncestor = errors.New("khaos: no common ancestor")

// KBlock is a node in the fork tree.
type KBlock struct {
	Block    *core.Block
	ID       []byte
	ParentID []byte
	Num      int64
	Parent   *KBlock
}

// KhaosDB is the fork tree.
type KhaosDB struct {
	blocks  map[string]*KBlock
	head    *KBlock
	maxSize int64 // retained block-number span below head; 0 = unbounded
}

// New returns an empty fork tree. maxSize bounds how far below the head blocks are
// retained (0 = unbounded, for tests / small replays).
func New(maxSize int64) *KhaosDB {
	return &KhaosDB{blocks: make(map[string]*KBlock), maxSize: maxSize}
}

func key(id []byte) string { return hex.EncodeToString(id) }

// Start seeds the tree with the root (genesis or a snapshot block) as the head. The
// root needs no parent in the tree.
func (k *KhaosDB) Start(root *core.Block) error {
	id, err := block.ID(root)
	if err != nil {
		return err
	}
	node := &KBlock{
		Block:    root,
		ID:       id,
		ParentID: block.ParentHash(root),
		Num:      block.Number(root),
	}
	k.blocks[key(id)] = node
	k.head = node
	return nil
}

// Push inserts a block, linking it to its parent. Returns the new node. If the block is
// already present its existing node is returned. If the parent is absent, ErrUnlinked.
// The head advances only when the new block's number is strictly greater (first-seen
// wins ties) — the TRON longest-chain fork rule.
func (k *KhaosDB) Push(b *core.Block) (*KBlock, error) {
	id, err := block.ID(b)
	if err != nil {
		return nil, err
	}
	if existing, ok := k.blocks[key(id)]; ok {
		return existing, nil
	}
	parentID := block.ParentHash(b)
	parent, ok := k.blocks[key(parentID)]
	if !ok {
		return nil, ErrUnlinked
	}
	node := &KBlock{
		Block:    b,
		ID:       id,
		ParentID: parentID,
		Num:      block.Number(b),
		Parent:   parent,
	}
	k.blocks[key(id)] = node
	if k.head == nil || node.Num > k.head.Num {
		k.head = node
		k.prune()
	}
	return node, nil
}

// Head returns the current chain tip.
func (k *KhaosDB) Head() *KBlock { return k.head }

// Get returns the node for id, or nil.
func (k *KhaosDB) Get(id []byte) *KBlock { return k.blocks[key(id)] }

// Contains reports whether id is in the tree.
func (k *KhaosDB) Contains(id []byte) bool {
	_, ok := k.blocks[key(id)]
	return ok
}

// GetBranch returns the two divergent branches from id1 and id2 back to (but excluding)
// their common ancestor. Each branch is ordered tip-first (id at index 0, the node just
// above the ancestor last). When id1 and id2 are on the same chain, the shorter branch
// is empty. Used by the Manager to switchFork: revoke branch2 (old), apply reversed
// branch1 (new). Mirrors java-tron KhaosDatabase.getBranch.
func (k *KhaosDB) GetBranch(id1, id2 []byte) (branch1, branch2 []*KBlock, err error) {
	n1 := k.Get(id1)
	n2 := k.Get(id2)
	if n1 == nil || n2 == nil {
		return nil, nil, ErrNoCommonAncestor
	}
	for n1.Num > n2.Num {
		branch1 = append(branch1, n1)
		n1 = n1.Parent
		if n1 == nil {
			return nil, nil, ErrNoCommonAncestor
		}
	}
	for n2.Num > n1.Num {
		branch2 = append(branch2, n2)
		n2 = n2.Parent
		if n2 == nil {
			return nil, nil, ErrNoCommonAncestor
		}
	}
	for n1 != n2 {
		branch1 = append(branch1, n1)
		branch2 = append(branch2, n2)
		n1 = n1.Parent
		n2 = n2.Parent
		if n1 == nil || n2 == nil {
			return nil, nil, ErrNoCommonAncestor
		}
	}
	return branch1, branch2, nil
}

// prune drops nodes more than maxSize below the head height (no-op when maxSize == 0).
func (k *KhaosDB) prune() {
	if k.maxSize <= 0 || k.head == nil {
		return
	}
	floor := k.head.Num - k.maxSize
	for id, n := range k.blocks {
		if n.Num < floor {
			delete(k.blocks, id)
		}
	}
}

// Size reports the number of nodes retained (test helper).
func (k *KhaosDB) Size() int { return len(k.blocks) }
