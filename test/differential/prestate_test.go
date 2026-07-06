package differential

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Redchar1992/go-tron/internal/actuator"
	"github.com/Redchar1992/go-tron/internal/block"
	"github.com/Redchar1992/go-tron/internal/db"
	"github.com/Redchar1992/go-tron/internal/node"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/replay"
)

// replay.MapProvider must satisfy the VM's historical-state fall-through interface.
var _ actuator.StateProvider = (*replay.MapProvider)(nil)

// TestMidChainReplayWithProvider replays a contract transaction whose callee was deployed
// before the replay window: the contract's code + storage and the caller's balance exist
// ONLY in the historical-state provider, not the node stores. It proves the vmStateDB falls
// through to the provider (M3.5c) — the piece that unlocks real mid-chain contract replay.
func TestMidChainReplayWithProvider(t *testing.T) {
	owner := addr21(0x11)
	contract := addr21(0xcc)

	// Runtime: value = SLOAD(slot 0); SSTORE(slot 1, value). It reads pre-existing storage
	// (only available from the provider) and copies it to slot 1.
	runtime := []byte{0x60, 0x00, 0x54, 0x60, 0x01, 0x55, 0x00}

	ps := replay.PreState{Accounts: map[string]replay.PreAccount{
		hex.EncodeToString(owner): {Balance: 1_000_000_000_000},
		hex.EncodeToString(contract): {
			Code:    hex.EncodeToString(runtime),
			Storage: map[string]string{"00": "07"}, // slot 0 = 7
		},
	}}

	// Round-trip through JSON to exercise the offline loader (the archive-node capture path
	// writes this same shape).
	path := filepath.Join(t.TempDir(), "prestate.json")
	raw, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := replay.LoadPreState(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := replay.NewMapProvider(loaded)
	if err != nil {
		t.Fatal(err)
	}

	// Manager starts mid-chain (block 1000) with an empty store + the provider.
	m := node.NewManager(db.NewDatabase(db.NewMemKV()), 0)
	m.SetStateProvider(provider)
	var rec *actuator.Receipt
	m.SetReceiptSink(func(_ int64, rs []*actuator.Receipt) { rec = rs[0] })

	root := mkBlock(t, 1000, nil, nil)
	if err := m.Start(root); err != nil {
		t.Fatal(err)
	}
	rootID, _ := block.ID(root)

	b := mkBlock(t, 1001, rootID, []*core.Transaction{triggerTx(t, owner, contract, nil)})
	if err := m.PushBlock(b); err != nil {
		t.Fatalf("mid-chain trigger: %v", err)
	}
	if rec == nil || rec.Reverted {
		t.Fatalf("trigger failed/reverted: %+v", rec)
	}
	if rec.Energy.EnergyUsageTotal <= 0 {
		t.Fatalf("no energy consumed — code not loaded from provider? %+v", rec.Energy)
	}

	// The runtime read slot 0 (=7, from the provider) and wrote it to slot 1. If either the
	// code or the pre-state storage had NOT come from the provider, slot 1 would be 0.
	var slot1 [32]byte
	slot1[31] = 1
	v, present, _ := m.State().Storage.Get(contract, slot1)
	if !present || v[31] != 0x07 {
		t.Fatalf("slot 1 = %x present=%v, want ...07 (code+storage came from the provider)", v, present)
	}
}
