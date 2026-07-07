package node

import (
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/replay"
)

// SeedChainState writes a captured resource-state snapshot — the network globals
// (TOTAL_ENERGY_WEIGHT / TOTAL_ENERGY_CURRENT_LIMIT / TOTAL_NET_WEIGHT / ENERGY_FEE /
// UNFREEZE_DELAY_DAYS / ALLOW_NEW_REWARD) and each account's staking state (V1/V2 self stake,
// delegated-in, energy usage + consume slot) — into the node state, so a mid-chain replay's
// staked-energy derivation matches the on-chain receipts (M3.5e §4.1/§4.2). Call it after
// Start and before the first PushBlock: with no session open the writes land in the committed
// base (the prior state the window builds on), and the freeze/unfreeze actuators maintain the
// values forward. A zero fixture field is left at its existing/genesis value.
func (m *Manager) SeedChainState(cs *replay.ChainState) error {
	props := m.state.Properties
	for _, kv := range []struct {
		key []byte
		v   int64
	}{
		{[]byte("TOTAL_ENERGY_WEIGHT"), cs.DynamicProps.TotalEnergyWeight},
		{[]byte("TOTAL_ENERGY_CURRENT_LIMIT"), cs.DynamicProps.TotalEnergyCurrentLimit},
		{[]byte("TOTAL_NET_WEIGHT"), cs.DynamicProps.TotalNetWeight},
		{[]byte("ENERGY_FEE"), cs.DynamicProps.EnergyFee},
		{[]byte("UNFREEZE_DELAY_DAYS"), cs.DynamicProps.UnfreezeDelayDays},
		{[]byte("ALLOW_NEW_REWARD"), cs.DynamicProps.AllowNewReward},
	} {
		if kv.v == 0 {
			continue // leave the genesis default
		}
		if err := props.PutInt64(kv.key, kv.v); err != nil {
			return err
		}
	}

	for addrHex, ar := range cs.Accounts {
		addr, err := hex.DecodeString(addrHex)
		if err != nil {
			return fmt.Errorf("replay: chainstate addr %q: %w", addrHex, err)
		}
		acct, err := m.state.Accounts.Get(addr)
		if err != nil {
			acct = &core.Account{Address: addr, Type: core.AccountType_Normal}
		}
		if ar.Balance != 0 {
			acct.Balance = ar.Balance
		}
		if acct.AccountResource == nil {
			acct.AccountResource = &core.Account_AccountResource{}
		}
		res := acct.AccountResource
		if ar.FrozenBalanceForEnergy != 0 {
			res.FrozenBalanceForEnergy = &core.Account_Frozen{FrozenBalance: ar.FrozenBalanceForEnergy}
		}
		if ar.FrozenV2Energy != 0 {
			setFrozenV2Energy(acct, ar.FrozenV2Energy)
		}
		if ar.AcquiredDelegatedEnergy != 0 {
			res.AcquiredDelegatedFrozenBalanceForEnergy = ar.AcquiredDelegatedEnergy
		}
		if ar.AcquiredDelegatedV2Energy != 0 {
			res.AcquiredDelegatedFrozenV2BalanceForEnergy = ar.AcquiredDelegatedV2Energy
		}
		res.EnergyUsage = ar.EnergyUsage
		res.LatestConsumeTimeForEnergy = ar.LatestConsumeTimeForEnergy
		if ar.EnergyWindowSize != 0 {
			res.EnergyWindowSize = ar.EnergyWindowSize
		}
		res.EnergyWindowOptimized = ar.EnergyWindowOptimized
		if err := m.state.Accounts.Put(acct); err != nil {
			return err
		}
	}
	return nil
}

// setFrozenV2Energy sets (replacing) the account's V2 ENERGY FreezeV2 entry.
func setFrozenV2Energy(a *core.Account, amount int64) {
	for _, f := range a.GetFrozenV2() {
		if f.GetType() == core.ResourceCode_ENERGY {
			f.Amount = amount
			return
		}
	}
	a.FrozenV2 = append(a.FrozenV2, &core.Account_FreezeV2{Type: core.ResourceCode_ENERGY, Amount: amount})
}

// ReplayFile drives a differential replay of a captured-block fixture through a fresh
// Manager: it seeds the Manager from the first block, then pushes the rest, verifying
// each block's recomputed id + txTrieRoot against the on-chain oracle values. It is a
// diagnostic entrypoint (`gotron --replay <fixture>`) that exercises the M2/M2.5 pipeline
// end-to-end without networking. Returns the number of blocks replayed.
//
// Replay-provisioning is enabled so a fixture that starts mid-chain can still apply its
// TransferContract transactions (balances are not the property under test here; roots
// are). See Manager.EnableReplayProvisioning.
func ReplayFile(path string, log *slog.Logger) (int, error) {
	f, err := replay.Load(path)
	if err != nil {
		return 0, err
	}

	m := NewManager(db.NewDatabase(db.NewMemKV()), 0)
	m.EnableReplayProvisioning()

	root, err := f.Blocks[0].Build()
	if err != nil {
		return 0, err
	}
	if err := f.Blocks[0].Check(root); err != nil {
		return 0, err
	}
	if err := m.Start(root); err != nil {
		return 0, err
	}
	rootID, err := block.ID(root)
	if err != nil {
		return 0, err
	}
	log.Info("replay: seeded root", "num", block.Number(root), "id", hex.EncodeToString(rootID))

	for _, bf := range f.Blocks[1:] {
		b, err := bf.Build()
		if err != nil {
			return 0, err
		}
		if err := bf.Check(b); err != nil {
			return 0, err
		}
		if err := m.PushBlock(b); err != nil {
			return 0, fmt.Errorf("replay: push block %d: %w", bf.Number, err)
		}
		log.Info("replay: block ok", "num", m.Head().Num,
			"id", hex.EncodeToString(m.Head().ID), "txs", len(bf.Transactions))
	}

	log.Info("replay: complete (all roots match chain)",
		"blocks", len(f.Blocks), "head", m.Head().Num)
	return len(f.Blocks), nil
}
