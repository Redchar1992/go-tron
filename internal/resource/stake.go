package resource

import "math"

// This file derives an account's currently-available STAKED energy — the quantity fed to
// Bill.CallerEnergy / Bill.OriginEnergy and to AccountEnergyLimit's frozenEnergy argument.
// It is the go-tron counterpart of java-tron EnergyProcessor.getAccountLeftEnergyFromFreeze
// and the ResourceProcessor recovery/increase machinery.
//
// CONSENSUS-CRITICAL. Every constant, cast, and rounding step mirrors java-tron so the
// derived value is byte-identical:
//   - EnergyProcessor.getAccountLeftEnergyFromFreeze / calculateGlobalEnergyLimit[V2]
//     (chainbase .../core/db/EnergyProcessor.java)
//   - ResourceProcessor.increase / recovery / divideCeil / getUsage
//     (chainbase .../core/db/ResourceProcessor.java)
//   - constants in Parameter.ChainConstant (common .../core/config/Parameter.java)
//
// The pure math takes every stateful input as an argument (see StakedEnergyInput); mapping
// a stored account + block + dynamic properties onto those inputs is the actuator's job
// (internal/actuator/energy.go), keeping this package free of proto/state imports.

const (
	// trxPrecision is Parameter.ChainConstant.TRX_PRECISION: sun per TRX. Staked balances
	// are in sun; the energy weight is measured in whole TRX.
	trxPrecision int64 = 1_000_000
	// usagePrecision is Parameter.ChainConstant.PRECISION, ResourceProcessor.precision: the
	// fixed-point scale the recovery average is carried at.
	usagePrecision int64 = 1_000_000
	// BlockProducedIntervalMs is Parameter.ChainConstant.BLOCK_PRODUCED_INTERVAL (ms/block);
	// a "slot" is one such interval since genesis. Exported for the actuator's getHeadSlot.
	BlockProducedIntervalMs int64 = 3000
	// windowSizeMs is Parameter.ChainConstant.WINDOW_SIZE_MS: the 24h recovery window.
	windowSizeMs int64 = 86_400_000
	// windowSizePrecision is Parameter.ChainConstant.WINDOW_SIZE_PRECISION: the scale a
	// window-optimized per-account window is stored at.
	windowSizePrecision int64 = 1000
	// DefaultEnergyWindow is the recovery window in slots when an account has none set:
	// WINDOW_SIZE_MS / BLOCK_PRODUCED_INTERVAL = 28800 (a full day of 3s slots).
	DefaultEnergyWindow int64 = windowSizeMs / BlockProducedIntervalMs
)

// StakedEnergyInput is the full set of inputs java-tron's getAccountLeftEnergyFromFreeze
// reads. Per-account fields come from the stored core.Account; the Total* / Support* /
// AllowNewReward globals come from the DynamicPropertiesStore; NowSlot / LatestConsumeSlot
// are genesis-relative block slots (getHeadSlot semantics — the genesis offset cancels in
// the recovery delta, so callers must derive both from the same genesis).
type StakedEnergyInput struct {
	// FrozenBalanceForEnergy is getAllFrozenBalanceForEnergy() in sun: own V1+V2 energy
	// stake plus energy delegated TO this account (V1+V2). See actuator extraction.
	FrozenBalanceForEnergy int64
	// EnergyUsage is AccountResource.energy_usage at the last consume (pre-recovery).
	EnergyUsage int64
	// LatestConsumeSlot is AccountResource.latest_consume_time_for_energy (a block slot).
	LatestConsumeSlot int64
	// WindowSize is getWindowSize(ENERGY) in slots (already de-scaled — see EnergyWindow).
	WindowSize int64
	// NowSlot is the current head slot (getHeadSlot()).
	NowSlot int64

	// TotalEnergyCurrentLimit is DynamicPropertiesStore.getTotalEnergyCurrentLimit().
	TotalEnergyCurrentLimit int64
	// TotalEnergyWeight is DynamicPropertiesStore.getTotalEnergyWeight() (whole-TRX weight).
	TotalEnergyWeight int64
	// SupportUnfreezeDelay selects the Stake2.0 weight formula (calculateGlobalEnergyLimitV2).
	SupportUnfreezeDelay bool
	// AllowNewReward gates the V1 totalEnergyWeight<=0 -> 0 guard.
	AllowNewReward bool
}

// AvailableStakedEnergy returns max(globalEnergyLimit - recoveredUsage, 0), faithful to
// EnergyProcessor.getAccountLeftEnergyFromFreeze.
func AvailableStakedEnergy(in StakedEnergyInput) int64 {
	limit := globalEnergyLimit(in)
	used := recoverEnergyUsage(in.EnergyUsage, in.LatestConsumeSlot, in.NowSlot, in.WindowSize)
	if left := limit - used; left > 0 {
		return left
	}
	return 0
}

// globalEnergyLimit mirrors EnergyProcessor.calculateGlobalEnergyLimit and its V2 variant.
// V1: staked TRX must reach at least 1 whole TRX; the weight is an integer TRX count.
// V2 (Stake2.0/supportUnfreezeDelay): the weight is a fractional TRX amount. Both scale by
// the global totalEnergyLimit/totalEnergyWeight ratio, computed in float64 exactly as
// java-tron's `(long)(weight * ((double) totalLimit / totalWeight))`.
func globalEnergyLimit(in StakedEnergyInput) int64 {
	froze := in.FrozenBalanceForEnergy
	if in.SupportUnfreezeDelay {
		if in.TotalEnergyWeight == 0 {
			return 0
		}
		weight := float64(froze) / float64(trxPrecision)
		return int64(weight * (float64(in.TotalEnergyCurrentLimit) / float64(in.TotalEnergyWeight)))
	}
	if froze < trxPrecision {
		return 0
	}
	// V1: allowNewReward guards a zero/negative weight; without it java-tron asserts >0
	// (a live-node invariant). We clamp to 0 to stay total.
	if in.TotalEnergyWeight <= 0 {
		return 0
	}
	weight := froze / trxPrecision
	return int64(float64(weight) * (float64(in.TotalEnergyCurrentLimit) / float64(in.TotalEnergyWeight)))
}

// IncreaseEnergyUsage mirrors ResourceProcessor.increase(lastUsage, usage, lastTime, now,
// windowSize): decay the stored usage average forward from its last-consume slot to now
// (linear within one window, fully recovered past it), add the new consumption's average,
// and convert back to energy units. It is the write-back arithmetic for
// EnergyProcessor.useEnergy — the new stored energy_usage after spending `add` staked
// energy at nowSlot. CONSENSUS-CRITICAL.
func IncreaseEnergyUsage(lastUsage, add, lastSlot, nowSlot, windowSize int64) int64 {
	if windowSize <= 0 {
		windowSize = DefaultEnergyWindow
	}
	averageLastUsage := divideCeil(lastUsage*usagePrecision, windowSize)
	averageAdd := divideCeil(add*usagePrecision, windowSize)
	if lastSlot != nowSlot {
		if lastSlot+windowSize > nowSlot {
			delta := nowSlot - lastSlot
			decay := float64(windowSize-delta) / float64(windowSize)
			averageLastUsage = roundHalfUp(float64(averageLastUsage) * decay)
		} else {
			averageLastUsage = 0
		}
	}
	averageLastUsage += averageAdd
	return averageLastUsage * windowSize / usagePrecision
}

// recoverEnergyUsage decays a stored energy_usage forward from its last-consume slot to
// now — increase with nothing added (ResourceProcessor.recovery).
func recoverEnergyUsage(lastUsage, lastSlot, nowSlot, windowSize int64) int64 {
	return IncreaseEnergyUsage(lastUsage, 0, lastSlot, nowSlot, windowSize)
}

// EnergyWindow mirrors AccountCapsule.getWindowSize(ENERGY): the stored per-account window,
// de-scaled when window-optimized, defaulting to a full day of slots.
func EnergyWindow(storedWindow int64, optimized bool) int64 {
	if storedWindow == 0 {
		return DefaultEnergyWindow
	}
	if optimized {
		if storedWindow < windowSizePrecision {
			return DefaultEnergyWindow
		}
		return storedWindow / windowSizePrecision
	}
	return storedWindow
}

// divideCeil is ResourceProcessor.divideCeil: ceil(numerator/denominator) for the
// non-negative operands this package produces.
func divideCeil(numerator, denominator int64) int64 {
	q := numerator / denominator
	if numerator%denominator > 0 {
		q++
	}
	return q
}

// roundHalfUp mirrors Maths.round (java.lang.Math.round): floor(x + 0.5). The recovery
// inputs are always non-negative, so this matches java-tron's round-half-up exactly.
func roundHalfUp(x float64) int64 {
	return int64(math.Floor(x + 0.5))
}
