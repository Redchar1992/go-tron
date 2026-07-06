package actuator

import (
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/resource"
)

// This file maps stored account + block + dynamic-property state onto the pure
// staked-energy derivation in internal/resource (resource.AvailableStakedEnergy). It is the
// state-facing half of java-tron EnergyProcessor.getAccountLeftEnergyFromFreeze; the
// arithmetic lives in resource/stake.go.

// mainnetGenesisTimestampMs is the TRON mainnet genesis block timestamp (ms). getHeadSlot
// subtracts it so a block slot is genesis-relative, matching the frame stored in
// latest_consume_time_for_energy.
//
// PLACEHOLDER: once genesis/chain config is plumbed into the node, read it from there
// instead of this constant. It only affects the recovery slot delta, which is moot while
// the dynamic-property globals below are zero (limit 0 -> available 0).
const mainnetGenesisTimestampMs int64 = 1529891469000

// energyDynamicProps carries the DynamicPropertiesStore globals the staked-energy
// derivation reads.
//
// DEFERRED (state plumbing): go-tron has no DynamicPropertiesStore yet (see the notes in
// internal/bandwidth), so these are zero. Zero totalEnergyWeight makes globalEnergyLimit
// return 0, so every account's available staked energy is 0 — exactly the pre-M3.5d
// behavior where the caller burns all consumed energy as TRX. When the dynamic-properties
// milestone lands, populate these from the store; the derivation then returns real staked
// energy with no change to the call sites below.
type energyDynamicProps struct {
	totalEnergyCurrentLimit int64
	totalEnergyWeight       int64
	supportUnfreezeDelay    bool
	allowNewReward          bool
}

// deferredEnergyDynamicProps is the zero placeholder used until the DynamicPropertiesStore
// exists (see the type doc).
var deferredEnergyDynamicProps = energyDynamicProps{}

// headSlot converts a block timestamp (ms) to a genesis-relative slot, matching
// EnergyProcessor.getHeadSlot.
func headSlot(blockTimestampMs int64) int64 {
	return (blockTimestampMs - mainnetGenesisTimestampMs) / resource.BlockProducedIntervalMs
}

// allFrozenBalanceForEnergy mirrors AccountCapsule.getAllFrozenBalanceForEnergy: own V1+V2
// energy stake plus energy delegated TO this account (V1+V2), all in sun.
func allFrozenBalanceForEnergy(a *core.Account) int64 {
	res := a.GetAccountResource()
	total := res.GetFrozenBalanceForEnergy().GetFrozenBalance() // V1 self-frozen for energy
	total += res.GetAcquiredDelegatedFrozenBalanceForEnergy()   // V1 delegated to me
	total += res.GetAcquiredDelegatedFrozenV2BalanceForEnergy() // V2 delegated to me
	for _, f := range a.GetFrozenV2() {                         // V2 self-frozen for energy
		if f.GetType() == core.ResourceCode_ENERGY {
			total += f.GetAmount()
		}
	}
	return total
}

// availableStakedEnergy derives an account's currently-available staked energy at the given
// block, faithful to EnergyProcessor.getAccountLeftEnergyFromFreeze. A nil account (never
// seen on chain) has no stake.
func availableStakedEnergy(a *core.Account, blockTimestampMs int64, props energyDynamicProps) int64 {
	if a == nil {
		return 0
	}
	res := a.GetAccountResource()
	return resource.AvailableStakedEnergy(resource.StakedEnergyInput{
		FrozenBalanceForEnergy:  allFrozenBalanceForEnergy(a),
		EnergyUsage:             res.GetEnergyUsage(),
		LatestConsumeSlot:       res.GetLatestConsumeTimeForEnergy(),
		WindowSize:              resource.EnergyWindow(res.GetEnergyWindowSize(), res.GetEnergyWindowOptimized()),
		NowSlot:                 headSlot(blockTimestampMs),
		TotalEnergyCurrentLimit: props.totalEnergyCurrentLimit,
		TotalEnergyWeight:       props.totalEnergyWeight,
		SupportUnfreezeDelay:    props.supportUnfreezeDelay,
		AllowNewReward:          props.allowNewReward,
	})
}

// lookupAccount reads an account from state, returning nil when absent (never seen on
// chain) so callers treat it as having no stake.
func lookupAccount(ctx *Context, addr []byte) *core.Account {
	a, err := ctx.State.Accounts.Get(addr)
	if err != nil {
		return nil
	}
	return a
}
