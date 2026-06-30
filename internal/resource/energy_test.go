package resource

import "testing"

// identity asserts the receipt's energy_usage_total identity holds.
func checkIdentity(t *testing.T, r Receipt, price int64) {
	t.Helper()
	if r.EnergyFee%price != 0 {
		t.Fatalf("energy_fee %d not a multiple of price %d", r.EnergyFee, price)
	}
	got := r.EnergyUsage + r.OriginEnergyUsage + r.EnergyFee/price
	if got != r.EnergyUsageTotal {
		t.Fatalf("identity broken: usage(%d)+origin(%d)+fee/price(%d) = %d != total %d",
			r.EnergyUsage, r.OriginEnergyUsage, r.EnergyFee/price, got, r.EnergyUsageTotal)
	}
}

func TestEnergyPriceFloor(t *testing.T) {
	if EnergyPrice(0) != 100 || EnergyPrice(50) != 100 || EnergyPrice(420) != 420 {
		t.Fatal("EnergyPrice floor wrong")
	}
}

// TestCallerPaysAllStaked: P=100 (caller pays all), caller has enough staked energy.
func TestCallerPaysAllStaked(t *testing.T) {
	r := Bill{EnergyUsed: 1000, CallerEnergy: 5000, ConsumeUserPercent: 100, EnergyPrice: 420}.Compute()
	if r.EnergyUsage != 1000 || r.OriginEnergyUsage != 0 || r.EnergyFee != 0 {
		t.Fatalf("got %+v", r)
	}
	checkIdentity(t, r, 420)
}

// TestCallerBurnsTrx: caller has no staked energy -> whole total is burned at price.
// Mirrors the real receipt at mainnet block 50,000,000: 31895 energy, price 420 ->
// energy_fee = 13,395,900.
func TestCallerBurnsTrx(t *testing.T) {
	r := Bill{EnergyUsed: 31895, CallerEnergy: 0, ConsumeUserPercent: 100, EnergyPrice: 420}.Compute()
	if r.EnergyUsage != 0 || r.OriginEnergyUsage != 0 {
		t.Fatalf("got %+v", r)
	}
	if r.EnergyFee != 13_395_900 {
		t.Fatalf("energy_fee = %d, want 13395900 (matches block 50M receipt)", r.EnergyFee)
	}
	checkIdentity(t, r, 420)
}

// TestPartialStakeThenBurn: caller covers part from staking, burns the rest.
func TestPartialStakeThenBurn(t *testing.T) {
	r := Bill{EnergyUsed: 1000, CallerEnergy: 600, ConsumeUserPercent: 100, EnergyPrice: 280}.Compute()
	if r.EnergyUsage != 600 {
		t.Fatalf("energy_usage = %d, want 600", r.EnergyUsage)
	}
	if r.EnergyFee != 400*280 {
		t.Fatalf("energy_fee = %d, want %d", r.EnergyFee, 400*280)
	}
	checkIdentity(t, r, 280)
}

// TestOriginSharePaid: P=40 -> origin pays 60%, capped by its staked energy & limit.
func TestOriginSharePaid(t *testing.T) {
	// total 1000, origin share 60% = 600, origin frozen 5000 & limit 5000 -> origin pays 600.
	// caller pays 400 from its 1000 staked -> no burn.
	r := Bill{
		EnergyUsed: 1000, CallerEnergy: 1000, OriginEnergy: 5000, OriginEnergyLimit: 5000,
		ConsumeUserPercent: 40, EnergyPrice: 420,
	}.Compute()
	if r.OriginEnergyUsage != 600 || r.EnergyUsage != 400 || r.EnergyFee != 0 {
		t.Fatalf("got %+v, want origin=600 caller=400 fee=0", r)
	}
	checkIdentity(t, r, 420)
}

// TestOriginCappedByLimit: origin's 60% share (600) is capped to originEnergyLimit=200;
// the caller covers the remaining 800 (from 500 staked + 300 burned).
func TestOriginCappedByLimit(t *testing.T) {
	r := Bill{
		EnergyUsed: 1000, CallerEnergy: 500, OriginEnergy: 5000, OriginEnergyLimit: 200,
		ConsumeUserPercent: 40, EnergyPrice: 100,
	}.Compute()
	if r.OriginEnergyUsage != 200 {
		t.Fatalf("origin_energy_usage = %d, want 200 (capped by limit)", r.OriginEnergyUsage)
	}
	if r.EnergyUsage != 500 || r.EnergyFee != 300*100 {
		t.Fatalf("got %+v, want caller_usage=500 fee=30000", r)
	}
	checkIdentity(t, r, 100)
}

// TestCallerIsOrigin: creator calling its own contract -> no split, caller pays all.
func TestCallerIsOrigin(t *testing.T) {
	r := Bill{
		EnergyUsed: 1000, CallerEnergy: 100, CallerIsOrigin: true,
		ConsumeUserPercent: 0, OriginEnergy: 9999, OriginEnergyLimit: 9999, EnergyPrice: 420,
	}.Compute()
	if r.OriginEnergyUsage != 0 {
		t.Fatalf("origin usage must be 0 when caller is origin, got %d", r.OriginEnergyUsage)
	}
	if r.EnergyUsage != 100 || r.EnergyFee != 900*420 {
		t.Fatalf("got %+v", r)
	}
	checkIdentity(t, r, 420)
}

func TestAccountEnergyLimitFeeLimitCap(t *testing.T) {
	// frozen 1000; balance 1e9, callValue 0; feeLimit 100000 sun at price 420.
	// energyFromBalance = 1e9/420 = 2,380,952; available = 1000+that; feeLimit/420 = 238.
	// min -> 238.
	got := AccountEnergyLimit(1000, 1_000_000_000, 0, 100_000, 420)
	if got != 100_000/420 {
		t.Fatalf("energy limit = %d, want %d (feeLimit-bound)", got, 100_000/420)
	}
}

func TestZeroEnergyNoReceipt(t *testing.T) {
	r := Bill{EnergyUsed: 0, EnergyPrice: 420}.Compute()
	if r != (Receipt{}) {
		t.Fatalf("zero energy must yield empty receipt, got %+v", r)
	}
}
