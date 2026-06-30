// Package genesis builds the TRON genesis block (transactions, txTrieRoot, block id)
// and loads the initial account/witness state, faithfully to java-tron.
//
// Verified against java-tron release_v4.7.1:
//   - genesis tx: TransferContract{owner=ASCII "0x000000000000000000000", to=addr, amount}
//     wrapped as Transaction{raw_data{contract[]{type:TransferContract, parameter:Any.pack}}}
//     (TransactionUtil.newGenesisTransaction).
//   - per-tx Merkle leaf = SHA-256 of the FULL serialized Transaction
//     (TransactionCapsule.getMerkleHash).
//   - txTrieRoot = SHA-256 binary Merkle (internal/merkle).
//   - block id = SHA-256 of the serialized BlockHeader.raw, after txTrieRoot and the
//     genesis witness note are set (BlockCapsule.getBlockId / setMerkleRoot / setWitness).
//
// CONSENSUS-CRITICAL. accountStateRoot is NOT set at genesis (mainnet default).
package genesis

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/address"
	"github.com/Redchar1992/go-tron/internal/crypto"
	"github.com/Redchar1992/go-tron/internal/merkle"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// genesisOwnerAddress is the exact ASCII byte string java-tron uses as the genesis
// TransferContract owner_address (NOT a decoded address). See
// chainbase TransactionUtil.newGenesisTransaction.
var genesisOwnerAddress = []byte("0x000000000000000000000")

// WitnessNote is the genesis "witness_address" payload java-tron sets via setWitness
// (BlockUtil.newGenesisBlockCapsule). It is ASCII bytes, not a real address.
const WitnessNote = "A new system must allow existing systems to be linked together " +
	"without requiring any central control or coordination"

// Asset is a genesis account allocation.
type Asset struct {
	Name    string `json:"name"`
	Address string `json:"address"` // Base58Check ("T...")
	Balance int64  `json:"balance"`
}

// Witness is a genesis witness entry.
type Witness struct {
	Address   string `json:"address"` // Base58Check
	URL       string `json:"url"`
	VoteCount int64  `json:"voteCount"`
}

// Config is a TRON genesis specification (the genesis.block section of a network config).
type Config struct {
	Timestamp  int64     `json:"timestamp"`
	ParentHash string    `json:"parentHash"` // hex, optionally 0x-prefixed
	Number     int64     `json:"number"`
	Assets     []Asset   `json:"assets"`
	Witnesses  []Witness `json:"witnesses"`
}

// NewGenesisTransaction builds a genesis TransferContract transaction to a 21-byte
// address for the given amount, matching java-tron's newGenesisTransaction.
func NewGenesisTransaction(to []byte, amount int64) (*core.Transaction, error) {
	tc := &core.TransferContract{
		OwnerAddress: genesisOwnerAddress,
		ToAddress:    to,
		Amount:       amount,
	}
	param, err := anypb.New(tc)
	if err != nil {
		return nil, fmt.Errorf("genesis: pack TransferContract: %w", err)
	}
	return &core.Transaction{
		RawData: &core.TransactionRaw{
			Contract: []*core.Transaction_Contract{{
				Type:      core.Transaction_Contract_TransferContract,
				Parameter: param,
			}},
		},
	}, nil
}

// TxMerkleHash returns SHA-256 of the FULL serialized transaction (java-tron getMerkleHash).
func TxMerkleHash(tx *core.Transaction) ([]byte, error) {
	b, err := proto.Marshal(tx)
	if err != nil {
		return nil, err
	}
	return crypto.Sha256(b), nil
}

// Transactions builds the ordered genesis transactions from the asset allocations.
func (c *Config) Transactions() ([]*core.Transaction, error) {
	txs := make([]*core.Transaction, 0, len(c.Assets))
	for i, a := range c.Assets {
		addr, err := address.FromBase58(a.Address)
		if err != nil {
			return nil, fmt.Errorf("genesis asset %d (%q): %w", i, a.Address, err)
		}
		tx, err := NewGenesisTransaction(addr.Bytes(), a.Balance)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

// TxTrieRootOf computes the transaction Merkle root over the given transactions.
func TxTrieRootOf(txs []*core.Transaction) ([]byte, error) {
	leaves := make([][]byte, len(txs))
	for i, tx := range txs {
		h, err := TxMerkleHash(tx)
		if err != nil {
			return nil, err
		}
		leaves[i] = h
	}
	return merkle.Root(leaves), nil
}

// TxTrieRoot computes the genesis transaction Merkle root.
func (c *Config) TxTrieRoot() ([]byte, error) {
	txs, err := c.Transactions()
	if err != nil {
		return nil, err
	}
	return TxTrieRootOf(txs)
}

// HeaderRaw builds the genesis BlockHeader.raw with the fields java-tron populates
// (timestamp, parentHash, number, txTrieRoot, witnessAddress).
func (c *Config) HeaderRaw() (*core.BlockHeaderRaw, error) {
	root, err := c.TxTrieRoot()
	if err != nil {
		return nil, err
	}
	parent, err := hex.DecodeString(strings.TrimPrefix(c.ParentHash, "0x"))
	if err != nil {
		return nil, fmt.Errorf("genesis: parentHash: %w", err)
	}
	return &core.BlockHeaderRaw{
		Timestamp:      c.Timestamp,
		ParentHash:     parent,
		Number:         c.Number,
		TxTrieRoot:     root,
		WitnessAddress: []byte(WitnessNote),
	}, nil
}

// HeaderHash returns the raw SHA-256 of the serialized BlockHeader.raw.
func HeaderHash(raw *core.BlockHeaderRaw) ([]byte, error) {
	b, err := proto.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return crypto.Sha256(b), nil
}

// BlockID returns the canonical TRON block id for a header: SHA-256 of the serialized
// BlockHeader.raw, with the first 8 bytes overwritten by the block number (big-endian).
// This matches java-tron's Sha256Hash.generateBlockId / BlockCapsule.getBlockId.
func BlockID(raw *core.BlockHeaderRaw) ([]byte, error) {
	h, err := HeaderHash(raw)
	if err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint64(h[0:8], uint64(raw.GetNumber()))
	return h, nil
}

// BlockID returns the genesis block id.
func (c *Config) BlockID() ([]byte, error) {
	raw, err := c.HeaderRaw()
	if err != nil {
		return nil, err
	}
	return BlockID(raw)
}

// Load writes the initial accounts and witnesses into state. (Account fields beyond
// address/balance, and witness URL/vote, are populated as available; richer fields
// like account name/type are not consensus-relevant to the block id.)
func (c *Config) Load(st *state.State) error {
	for i, a := range c.Assets {
		addr, err := address.FromBase58(a.Address)
		if err != nil {
			return fmt.Errorf("genesis asset %d: %w", i, err)
		}
		if err := st.Accounts.Put(&core.Account{Address: addr.Bytes(), Balance: a.Balance}); err != nil {
			return err
		}
	}
	for i, w := range c.Witnesses {
		addr, err := address.FromBase58(w.Address)
		if err != nil {
			return fmt.Errorf("genesis witness %d: %w", i, err)
		}
		if err := st.Witnesses.Put(&core.Witness{Address: addr.Bytes(), Url: w.URL, VoteCount: w.VoteCount}); err != nil {
			return err
		}
	}
	return nil
}
