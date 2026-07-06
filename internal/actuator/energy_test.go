package actuator

import (
	"testing"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func TestAllFrozenBalanceForEnergy(t *testing.T) {
	a := &core.Account{
		FrozenV2: []*core.Account_FreezeV2{
			{Type: core.ResourceCode_ENERGY, Amount: 6_000_000},
			{Type: core.ResourceCode_BANDWIDTH, Amount: 999_999}, // must be ignored
		},
		AccountResource: &core.Account_AccountResource{
			FrozenBalanceForEnergy:                  &core.Account_Frozen{FrozenBalance: 3_000_000},
			AcquiredDelegatedFrozenBalanceForEnergy: 1_000_000,
			// V2 delegated-in left 0 to keep the sum readable.
		},
	}
	// 3_000_000 V1 self + 1_000_000 V1 delegated-in + 6_000_000 V2 self = 10_000_000 sun.
	if got := allFrozenBalanceForEnergy(a); got != 10_000_000 {
		t.Fatalf("allFrozenBalanceForEnergy = %d, want 10_000_000", got)
	}
}

func TestAvailableStakedEnergy(t *testing.T) {
	// With energy_usage 0 the recovery term is 0 regardless of the block slot, so the
	// available energy equals the global limit: 10 TRX * (50e9 / 100000) = 5_000_000.
	a := &core.Account{
		FrozenV2: []*core.Account_FreezeV2{{Type: core.ResourceCode_ENERGY, Amount: 10_000_000}},
		AccountResource: &core.Account_AccountResource{
			EnergyUsage: 0,
		},
	}
	props := energyDynamicProps{totalEnergyCurrentLimit: 50_000_000_000, totalEnergyWeight: 100_000}
	if got := availableStakedEnergy(a, 1_600_000_000_000, props); got != 5_000_000 {
		t.Fatalf("availableStakedEnergy = %d, want 5_000_000", got)
	}

	// A nil account (never seen on chain) has no stake.
	if got := availableStakedEnergy(nil, 1_600_000_000_000, props); got != 0 {
		t.Fatalf("availableStakedEnergy(nil) = %d, want 0", got)
	}

	// The deferred (zero) dynamic-property placeholder yields 0 — preserving current
	// vm.go behavior until the DynamicPropertiesStore lands.
	if got := availableStakedEnergy(a, 1_600_000_000_000, deferredEnergyDynamicProps); got != 0 {
		t.Fatalf("availableStakedEnergy(deferred props) = %d, want 0", got)
	}
}
