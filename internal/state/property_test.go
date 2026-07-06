package state

import (
	"testing"

	"github.com/Redchar1992/go-tron/internal/db"
)

func newTestProperties(t *testing.T) *PropertyStore {
	t.Helper()
	return New(db.NewDatabase(db.NewMemKV())).Properties
}

func TestPropertyInt64RoundTrip(t *testing.T) {
	p := newTestProperties(t)
	key := []byte("SOME_PROP")

	// Absent key: (0, false).
	if v, ok, err := p.GetInt64(key); err != nil || ok || v != 0 {
		t.Fatalf("absent GetInt64 = (%d,%v,%v), want (0,false,nil)", v, ok, err)
	}
	// A negative value must round-trip too (java-tron stores signed longs).
	for _, want := range []int64{0, 1, 50_000_000_000, -7, 1<<62 + 3} {
		if err := p.PutInt64(key, want); err != nil {
			t.Fatalf("PutInt64(%d): %v", want, err)
		}
		v, ok, err := p.GetInt64(key)
		if err != nil || !ok || v != want {
			t.Fatalf("GetInt64 after Put(%d) = (%d,%v,%v)", want, v, ok, err)
		}
	}
}

func TestPropertyEnergyDefaults(t *testing.T) {
	// Before seeding, the typed accessors return java-tron's genesis defaults via getOr.
	p := newTestProperties(t)
	if v, err := p.TotalEnergyWeight(); err != nil || v != 0 {
		t.Fatalf("default TotalEnergyWeight = (%d,%v), want 0", v, err)
	}
	if v, err := p.TotalEnergyCurrentLimit(); err != nil || v != DefaultTotalEnergyLimit {
		t.Fatalf("default TotalEnergyCurrentLimit = (%d,%v), want %d", v, err, DefaultTotalEnergyLimit)
	}
	if v, err := p.SupportUnfreezeDelay(); err != nil || v {
		t.Fatalf("default SupportUnfreezeDelay = (%v,%v), want false", v, err)
	}
	if v, err := p.AllowNewReward(); err != nil || v {
		t.Fatalf("default AllowNewReward = (%v,%v), want false", v, err)
	}
}

func TestPropertySeedGenesisDefaults(t *testing.T) {
	p := newTestProperties(t)
	if err := p.SeedGenesisDefaults(); err != nil {
		t.Fatalf("SeedGenesisDefaults: %v", err)
	}
	// After seeding, the raw keys are materialized (ok=true) and hold the fresh-chain values.
	if v, ok, err := p.GetInt64(propTotalEnergyWeight); err != nil || !ok || v != 0 {
		t.Fatalf("seeded TOTAL_ENERGY_WEIGHT = (%d,%v,%v)", v, ok, err)
	}
	if v, ok, err := p.GetInt64(propTotalEnergyCurrentLimit); err != nil || !ok || v != DefaultTotalEnergyLimit {
		t.Fatalf("seeded TOTAL_ENERGY_CURRENT_LIMIT = (%d,%v,%v)", v, ok, err)
	}
	// Stake2.0 activation is off at genesis: V1 formula selected, new-reward guard off.
	if v, err := p.SupportUnfreezeDelay(); err != nil || v {
		t.Fatalf("seeded SupportUnfreezeDelay = (%v,%v), want false", v, err)
	}
}

// TestPropertyRevocable confirms a property write inside an open session is discarded on
// Revoke — the staked-energy globals must roll back with the rest of state on a bad block.
func TestPropertyRevocable(t *testing.T) {
	d := db.NewDatabase(db.NewMemKV())
	p := New(d).Properties
	if err := p.PutInt64(propTotalEnergyWeight, 100); err != nil {
		t.Fatal(err)
	}
	d.BuildSession()
	if err := p.PutInt64(propTotalEnergyWeight, 999); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := p.GetInt64(propTotalEnergyWeight); v != 999 {
		t.Fatalf("in-session TotalEnergyWeight = %d, want 999", v)
	}
	d.Revoke()
	if v, _, _ := p.GetInt64(propTotalEnergyWeight); v != 100 {
		t.Fatalf("after Revoke TotalEnergyWeight = %d, want 100", v)
	}
}
