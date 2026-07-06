package tvm

import (
	"errors"
	"testing"
)

func TestStakingOpsGating(t *testing.T) {
	table := opTable()
	ops := []OpCode{
		FREEZE, UNFREEZE, FREEZEEXPIRETIME,
		VOTEWITNESS, WITHDRAWREWARD,
		FREEZEBALANCEV2, UNFREEZEBALANCEV2, CANCELALLUNFREEZEV2,
		WITHDRAWEXPIREUNFREEZE, DELEGATERESOURCE, UNDELEGATERESOURCE,
	}
	// Every staking opcode is registered but gated off by the zero VMConfig, so on a
	// from-genesis chain it faults as invalid (dispatch: enabled(cfg)==false -> ErrInvalidOpcode).
	for _, op := range ops {
		o := table[op]
		if o == nil {
			t.Fatalf("op %#x not registered", byte(op))
		}
		if o.enabled == nil || o.enabled(VMConfig{}) {
			t.Fatalf("op %#x should be gated off by default", byte(op))
		}
	}

	// Each opcode enables only under its own proposal flag.
	if !table[FREEZE].enabled(VMConfig{AllowTvmFreeze: true}) {
		t.Fatal("FREEZE should enable under AllowTvmFreeze")
	}
	if table[FREEZE].enabled(VMConfig{AllowTvmVote: true}) {
		t.Fatal("FREEZE must not enable under AllowTvmVote")
	}
	if !table[VOTEWITNESS].enabled(VMConfig{AllowTvmVote: true}) {
		t.Fatal("VOTEWITNESS should enable under AllowTvmVote")
	}
	if !table[FREEZEBALANCEV2].enabled(VMConfig{AllowTvmFreezeV2: true}) {
		t.Fatal("FREEZEBALANCEV2 should enable under AllowTvmFreezeV2")
	}

	// Stack arity matches java-tron OperationRegistry.
	if table[VOTEWITNESS].pop != 4 || table[WITHDRAWREWARD].pop != 0 || table[DELEGATERESOURCE].pop != 3 {
		t.Fatal("staking opcode stack arity mismatch")
	}

	// Enabled execution is deferred: fails closed rather than mutating state.
	if err := opStakingDeferred(nil, nil); !errors.Is(err, ErrStakingOpDeferred) {
		t.Fatalf("opStakingDeferred = %v, want ErrStakingOpDeferred", err)
	}
}
