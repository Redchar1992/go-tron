package tvm

import "errors"

// TRON's Stake1.0 / voting / Stake2.0 TVM opcodes (0xd5..0xdf) let a contract manage its own
// staked resources and votes on-chain:
//
//   - allowTvmFreeze  (#52, min VERSION_4_2): FREEZE, UNFREEZE, FREEZEEXPIRETIME
//   - allowTvmVote    (#59, min VERSION_4_3): VOTEWITNESS, WITHDRAWREWARD
//   - allowTvmFreezeV2 (Stake2.0, = supportUnfreezeDelay): FREEZEBALANCEV2, UNFREEZEBALANCEV2,
//       CANCELALLUNFREEZEV2, WITHDRAWEXPIREUNFREEZE, DELEGATERESOURCE, UNDELEGATERESOURCE
//
// Each gate is a chain-parameter proposal, off by default and on a from-genesis chain — so
// these opcodes fault as invalid until enabled, which is exactly go-tron's behavior with the
// gates left false. That is the reachable, bit-for-bit-faithful behavior on the replay
// target (TestStakingOpsGating locks it in).
//
// DEFERRED (state machine): faithful execution needs the Stake2.0 write-side — mutating an
// account's FrozenV2 / unfreeze queue / delegation, tallying witness votes, and the
// delegate/vote REWARD accounting (WITHDRAWREWARD) — a large subsystem beyond M3.5d (the read
// side landed with the staked-energy derivation, resource/stake.go). Until it lands, if a
// chain config explicitly enables one of these gates the opcode is present but fails CLOSED
// (exec returns ErrStakingOpDeferred → the frame faults) rather than silently mis-mutating
// state. The opcodes are registered with java-tron's stack arity (pop/push) so stack
// validation is already correct; only the state effect is deferred.

// ErrStakingOpDeferred is returned by the staking/vote opcodes while their state machine is
// unimplemented. It only arises if a gate is explicitly enabled (never on the default path).
var ErrStakingOpDeferred = errors.New(
	"tvm: staking/vote opcode not implemented (needs the Stake2.0 resource + reward + vote state machine)")

// opStakingDeferred is the shared fail-closed executor for the 0xd5..0xdf opcodes.
func opStakingDeferred(*interpreter, *scope) error { return ErrStakingOpDeferred }
