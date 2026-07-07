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
// without touching account state, and records the root's header timestamp as
// LATEST_BLOCK_HEADER_TIMESTAMP so the first processed block's transactions read the
// root's timestamp as "now" — exactly what a java-tron node that had applied the root
// would report. Replay begins from the root's children.
func (m *Manager) Start(root *core.Block) error {
	if err := m.khaos.Start(root); err != nil {
		return fmt.Errorf("manager: seed khaos: %w", err)
	}
	ts := root.GetBlockHeader().GetRawData().GetTimestamp()
	if err := m.state.Properties.SaveLatestBlockHeaderTimestamp(ts); err != nil {
		return fmt.Errorf("manager: seed header timestamp: %w", err)
	}
	// Seed NEXT_MAINTENANCE_TIME to the root's timestamp so the first processed block runs a
	// (typically empty) maintenance and aligns the 6h schedule — java-tron initializes it at
	// genesis likewise.
	if err := m.state.Properties.SaveNextMaintenanceTime(ts); err != nil {
		return fmt.Errorf("manager: seed maintenance time: %w", err)
	}
	return nil
}

// RunMaintenance applies the accumulated votes (VotesStore) to witness vote counts — the DPoS
// vote tally (java-tron MaintenanceManager.countVote + the witness-count update in
// doMaintenance): each voter's (new - old) delta is summed per candidate and added to the
// witness's vote count, then the VotesStore is cleared for the next epoch.
//
// DEFERRED (the remaining reward economics): the SR election (sort/elect the active 27 +
// isJobs), the per-cycle reward accrual (DelegationStore/MortgageService), the GR-power
// removal. This is the vote TALLY only.
func (m *Manager) RunMaintenance() error {
	deltas := map[string]int64{}
	var voters [][]byte
	if err := m.state.Votes.Each(func(v *core.Votes) error {
		for _, ov := range v.GetOldVotes() {
			deltas[string(ov.GetVoteAddress())] -= ov.GetVoteCount()
		}
		for _, nv := range v.GetNewVotes() {
			deltas[string(nv.GetVoteAddress())] += nv.GetVoteCount()
		}
		voters = append(voters, v.GetAddress())
		return nil
	}); err != nil {
		return err
	}
	for _, addr := range voters {
		if err := m.state.Votes.Delete(addr); err != nil {
			return err
		}
	}
	// Apply deltas per witness. Order-independent (each witness updated once), so the map
	// iteration does not affect the result.
	for addrStr, delta := range deltas {
		if delta == 0 {
			continue
		}
		w, err := m.state.Witnesses.Get([]byte(addrStr))
		if err != nil {
			continue // candidate is not (or no longer) a witness — skip, as java-tron does
		}
		w.VoteCount += delta
		if err := m.state.Witnesses.Put(w); err != nil {
			return err
		}
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
		Version:   hdr.GetVersion(),
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
	// DPoS maintenance / vote tally: when this block crosses NEXT_MAINTENANCE_TIME, apply the
	// accumulated votes to witness counts and advance the schedule — after the block's txs,
	// before the header-timestamp save (java-tron consensus.applyBlock ordering).
	next, err := m.state.Properties.NextMaintenanceTime()
	if err != nil {
		return fmt.Errorf("manager: block %d next-maintenance: %w", block.Number(b), err)
	}
	if hdr.GetTimestamp() >= next {
		if err := m.RunMaintenance(); err != nil {
			return fmt.Errorf("manager: block %d maintenance: %w", block.Number(b), err)
		}
		if err := m.state.Properties.UpdateNextMaintenanceTime(hdr.GetTimestamp()); err != nil {
			return fmt.Errorf("manager: block %d update-maintenance: %w", block.Number(b), err)
		}
	}

	// Advance LATEST_BLOCK_HEADER_TIMESTAMP only AFTER the block's transactions, mirroring
	// java-tron Manager.processBlock -> updateDynamicProperties ordering: actuators inside
	// block N read block N-1's timestamp as "now". Runs inside the block's revoking session,
	// so a revoked block rolls the property back too.
	if err := m.state.Properties.SaveLatestBlockHeaderTimestamp(hdr.GetTimestamp()); err != nil {
		return fmt.Errorf("manager: block %d header timestamp: %w", block.Number(b), err)
	}
	if m.receiptSink != nil && len(receipts) > 0 {
		m.receiptSink(blk.Number, receipts)
	}
	return nil
}

// provisionReplay tops up Transfer/FreezeBalance owners to cover their transfer/freeze
// amount when the Manager runs in replay-provisioning mode (see EnableReplayProvisioning).
// Writes land in the current open session, so they revoke cleanly with the block.
// (UnfreezeBalance cannot be provisioned: it carries no amount and needs a pre-existing
// expired frozen entry — historical mid-span unfreezes would need real prior state.)
func (m *Manager) provisionReplay(tx *core.Transaction) error {
	for _, c := range tx.GetRawData().GetContract() {
		var owner []byte
		var amount int64
		switch c.GetType() {
		case core.Transaction_Contract_TransferContract:
			tc := new(core.TransferContract)
			if err := c.GetParameter().UnmarshalTo(tc); err != nil {
				return err
			}
			owner, amount = tc.GetOwnerAddress(), tc.GetAmount()
		case core.Transaction_Contract_FreezeBalanceContract:
			fc := new(core.FreezeBalanceContract)
			if err := c.GetParameter().UnmarshalTo(fc); err != nil {
				return err
			}
			owner, amount = fc.GetOwnerAddress(), fc.GetFrozenBalance()
		case core.Transaction_Contract_VoteWitnessContract:
			if err := m.provisionVote(c); err != nil {
				return err
			}
			continue
		default:
			continue
		}
		acc, err := m.state.Accounts.Get(owner)
		if err != nil {
			acc = &core.Account{Address: owner}
		}
		if acc.GetBalance() < amount {
			acc.Balance = amount
			if err := m.state.Accounts.Put(acc); err != nil {
				return err
			}
		}
	}
	return nil
}

// provisionVote provisions a VoteWitnessContract's prerequisites for mid-chain replay: the
// candidate witnesses (so the vote passes the witness-exists check) and the voter's TRON
// power (set V1 frozen to exactly cover the vote sum). Root verification is unaffected — this
// only lets the real vote tx apply; the on-chain vote/stake state is not the property tested.
func (m *Manager) provisionVote(c *core.Transaction_Contract) error {
	vc := new(core.VoteWitnessContract)
	if err := c.GetParameter().UnmarshalTo(vc); err != nil {
		return err
	}
	var sum int64
	for _, v := range vc.GetVotes() {
		sum += v.GetVoteCount()
		if has, _ := m.state.Witnesses.Has(v.GetVoteAddress()); !has {
			if err := m.state.Witnesses.Put(&core.Witness{Address: v.GetVoteAddress()}); err != nil {
				return err
			}
		}
	}
	acc, err := m.state.Accounts.Get(vc.GetOwnerAddress())
	if err != nil {
		acc = &core.Account{Address: vc.GetOwnerAddress()}
	}
	// TRX_PRECISION sun per vote; a single V1 frozen entry covers the required TRON power.
	acc.Frozen = []*core.Account_Frozen{{FrozenBalance: sum * 1_000_000}}
	return m.state.Accounts.Put(acc)
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
