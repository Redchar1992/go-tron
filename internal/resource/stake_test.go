package resource

import "testing"

// The expected values below are hand-computed from java-tron's formulas (see stake.go
// header) so the Go port is checked against the reference arithmetic, not against itself.

func TestRecoverEnergyUsage(t *testing.T) {
	const w = DefaultEnergyWindow // 28800
	tests := []struct {
		name                         string
		lastUsage, lastSlot, nowSlot int64
		window                       int64
		want                         int64
	}{
		// No time elapsed: usage round-trips back to itself (divideCeil then getUsage).
		{"no-time-elapsed", 1_000_000, 100, 100, w, 1_000_000},
		// Exactly one full window elapsed (lastSlot+window == now): fully recovered.
		{"full-window", 1_000_000, 0, w, w, 0},
		// More than a full window: fully recovered.
		{"beyond-window", 1_000_000, 0, w + 5000, w, 0},
		// Half a window elapsed: linear decay to half.
		{"half-window", 1_000_000, 0, w / 2, w, 500_000},
		// Zero usage stays zero.
		{"zero-usage", 0, 0, w / 2, w, 0},
		// windowSize<=0 falls back to the default day-window.
		{"default-window-fallback", 1_000_000, 0, w / 2, 0, 500_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := recoverEnergyUsage(tc.lastUsage, tc.lastSlot, tc.nowSlot, tc.window)
			if got != tc.want {
				t.Fatalf("recoverEnergyUsage(%d,%d,%d,%d) = %d, want %d",
					tc.lastUsage, tc.lastSlot, tc.nowSlot, tc.window, got, tc.want)
			}
		})
	}
}

func TestGlobalEnergyLimit(t *testing.T) {
	tests := []struct {
		name string
		in   StakedEnergyInput
		want int64
	}{
		// V1: below 1 TRX staked -> 0.
		{"v1-below-1trx", StakedEnergyInput{
			FrozenBalanceForEnergy: 999_999, TotalEnergyCurrentLimit: 50_000_000_000,
			TotalEnergyWeight: 100_000}, 0},
		// V1: 10 TRX of 100000-TRX total on a 50e9 budget -> 10 * 500000 = 5_000_000.
		{"v1-weight-ratio", StakedEnergyInput{
			FrozenBalanceForEnergy: 10_000_000, TotalEnergyCurrentLimit: 50_000_000_000,
			TotalEnergyWeight: 100_000}, 5_000_000},
		// V1: zero total weight -> 0 (guard).
		{"v1-zero-total-weight", StakedEnergyInput{
			FrozenBalanceForEnergy: 10_000_000, TotalEnergyCurrentLimit: 50_000_000_000,
			TotalEnergyWeight: 0}, 0},
		// V2: fractional 0.5 TRX is honored (V1 would floor to 0). 0.5 * 500000 = 250_000.
		{"v2-fractional-weight", StakedEnergyInput{
			SupportUnfreezeDelay: true, FrozenBalanceForEnergy: 500_000,
			TotalEnergyCurrentLimit: 50_000_000_000, TotalEnergyWeight: 100_000}, 250_000},
		// V2: zero total weight -> 0.
		{"v2-zero-total-weight", StakedEnergyInput{
			SupportUnfreezeDelay: true, FrozenBalanceForEnergy: 500_000,
			TotalEnergyCurrentLimit: 50_000_000_000, TotalEnergyWeight: 0}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := globalEnergyLimit(tc.in); got != tc.want {
				t.Fatalf("globalEnergyLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAvailableStakedEnergy(t *testing.T) {
	// 10 TRX -> 5_000_000 global limit; half-window recovery of a 1_000_000 usage leaves
	// 500_000 used, so 4_500_000 remains available.
	in := StakedEnergyInput{
		FrozenBalanceForEnergy:  10_000_000,
		EnergyUsage:             1_000_000,
		LatestConsumeSlot:       0,
		NowSlot:                 DefaultEnergyWindow / 2,
		WindowSize:              DefaultEnergyWindow,
		TotalEnergyCurrentLimit: 50_000_000_000,
		TotalEnergyWeight:       100_000,
	}
	if got := AvailableStakedEnergy(in); got != 4_500_000 {
		t.Fatalf("AvailableStakedEnergy = %d, want 4_500_000", got)
	}

	// Recovered usage exceeding the limit clamps to 0, never negative.
	in.FrozenBalanceForEnergy = 1_000_000 // 1 TRX -> limit 500_000
	in.EnergyUsage = 100_000_000          // huge fresh usage, no recovery (now==last)
	in.LatestConsumeSlot = 5
	in.NowSlot = 5
	if got := AvailableStakedEnergy(in); got != 0 {
		t.Fatalf("AvailableStakedEnergy (over-used) = %d, want 0", got)
	}
}

func TestEnergyWindow(t *testing.T) {
	tests := []struct {
		stored    int64
		optimized bool
		want      int64
	}{
		{0, false, DefaultEnergyWindow},  // unset -> default
		{28800, false, 28800},            // non-optimized stored verbatim
		{28_800_000, true, 28800},        // optimized -> de-scaled by 1000
		{500, true, DefaultEnergyWindow}, // optimized but < precision -> default
	}
	for _, tc := range tests {
		if got := EnergyWindow(tc.stored, tc.optimized); got != tc.want {
			t.Fatalf("EnergyWindow(%d,%v) = %d, want %d", tc.stored, tc.optimized, got, tc.want)
		}
	}
}
