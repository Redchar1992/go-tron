package node

import (
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/Redchar1992/go-tron/internal/actuator"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/genesis"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/replay"
)

func addr21c(b byte) []byte {
	a := make([]byte, 21)
	a[0] = 0x41
	a[20] = b
	return a
}

// newSeededManager returns a genesis-initialized Manager (single funded witness) for seeding.
func newSeededManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(db.NewDatabase(db.NewMemKV()), 0)
	cfg := &genesis.Config{Timestamp: 1_600_000_000_000, Number: 0, ParentHash: "00"}
	if err := m.InitGenesis(cfg); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSeedChainStateGlobalsAndAccount(t *testing.T) {
	m := newSeededManager(t)
	owner := addr21c(0x41)

	cs := &replay.ChainState{
		Number: 50_000_000,
		DynamicProps: replay.DynamicProps{
			TotalEnergyWeight: 1_000_000, TotalEnergyCurrentLimit: 90_000_000_000,
			EnergyFee: 420,
		},
		Accounts: map[string]replay.AccountResource{
			hex.EncodeToString(owner): {
				Balance: 5_000_000, FrozenBalanceForEnergy: 10_000_000,
				EnergyUsage: 123, LatestConsumeTimeForEnergy: 456,
			},
		},
	}
	if err := m.SeedChainState(cs); err != nil {
		t.Fatal(err)
	}

	st := m.State()
	if w, _ := st.Properties.TotalEnergyWeight(); w != 1_000_000 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 1_000_000", w)
	}
	if l, _ := st.Properties.TotalEnergyCurrentLimit(); l != 90_000_000_000 {
		t.Fatalf("TOTAL_ENERGY_CURRENT_LIMIT = %d, want 90e9", l)
	}
	if f, _ := st.Properties.EnergyFee(); f != 420 {
		t.Fatalf("ENERGY_FEE = %d, want 420", f)
	}
	acct, err := st.Accounts.Get(owner)
	if err != nil {
		t.Fatal(err)
	}
	ar := acct.GetAccountResource()
	if ar.GetFrozenBalanceForEnergy().GetFrozenBalance() != 10_000_000 {
		t.Fatalf("seeded V1 energy stake = %d", ar.GetFrozenBalanceForEnergy().GetFrozenBalance())
	}
	if ar.GetEnergyUsage() != 123 || ar.GetLatestConsumeTimeForEnergy() != 456 {
		t.Fatalf("seeded usage/consume = %d/%d", ar.GetEnergyUsage(), ar.GetLatestConsumeTimeForEnergy())
	}
	// A zero field (TotalNetWeight) was left at the genesis default (0 here anyway) — verify
	// the current-limit was NOT clobbered to zero by an unset field elsewhere.
	if l, _ := st.Properties.TotalEnergyCurrentLimit(); l == 0 {
		t.Fatal("current limit must not be zeroed")
	}
}

// TestSeedChainStatePowersHistoricalReplay is the §4.1 payoff: after seeding a height's
// network weight + a caller's stake + the era's ENERGY_FEE, a contract call from that caller
// is covered by staked energy at the SEEDED price — the derivation running on injected
// historical state, not a self-driven freeze.
func TestSeedChainStatePowersHistoricalReplay(t *testing.T) {
	m := newSeededManager(t)
	st := m.State()
	owner := addr21c(0x42)
	contract := addr21c(0xc7)

	// Deploy a contract (SSTORE-on-trigger) directly into state.
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	if err := st.Accounts.Put(&core.Account{Address: contract, Type: core.AccountType_Contract}); err != nil {
		t.Fatal(err)
	}
	if err := st.Contracts.PutCode(contract, runtime); err != nil {
		t.Fatal(err)
	}

	// Seed the caller: 1000 TRX staked for energy, weight 1000, era price 420, and the header
	// timestamp advanced (so the caller's zero consume-slot recovers fully by "now").
	cs := &replay.ChainState{
		DynamicProps: replay.DynamicProps{
			TotalEnergyWeight: 1000, TotalEnergyCurrentLimit: 90_000_000_000, EnergyFee: 420,
		},
		Accounts: map[string]replay.AccountResource{
			hex.EncodeToString(owner): {Balance: 1_000_000_000, FrozenBalanceForEnergy: 1_000_000_000},
		},
	}
	if err := m.SeedChainState(cs); err != nil {
		t.Fatal(err)
	}
	if err := st.Properties.SaveLatestBlockHeaderTimestamp(1_600_000_100_000); err != nil {
		t.Fatal(err)
	}

	// Trigger the contract from the seeded caller.
	p, err := anypb.New(&core.TriggerSmartContract{OwnerAddress: owner, ContractAddress: contract})
	if err != nil {
		t.Fatal(err)
	}
	tx := &core.Transaction{RawData: &core.TransactionRaw{
		Contract: []*core.Transaction_Contract{{Type: core.Transaction_Contract_TriggerSmartContract, Parameter: p}},
		FeeLimit: 1_000_000_000,
	}}
	res, err := actuator.Apply(st, tx, actuator.BlockContext{Number: 50_000_000, Timestamp: 1_600_000_103_000, Version: 35})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.Receipts) != 1 {
		t.Fatalf("want 1 receipt, got %d", len(res.Receipts))
	}
	bill := res.Receipts[0].Energy
	if bill.EnergyUsage <= 0 || bill.EnergyFee != 0 {
		t.Fatalf("seeded-state trigger want stake-covered, got %+v", bill)
	}
}
