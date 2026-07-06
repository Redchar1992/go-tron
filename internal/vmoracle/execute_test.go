package vmoracle

import (
	"encoding/hex"
	"strings"
	"testing"
)

// hexAddr builds a 21-byte 0x41 TRON address ending in b, as lowercase hex.
func hexAddr(b byte) string {
	a := make([]byte, 21)
	a[0] = 0x41
	a[1] = 0xC0
	a[20] = b
	return hex.EncodeToString(a)
}

// storeLogReturn is runtime bytecode that: SSTOREs 0x2a at slot 0; emits LOG1(data=0x2a,
// topic=0xcc..cc); RETURNs the 32-byte word 0x2a.
func storeLogReturn() string {
	topic := strings.Repeat("cc", 32)
	code := "" +
		"602a" + "6000" + "55" + // PUSH1 2a; PUSH1 0; SSTORE
		"602a" + "6000" + "52" + // PUSH1 2a; PUSH1 0; MSTORE
		"7f" + topic + "6020" + "6000" + "a1" + // PUSH32 topic; PUSH1 32; PUSH1 0; LOG1
		"6020" + "6000" + "f3" // PUSH1 32; PUSH1 0; RETURN
	return code
}

// baseWorld builds a World with a contract deployed and an owner funded, at a given energy fee
// / staking config.
func baseWorld(code string, ownerStake, weight, currentLimit int64) (World, Tx) {
	owner, contract := hexAddr(0x01), hexAddr(0x02)
	w := World{
		Version: 23,
		DynamicProps: DynamicProps{
			TotalEnergyWeight:       weight,
			TotalEnergyCurrentLimit: currentLimit,
			EnergyFee:               100,
		},
		Block: Block{Number: 5_000_000, Timestamp: 1_600_000_000_000, Witness: hexAddr(0x09)},
		Accounts: map[string]Account{
			owner:    {Balance: 1_000_000_000, EnergyStake: ownerStake},
			contract: {Code: code},
		},
	}
	tx := Tx{Type: "TriggerSmartContract", Owner: owner, Contract: contract, FeeLimit: 1_000_000_000}
	return w, tx
}

func TestExecuteTriggerStorageLogReturn(t *testing.T) {
	w, tx := baseWorld(storeLogReturn(), 0, 0, 0) // no stake -> caller burns TRX
	got, err := Execute(w, tx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Result != "SUCCESS" {
		t.Fatalf("result = %s (%s)", got.Result, got.VMError)
	}
	wantWord := strings.Repeat("0", 62) + "2a"
	if got.Return != wantWord {
		t.Fatalf("return = %s, want %s", got.Return, wantWord)
	}
	// storage: slot 0 set to 0x2a on the contract.
	slot0 := strings.Repeat("0", 64)
	if v := got.StorageWrites[hexAddr(0x02)][slot0]; v != wantWord {
		t.Fatalf("storage[contract][slot0] = %q, want %s", v, wantWord)
	}
	// one log: contract address, one topic 0xcc.., data 0x2a.
	if len(got.Logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(got.Logs))
	}
	l := got.Logs[0]
	if l.Address != hexAddr(0x02) || len(l.Topics) != 1 || l.Topics[0] != strings.Repeat("cc", 32) || l.Data != wantWord {
		t.Fatalf("log = %+v", l)
	}
	// no stake -> all energy burned as TRX: EnergyFee == EnergyUsed * price(100), no origin.
	if got.EnergyFee != got.EnergyUsed*100 {
		t.Fatalf("EnergyFee = %d, want EnergyUsed(%d)*100", got.EnergyFee, got.EnergyUsed)
	}
	if got.OriginEnergyUsage != 0 {
		t.Fatalf("OriginEnergyUsage = %d, want 0", got.OriginEnergyUsage)
	}
}

func TestExecuteRevertDiscardsEffects(t *testing.T) {
	// SSTORE 2a@0; LOG0; REVERT — all effects must be discarded, but energy is still charged.
	code := "602a" + "6000" + "55" + // SSTORE
		"6000" + "6000" + "a0" + // LOG0
		"6000" + "6000" + "fd" // PUSH1 0; PUSH1 0; REVERT
	w, tx := baseWorld(code, 0, 0, 0)
	got, err := Execute(w, tx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Result != "REVERT" {
		t.Fatalf("result = %s", got.Result)
	}
	if len(got.StorageWrites) != 0 {
		t.Fatalf("reverted tx must show no storage writes, got %v", got.StorageWrites)
	}
	if len(got.Logs) != 0 {
		t.Fatalf("reverted tx must emit no logs, got %d", len(got.Logs))
	}
	if got.EnergyUsed <= 0 {
		t.Fatalf("reverted tx still consumes energy, got %d", got.EnergyUsed)
	}
}

func TestExecuteStakedEnergyCoversBurn(t *testing.T) {
	// 1 TRX stake, weight 1, currentLimit 1e9 -> callerEnergy = 1e9, covers the small run:
	// no TRX burned, energy paid from stake.
	w, tx := baseWorld(storeLogReturn(), 1_000_000, 1, 1_000_000_000)
	got, err := Execute(w, tx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Result != "SUCCESS" {
		t.Fatalf("result = %s (%s)", got.Result, got.VMError)
	}
	if got.EnergyFee != 0 {
		t.Fatalf("staked energy should cover the burn: EnergyFee = %d, want 0", got.EnergyFee)
	}
}
