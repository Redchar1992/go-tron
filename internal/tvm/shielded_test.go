package tvm

import (
	"errors"
	"testing"
)

func TestShieldedGating(t *testing.T) {
	addrs := []uint64{0x1000001, 0x1000002, 0x1000003, 0x1000004}

	// Gate off (default / mainnet / from-genesis): these addresses are NOT precompiles, so a
	// CALL to them is an ordinary account call — bit-for-bit faithful to getContractForAddr
	// returning null.
	off := VMConfig{AllowSolidity059: true} // shielded gate implicitly false
	for _, a := range addrs {
		if pc := lookupPrecompile(precompileAddr(a), off, nil); pc != nil {
			t.Fatalf("addr %#x should not be a precompile with the gate off", a)
		}
	}

	// Gate on: resolves to the deferred contract, which fails closed and charges java-tron's
	// fixed energy.
	on := VMConfig{AllowShieldedTRC20Transaction: true}
	wantEnergy := map[uint64]uint64{
		0x1000001: energyVerifyMintProof,
		0x1000002: energyVerifyTransferProof,
		0x1000003: energyVerifyBurnProof,
		0x1000004: energyMerkleHash,
	}
	for _, a := range addrs {
		pc := lookupPrecompile(precompileAddr(a), on, nil)
		if pc == nil {
			t.Fatalf("addr %#x should resolve with the gate on", a)
		}
		if got := pc.RequiredEnergy(nil); got != wantEnergy[a] {
			t.Fatalf("addr %#x energy = %d, want %d", a, got, wantEnergy[a])
		}
		if _, err := pc.Run(make([]byte, 1504)); !errors.Is(err, errShieldedUnsupported) {
			t.Fatalf("addr %#x should fail closed, got err %v", a, err)
		}
	}
}
