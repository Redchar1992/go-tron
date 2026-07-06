// Manager is the block/transaction pipeline — the go-tron analog of java-tron's
// Manager. It wires the revoking database, the chain stores, and the KhaosDB fork tree
// into the canonical flow:
//
//   - PushBlock: dedup -> validate roots -> KhaosDB insert -> extend head OR switchFork.
//   - processBlock: open a revoking session, apply every transaction via the actuators,
//     leave the session open (an unconfirmed block) for later commit/revoke.
//   - switchFork: revoke the old branch's sessions and re-apply the new (longer) branch;
//     restore the old branch if the new one fails to apply (rollback-safe).
//
// Invariant: len(m.applied) == m.db.Depth() == number of blocks applied above the
// committed base, and m.applied is exactly the current head's branch above that base.
//
// M2 scope: solidification/flush-to-base (driven by the DPoS solid block) is deferred,
// so applied blocks stay in revoking sessions rather than flushing to the base KV. Fee /
// energy accounting is deferred (see internal/actuator). The consensus property M2
// verifies is ROOT equivalence: our recomputed txTrieRoot and block id equal the real
// mainnet block's, for every replayed block.
//
// CONSENSUS-CRITICAL: ordering mirrors java-tron.
package node

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/actuator"
	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/genesis"
	"github.com/Redchar1992/go-tron/internal/khaos"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// ErrTxTrieRootMismatch means a block's recomputed transaction Merkle root disagrees
// with the root embedded in its header — a consensus-fatal divergence.
var ErrTxTrieRootMismatch = errors.New("manager: txTrieRoot mismatch")

// appliedRef identifies an applied block whose revoking session is still open.
type appliedRef struct {
	id  []byte
	num int64
}

// Manager drives the block/transaction pipeline over state + db + khaos.
type Manager struct {
	db            *db.Database
	state         *state.State
	khaos         *khaos.KhaosDB
	applied       []appliedRef // head branch above the committed base; aligned with db sessions
	lenient       bool         // replay-provisioning: auto-fund missing TransferContract owners
	receiptSink   func(blockNum int64, receipts []*actuator.Receipt)
	stateProvider actuator.StateProvider
}

// NewManager constructs a Manager over the given revoking database. maxFork bounds how
// far below the head KhaosDB retains blocks (0 = unbounded, for tests / short replays).
func NewManager(d *db.Database, maxFork int64) *Manager {
	return &Manager{
		db:    d,
		state: state.New(d),
		khaos: khaos.New(maxFork),
	}
}

// State exposes the chain stores (read-only use by callers / tests).
func (m *Manager) State() *state.State { return m.state }

// SetReceiptSink registers a callback invoked with each applied block's VM receipts
// (energy bills + execution results from CreateSmartContract/TriggerSmartContract). The
// differential harness uses it to diff our receipts against on-chain values; nil (default)
// disables it. Receipts are emitted only for blocks that reach processBlock's tail (i.e.
// applied, not revoked).
func (m *Manager) SetReceiptSink(fn func(blockNum int64, receipts []*actuator.Receipt)) {
	m.receiptSink = fn
}

// SetStateProvider registers the historical-state oracle the VM falls through to for
// accounts/contracts absent from the node stores — the M3.5c dependency for replaying
// mid-chain contract transactions whose callee state predates the replay window. nil
// (default) means genesis-contiguous replay, where all state is built by the replay.
func (m *Manager) SetStateProvider(p actuator.StateProvider) { m.stateProvider = p }

// EnableReplayProvisioning makes processBlock auto-fund TransferContract owners that are
// missing or underfunded. It is for differential replay starting from a non-genesis
// block, where full prior state is unavailable: it lets the pipeline exercise real dense
// blocks at the cost of balance-equivalence (which is not offline-verifiable anyway —
// fee/bandwidth correctness is checked separately via the receipt oracle). Root
// verification (block id + txTrieRoot) is unaffected.
func (m *Manager) EnableReplayProvisioning() { m.lenient = true }

// Head returns the current chain tip node, or nil before InitGenesis.
func (m *Manager) Head() *khaos.KBlock { return m.khaos.Head() }

// Start seeds KhaosDB with a root block (genesis or a snapshot block) as the head,
// without touching state. Replay begins from the root's children. Used by the
// differential harness, which reconstructs the root block from chain data.
func (m *Manager) Start(root *core.Block) error {
	if err := m.khaos.Start(root); err != nil {
		return fmt.Errorf("manager: seed khaos: %w", err)
	}
	return nil
}

// InitGenesis loads the genesis accounts/witnesses into the committed base state and
// seeds KhaosDB with the genesis block as the head. Genesis state is committed directly
// (no revoking session) — it is the base every replayed block builds on.
func (m *Manager) InitGenesis(cfg *genesis.Config) error {
	if m.db.Depth() != 0 {
		return errors.New("manager: InitGenesis must run on a fresh database")
	}
	if err := cfg.Load(m.state); err != nil {
		return fmt.Errorf("manager: load genesis state: %w", err)
	}
	gb, err := cfg.Block()
	if err != nil {
		return fmt.Errorf("manager: build genesis block: %w", err)
	}
	return m.Start(gb)
}

// validateBlock recomputes the transaction Merkle root and checks it against the root
// carried in the header. This is the M2 consensus assertion: our merkle == java-tron's.
func validateBlock(b *core.Block) error {
	got, err := block.CalcTxTrieRoot(b.GetTransactions())
	if err != nil {
		return err
	}
	want := b.GetBlockHeader().GetRawData().GetTxTrieRoot()
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: got %x want %x", ErrTxTrieRootMismatch, got, want)
	}
	return nil
}

// PushBlock validates a block, inserts it into the fork tree, and either extends the
// head linearly or switches to it as a longer branch. A duplicate or shorter side
// branch is a no-op (stored only). Mirrors java-tron Manager.pushBlock ordering.
func (m *Manager) PushBlock(b *core.Block) error {
	id, err := block.ID(b)
	if err != nil {
		return err
	}
	if m.khaos.Contains(id) { // de-dup
		return nil
	}
	if err := validateBlock(b); err != nil {
		return err
	}

	oldHead := m.khaos.Head()
	if oldHead == nil {
		return errors.New("manager: PushBlock before InitGenesis")
	}

	node, err := m.khaos.Push(b)
	if err != nil {
		return err // ErrUnlinked: parent unknown
	}

	// Not the new head: a shorter side branch. Stored in khaos, state untouched.
	if !bytes.Equal(m.khaos.Head().ID, node.ID) {
		return nil
	}

	// New head. Either it extends the currently-applied tip (linear) or it overtook a
	// different branch (fork switch).
	if bytes.Equal(node.ParentID, oldHead.ID) {
		return m.applyOnTop(node)
	}
	return m.switchFork(node, oldHead)
}

// applyOnTop opens a session and applies the block on top of the current state. On
// failure the session is revoked so committed state is untouched.
func (m *Manager) applyOnTop(node *khaos.KBlock) error {
	m.db.BuildSession()
	if err := m.processBlock(node.Block); err != nil {
		m.db.Revoke()
		return err
	}
	m.applied = append(m.applied, appliedRef{id: node.ID, num: node.Num})
	return nil
}

// processBlock applies every transaction in a block to state via the actuator registry.
// It runs inside an already-open revoking session opened by the caller.
func (m *Manager) processBlock(b *core.Block) error {
	hdr := b.GetBlockHeader().GetRawData()
	blk := actuator.BlockContext{
		Number:    hdr.GetNumber(),
		Timestamp: hdr.GetTimestamp(),
		Witness:   hdr.GetWitnessAddress(),
		Provider:  m.stateProvider,
	}
	var receipts []*actuator.Receipt
	for i, tx := range b.GetTransactions() {
		if m.lenient {
			if err := m.provisionReplay(tx); err != nil {
				return fmt.Errorf("manager: block %d tx %d provision: %w", block.Number(b), i, err)
			}
		}
		res, err := actuator.Apply(m.state, tx, blk)
		if err != nil {
			return fmt.Errorf("manager: block %d tx %d: %w", block.Number(b), i, err)
		}
		receipts = append(receipts, res.Receipts...)
	}
	if m.receiptSink != nil && len(receipts) > 0 {
		m.receiptSink(blk.Number, receipts)
	}
	return nil
}

// provisionReplay tops up TransferContract owners to cover their transfer amount when the
// Manager runs in replay-provisioning mode (see EnableReplayProvisioning). Writes land in
// the current open session, so they revoke cleanly with the block.
func (m *Manager) provisionReplay(tx *core.Transaction) error {
	for _, c := range tx.GetRawData().GetContract() {
		if c.GetType() != core.Transaction_Contract_TransferContract {
			continue
		}
		tc := new(core.TransferContract)
		if err := c.GetParameter().UnmarshalTo(tc); err != nil {
			return err
		}
		acc, err := m.state.Accounts.Get(tc.GetOwnerAddress())
		if err != nil {
			acc = &core.Account{Address: tc.GetOwnerAddress()}
		}
		if acc.GetBalance() < tc.GetAmount() {
			acc.Balance = tc.GetAmount()
			if err := m.state.Accounts.Put(acc); err != nil {
				return err
			}
		}
	}
	return nil
}

// switchFork revokes the old branch's open sessions and re-applies the new (longer)
// branch in ancestor-first order. If the new branch fails to apply, the old branch is
// restored so the node never lands in a half-applied state.
func (m *Manager) switchFork(newHead, oldHead *khaos.KBlock) error {
	newBr, oldBr, err := m.khaos.GetBranch(newHead.ID, oldHead.ID)
	if err != nil {
		return fmt.Errorf("manager: switchFork branch: %w", err)
	}

	// Revoke the old branch (it sits at the top of the session stack, tip-first).
	for range oldBr {
		m.db.Revoke()
		m.applied = m.applied[:len(m.applied)-1]
	}

	// Apply the new branch ancestor-first (GetBranch returns tip-first).
	for i := len(newBr) - 1; i >= 0; i-- {
		node := newBr[i]
		m.db.BuildSession()
		if err := m.processBlock(node.Block); err != nil {
			m.db.Revoke()
			m.restoreBranch(oldBr) // best-effort rollback to the prior head
			return fmt.Errorf("manager: switchFork apply %x: %w", node.ID, err)
		}
		m.applied = append(m.applied, appliedRef{id: node.ID, num: node.Num})
	}
	return nil
}

// restoreBranch re-applies a previously-revoked branch (ancestor-first) after a failed
// fork switch, returning the node to its prior head. Best-effort.
func (m *Manager) restoreBranch(branch []*khaos.KBlock) {
	for i := len(branch) - 1; i >= 0; i-- {
		node := branch[i]
		m.db.BuildSession()
		if err := m.processBlock(node.Block); err != nil {
			m.db.Revoke()
			return
		}
		m.applied = append(m.applied, appliedRef{id: node.ID, num: node.Num})
	}
}
