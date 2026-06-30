// Package resource models TRON's ENERGY accounting for contract transactions — the
// post-execution step that turns the TVM's raw energy consumption into the receipt fields
// reported on-chain (energy_usage, origin_energy_usage, energy_fee, energy_usage_total).
//
// It is the energy counterpart to internal/bandwidth (which models the "net" resource).
// CONSENSUS-CRITICAL: the caller/origin split, the staked-energy usage, and the TRX burn
// must match java-tron (TransactionTrace.setBill / pay, EnergyProcessor) byte-for-byte.
//
// Scope (M3.3): the deterministic energy BILL — given the energy the VM consumed plus the
// caller's and origin's available staked energy, the contract's consume_user_resource_
// percent and origin_energy_limit, the energy price, and the fee limit, compute the split
// and burn. The stateful inputs (how much staked energy each account currently has — the
// FreezeV2 stake, daily free limit, and time-based recovery) are taken as inputs here;
// deriving them from account/dynamic-property state is a later step.
package resource
