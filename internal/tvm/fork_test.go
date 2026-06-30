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
