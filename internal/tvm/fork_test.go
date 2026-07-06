package tvm

import (
	"errors"
	"testing"

	"github.com/holiman/uint256"
)

// runWithCfg executes code under a specific VMConfig (fork era).
func runWithCfg(code []byte, limit uint64, cfg VMConfig) *Result {
	s := NewMemStateDB()
	self := addr(0x51)
	s.SetCode(self, code)
	c := &Contract{
		Self: self, CodeAddr: self, Caller: addr(0x52), Origin: addr(0x52),
		Value: new(uint256.Int), Code: code,
	}
	evm := NewEVM(s, BlockContext{ChainID: uint256From(728126428)}, cfg)
	return evm.Execute(c, nil, limit)
}

// TestShrGatedByConstantinople: SHR (0x1c) faults before Constantinople, works after.
func TestShrGatedByConstantinople(t *testing.T) {
	// PUSH1 2; PUSH1 1; SHR; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN  -> 2>>1 = 1
	code := []byte{0x60, 2, 0x60, 1, 0x1c, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}

	pre := runWithCfg(code, 100000, VMConfig{})
	if !errors.Is(pre.Err, ErrInvalidOpcode) {
		t.Fatalf("pre-Constantinople SHR err = %v, want ErrInvalidOpcode", pre.Err)
	}

	post := runWithCfg(code, 100000, ConstantinopleVMConfig())
	if post.Err != nil {
		t.Fatalf("post-Constantinople SHR err = %v", post.Err)
	}
	if lastByte(post.Return) != 1 {
		t.Fatalf("2>>1 = %x, want 1", post.Return)
	}
}

// TestChainidGatedByIstanbul: CHAINID (0x46) faults before Istanbul, works after.
func TestChainidGatedByIstanbul(t *testing.T) {
	// CHAINID; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
	code := []byte{0x46, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}

	// Constantinople does not yet enable CHAINID.
	pre := runWithCfg(code, 100000, ConstantinopleVMConfig())
	if !errors.Is(pre.Err, ErrInvalidOpcode) {
		t.Fatalf("pre-Istanbul CHAINID err = %v, want ErrInvalidOpcode", pre.Err)
	}

	post := runWithCfg(code, 100000, LatestVMConfig())
	if post.Err != nil {
		t.Fatalf("post-Istanbul CHAINID err = %v", post.Err)
	}
	var got uint256.Int
	got.SetBytes(post.Return)
	if got.Uint64() != 728126428 {
		t.Fatalf("chainid = %d, want 728126428", got.Uint64())
	}
}

// TestCreate2GatedByConstantinople: CREATE2 (0xf5) faults before Constantinople.
func TestCreate2GatedByConstantinople(t *testing.T) {
	// Minimal: PUSH1 0 x4 then CREATE2 — only needs to reach the opcode to test the gate.
	code := []byte{0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0xf5}
	pre := runWithCfg(code, 100000, VMConfig{})
	if !errors.Is(pre.Err, ErrInvalidOpcode) {
		t.Fatalf("pre-Constantinople CREATE2 err = %v, want ErrInvalidOpcode", pre.Err)
	}
	post := runWithCfg(code, 100000, ConstantinopleVMConfig())
	if post.Err != nil {
		t.Fatalf("post-Constantinople CREATE2 err = %v", post.Err)
	}
}

// TestTokenOpsGatedByTransferTrc10: CALLTOKENVALUE (0xd2) faults without the flag.
func TestTokenOpsGatedByTransferTrc10(t *testing.T) {
	// CALLTOKENVALUE; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN
	code := []byte{0xd2, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3}
	if pre := runWithCfg(code, 100000, VMConfig{}); !errors.Is(pre.Err, ErrInvalidOpcode) {
		t.Fatalf("pre-TransferTrc10 CALLTOKENVALUE err = %v, want ErrInvalidOpcode", pre.Err)
	}
	if post := runWithCfg(code, 100000, LatestVMConfig()); post.Err != nil {
		t.Fatalf("post CALLTOKENVALUE err = %v", post.Err)
	}
}

// Each TVM gate must flip on exactly at its java-tron activation version (ProposalUtil.java
// forkController.pass(ForkBlockVersionEnum.VERSION_X)); the block just before must not have
// it. Boundaries: TransferTrc10=6, Constantinople=8, Solidity059=9, Istanbul=19,
// London/CompatibleEvm=23, HigherCPULimit(=!LegacyMemCost)=24.
func TestVMConfigForVersion_Boundaries(t *testing.T) {
	cases := []struct {
		name    string
		gate    func(VMConfig) bool
		off, on int32 // version just before / at activation
	}{
		{"AllowTransferTrc10", func(c VMConfig) bool { return c.AllowTransferTrc10 }, 5, 6},
		{"AllowConstantinople", func(c VMConfig) bool { return c.AllowConstantinople }, 7, 8},
		{"AllowSolidity059", func(c VMConfig) bool { return c.AllowSolidity059 }, 8, 9},
		{"AllowIstanbul", func(c VMConfig) bool { return c.AllowIstanbul }, 18, 19},
		{"AllowLondon", func(c VMConfig) bool { return c.AllowLondon }, 22, 23},
		{"Forward6364", func(c VMConfig) bool { return c.Forward6364 }, 22, 23},
		// LegacyMemCost is the INVERSE — legacy (true) pre-24, surcharge (false) at/after 24.
		{"LegacyMemCost(inverse)", func(c VMConfig) bool { return !c.LegacyMemCost }, 23, 24},
	}
	for _, tc := range cases {
		if tc.gate(VMConfigForVersion(tc.off)) {
			t.Errorf("%s: enabled at version %d, want off", tc.name, tc.off)
		}
		if !tc.gate(VMConfigForVersion(tc.on)) {
			t.Errorf("%s: not enabled at version %d, want on", tc.name, tc.on)
		}
	}
}

// Version 0 is pre-everything: only the original TRON opcode set.
func TestVMConfigForVersion_PreEverything(t *testing.T) {
	c := VMConfigForVersion(0)
	if c.AllowTransferTrc10 || c.AllowConstantinople || c.AllowSolidity059 ||
		c.AllowIstanbul || c.AllowLondon || c.Forward6364 {
		t.Fatalf("version 0 must have no fork gates: %+v", c)
	}
	if !c.LegacyMemCost {
		t.Fatalf("version 0 must use legacy memory cost")
	}
}

// At/after the latest modeled fork version the resolver must equal LatestVMConfig, so the
// existing "modern era" presets/tests keep the same behavior.
func TestVMConfigForVersion_LatestEqualsPreset(t *testing.T) {
	if got, want := VMConfigForVersion(LatestForkVersion), LatestVMConfig(); got != want {
		t.Fatalf("VMConfigForVersion(%d) = %+v, want LatestVMConfig() %+v", LatestForkVersion, got, want)
	}
}
