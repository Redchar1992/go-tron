package node

import (
	"bytes"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// chainRootKey stores the serialized block used to seed the in-memory fork tree.
// State itself lives in the normal state-store prefixes; keeping the root block here
// lets a process restart reconstruct Manager without trusting an external RPC.
var chainRootKey = []byte("meta/chain/root")

var deterministicProto = proto.MarshalOptions{Deterministic: true}

func persistChainRoot(store db.KV, root *core.Block) error {
	encoded, err := deterministicProto.Marshal(root)
	if err != nil {
		return fmt.Errorf("node: marshal chain root: %w", err)
	}
	return store.Put(chainRootKey, encoded)
}

func loadChainRoot(store db.KV) (*core.Block, error) {
	encoded, err := store.Get(chainRootKey)
	if err != nil {
		if err == db.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("node: read chain root: %w", err)
	}
	root := new(core.Block)
	if err := proto.Unmarshal(encoded, root); err != nil {
		return nil, fmt.Errorf("node: decode chain root: %w", err)
	}
	if root.GetBlockHeader() == nil || root.GetBlockHeader().GetRawData() == nil {
		return nil, fmt.Errorf("node: persisted chain root has no header")
	}
	if err := validateBlock(root); err != nil {
		return nil, fmt.Errorf("node: persisted chain root: %w", err)
	}
	if _, err := block.ID(root); err != nil {
		return nil, fmt.Errorf("node: persisted chain root id: %w", err)
	}
	return root, nil
}

// bootstrapChain initializes a configured fresh chain or restores the persisted root.
// It intentionally does not pretend to recover an uncommitted head: Manager's revoking
// sessions are still memory-only until solidification is implemented. A persisted root
// therefore always corresponds to the durable base state.
func (n *Node) bootstrapChain() error {
	root, err := loadChainRoot(n.committed)
	if err != nil {
		return err
	}
	if root != nil {
		if n.cfg.Genesis != nil {
			expected, err := n.cfg.Genesis.Block()
			if err != nil {
				return fmt.Errorf("node: build configured genesis for verification: %w", err)
			}
			gotID, err := block.ID(root)
			if err != nil {
				return err
			}
			wantID, err := block.ID(expected)
			if err != nil {
				return err
			}
			if !bytes.Equal(gotID, wantID) {
				return fmt.Errorf("node: configured genesis does not match persisted chain root (got %x want %x)", gotID, wantID)
			}
		}
		if err := n.manager.Start(root); err != nil {
			return fmt.Errorf("node: restore chain root: %w", err)
		}
		n.log.Info("node: restored chain root", "number", block.Number(root), "id", fmt.Sprintf("%x", mustBlockID(root)))
		return nil
	}

	if n.cfg.Genesis == nil {
		n.log.Warn("node: no genesis configured; chain manager awaits bootstrap")
		return nil
	}
	if err := n.manager.InitGenesis(n.cfg.Genesis); err != nil {
		return fmt.Errorf("node: initialize genesis: %w", err)
	}
	root = n.manager.Head().Block
	if err := persistChainRoot(n.committed, root); err != nil {
		return fmt.Errorf("node: persist chain root: %w", err)
	}
	n.log.Info("node: initialized genesis", "number", block.Number(root), "id", fmt.Sprintf("%x", mustBlockID(root)))
	return nil
}

func mustBlockID(b *core.Block) []byte {
	id, _ := block.ID(b)
	return id
}

// GenesisRootKey is exposed for tooling that needs to inspect the durable chain marker.
// Callers should treat the key as opaque and use loadChainRoot through Node startup.
func GenesisRootKey() []byte { return append([]byte(nil), chainRootKey...) }
