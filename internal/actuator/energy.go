package actuator

import (
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/resource"
	"github.com/Redchar1992/go-tron/internal/state"
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
// derivation reads. It is populated from the PropertyStore (energyDynamicPropsFromState);
// on a from-genesis chain these hold the seeded fresh-chain defaults (weight 0), which makes
// globalEnergyLimit return 0 so every account's available staked energy is 0 — exactly the
// pre-M3.5d behavior where the caller burns all consumed energy as TRX. Once freeze actuators
// grow TOTAL_ENERGY_WEIGHT above 0 the same derivation returns real staked energy with no
// change to the call sites.
type energyDynamicProps struct {
	totalEnergyCurrentLimit int64
	totalEnergyWeight       int64
	supportUnfreezeDelay    bool
	allowNewReward          bool
}

// energyDynamicPropsFromState reads the staked-energy derivation's network globals from the
// PropertyStore (java-tron DynamicPropertiesStore.getTotalEnergyCurrentLimit /
// getTotalEnergyWeight / supportUnfreezeDelay / allowNewReward).
func energyDynamicPropsFromState(st *state.State) (energyDynamicProps, error) {
	currentLimit, err := st.Properties.TotalEnergyCurrentLimit()
	if err != nil {
		return energyDynamicProps{}, err
	}
	weight, err := st.Properties.TotalEnergyWeight()
	if err != nil {
		return energyDynamicProps{}, err
	}
	unfreezeDelay, err := st.Properties.SupportUnfreezeDelay()
	if err != nil {
		return energyDynamicProps{}, err
	}
	newReward, err := st.Properties.AllowNewReward()
	if err != nil {
		return energyDynamicProps{}, err
	}
	return energyDynamicProps{
		totalEnergyCurrentLimit: currentLimit,
		totalEnergyWeight:       weight,
		supportUnfreezeDelay:    unfreezeDelay,
		allowNewReward:          newReward,
	}, nil
}

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

// chargeCallerEnergy is the write-back half of EnergyProcessor.useEnergy: fold `usage`
// staked energy into the account's decayed energy_usage average and stamp
// latest_consume_time_for_energy with the current head slot. Called for every contract tx
// (a zero usage still stamps the slot, matching java-tron).
//
// An account absent from the local store is skipped: such a caller (provider-only, in
// mid-chain replay) had no locally-visible stake, so its usage is necessarily 0; its real
// resource state arrives with fixture state seeding (M3.5e §4.1).
func chargeCallerEnergy(ctx *Context, addr []byte, usage, nowMs int64) error {
	acct, err := ctx.State.Accounts.Get(addr)
	if err != nil {
		return nil
	}
	if acct.AccountResource == nil {
		acct.AccountResource = &core.Account_AccountResource{}
	}
	res := acct.AccountResource
	nowSlot := headSlot(nowMs)
	res.EnergyUsage = resource.IncreaseEnergyUsage(
		res.GetEnergyUsage(), usage, res.GetLatestConsumeTimeForEnergy(), nowSlot,
		resource.EnergyWindow(res.GetEnergyWindowSize(), res.GetEnergyWindowOptimized()))
	res.LatestConsumeTimeForEnergy = nowSlot
	return ctx.State.Accounts.Put(acct)
}
