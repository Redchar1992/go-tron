package resource

// Receipt is the energy portion of a transaction receipt — the fields reported by
// gettransactioninfobyid. The identity that always holds:
//
//	EnergyUsageTotal == EnergyUsage + OriginEnergyUsage + EnergyFee/EnergyPrice
//
// and the transaction's total burned fee = EnergyFee + (bandwidth) net_fee.
type Receipt struct {
	EnergyUsage       int64 // energy paid from the caller's staked energy
	OriginEnergyUsage int64 // energy paid from the contract origin's staked energy
	EnergyFee         int64 // sun burned by the caller for energy not covered by staking
	EnergyUsageTotal  int64 // total energy consumed (usage + origin + burned)
}

// Bill is the input to the energy split: the energy the VM consumed plus the resource
// state that determines who pays. Energy quantities are in energy units; EnergyPrice is
// the dynamic ENERGY_FEE value in sun per energy (the effective price applies a 100-sun
// floor — see EnergyPrice).
type Bill struct {
	EnergyUsed         int64 // raw energy the VM consumed (energy_usage_total before split)
	CallerEnergy       int64 // caller's currently-available staked energy
	OriginEnergy       int64 // contract origin's currently-available staked energy
	CallerIsOrigin     bool  // true when the caller IS the contract origin (no split)
	ConsumeUserPercent int64 // contract consume_user_resource_percent (0..100), caller's %
	OriginEnergyLimit  int64 // contract origin_energy_limit (cap on the origin's share)
	EnergyPrice        int64 // dynamic ENERGY_FEE (sun per energy; 0 -> 100-sun floor)
}

// SunPerEnergyFloor is java-tron's Constant.SUN_PER_ENERGY: the minimum energy price the
// burn uses, even if the dynamic ENERGY_FEE is lower/unset.
const SunPerEnergyFloor int64 = 100

// energyPrice constants observed on mainnet over time (sun per energy). The active value
// is a dynamic property; these document the history for fixtures/tests.
const (
	EnergyPriceV1 int64 = 100 // genesis-era
	EnergyPriceV2 int64 = 140
	EnergyPriceV3 int64 = 280
	EnergyPriceV4 int64 = 420 // 2023 adjustment
)

// EnergyPrice returns the effective sun-per-energy price: max(100, dynamicFee), matching
// java-tron's `max(Constant.SUN_PER_ENERGY, getEnergyFee())`.
func EnergyPrice(dynamicFee int64) int64 {
	if dynamicFee > SunPerEnergyFloor {
		return dynamicFee
	}
	return SunPerEnergyFloor
}

// Compute splits the VM's energy bill into the receipt fields, faithful to java-tron's
// ReceiptCapsule.payEnergyBill:
//
//   - The origin (developer) pays percent = 100 - consume_user_resource_percent of the
//     total, from its staked energy only, capped by min(originFrozen, originEnergyLimit) —
//     the origin never burns TRX.
//   - The caller pays the rest: from its staked energy first, then by burning TRX at the
//     effective energy price for any remainder.
//
// The identity EnergyUsageTotal = EnergyUsage + OriginEnergyUsage + EnergyFee/price holds.
func (b Bill) Compute() Receipt {
	total := b.EnergyUsed
	if total <= 0 {
		return Receipt{}
	}
	price := EnergyPrice(b.EnergyPrice)

	var originUsage int64
	if !b.CallerIsOrigin {
		percent := 100 - b.ConsumeUserPercent
		if percent < 0 {
			percent = 0
		}
		originUsage = total * percent / 100
		if cap := min(b.OriginEnergy, b.OriginEnergyLimit); originUsage > cap {
			originUsage = cap
		}
	}

	callerUsage := total - originUsage
	var energyUsage, energyFee int64
	if b.CallerEnergy >= callerUsage {
		energyUsage = callerUsage
	} else {
		energyUsage = b.CallerEnergy
		energyFee = (callerUsage - b.CallerEnergy) * price
	}

	return Receipt{
		EnergyUsage:       energyUsage,
		OriginEnergyUsage: originUsage,
		EnergyFee:         energyFee,
		EnergyUsageTotal:  total,
	}
}

// AccountEnergyLimit returns the pre-execution energy budget for one account: its staked
// energy plus what its spendable balance (balance - callValue) can buy at the energy
// price, bounded by what feeLimit can buy. Faithful to getAccountEnergyLimitWithFixRatio.
func AccountEnergyLimit(frozenEnergy, balance, callValue, feeLimit, dynamicFee int64) int64 {
	price := EnergyPrice(dynamicFee)
	spendable := balance - callValue
	if spendable < 0 {
		spendable = 0
	}
	available := frozenEnergy + spendable/price
	return min(available, feeLimit/price)
}
