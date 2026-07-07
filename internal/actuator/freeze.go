package actuator

import (
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// Stake 1.0 actuators: FreezeBalanceContract / UnfreezeBalanceContract — the resource
// write-side that grows/shrinks per-account frozen balances and the network TOTAL_NET_WEIGHT
// / TOTAL_ENERGY_WEIGHT globals. This is what makes the staked-energy derivation
// (internal/resource, wired in vm.go) return non-zero on a chain that has seen freezes.
//
// Faithful to java-tron FreezeBalanceActuator / UnfreezeBalanceActuator (V1). CONSENSUS-
// CRITICAL details reproduced exactly:
//   - "now" is LATEST_BLOCK_HEADER_TIMESTAMP — the PREVIOUS block's timestamp during block
//     processing (java-tron updates the property only after a block's txs; see Manager).
//   - The weight delta added to TOTAL_*_WEIGHT is frozenBalance/TRX_PRECISION (floor of the
//     DELTA) pre-allowNewReward, and the account's floor(new total)-floor(old total) after
//     it — the historic floor-drift behavior.
//   - Freezes merge into a single frozen entry whose expire time is the latest freeze's
//     (validate enforces frozenCount <= 1); bandwidth unfreeze releases only EXPIRED
//     entries, energy unfreeze releases the whole (single) energy stake.
//   - V1 unfreeze clears the account's votes (AccountCapsule.clearVotes).
//
// DEFERRED (each fails closed / is unreachable from genesis):
//   - Resource delegation (receiver set + ALLOW_DELEGATE_RESOURCE on): needs the
//     DelegatedResource store — next slice. The gate defaults 0 and no proposal processing
//     exists, so on a from-genesis chain a set receiver is IGNORED (self-freeze), exactly
//     java-tron's supportDR()==false behavior.
//   - TRON_POWER (new-resource-model, proposal-gated, default off): rejected, matching
//     java-tron with supportAllowNewResourceModel()==false.
//   - VotesStore bookkeeping + reward withdrawal on unfreeze (MortgageService.withdrawReward):
//     go-tron has no vote-tally/reward subsystem yet; the account-side vote clearing IS
//     applied. No effect on resource receipts.
const frozenPeriodMs int64 = 86_400_000 // Parameter.ChainConstant.FROZEN_PERIOD (1 day)

// trxPrecision is Parameter.ChainConstant.TRX_PRECISION (sun per TRX); stake weights are
// measured in whole TRX.
const trxPrecision int64 = 1_000_000

// errDelegateResourceDeferred fails V1 delegated freezes closed until the DelegatedResource
// store lands. Unreachable from genesis (ALLOW_DELEGATE_RESOURCE defaults 0).
var errDelegateResourceDeferred = errors.New(
	"actuator: V1 resource delegation not implemented (needs the DelegatedResource store)")

// freezeBalanceActuator applies FreezeBalanceContract.
type freezeBalanceActuator struct{}

func (freezeBalanceActuator) unpack(ctx *Context) (*core.FreezeBalanceContract, error) {
	fc := new(core.FreezeBalanceContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(fc); err != nil {
		return nil, fmt.Errorf("unpack FreezeBalanceContract: %w", err)
	}
	return fc, nil
}

// sumFrozen is AccountCapsule.getFrozenBalance(): the sum of the (0-or-1) V1 bandwidth
// frozen entries, in sun.
func sumFrozen(a *core.Account) int64 {
	var total int64
	for _, f := range a.GetFrozen() {
		total += f.GetFrozenBalance()
	}
	return total
}

// energyFrozen is AccountCapsule.getEnergyFrozenBalance(): the V1 energy stake, in sun.
func energyFrozen(a *core.Account) int64 {
	return a.GetAccountResource().GetFrozenBalanceForEnergy().GetFrozenBalance()
}

func (a freezeBalanceActuator) Validate(ctx *Context) error {
	fc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(fc.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	acct, err := ctx.State.Accounts.Get(fc.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: freeze owner account missing: %w", err)
	}

	frozenBalance := fc.GetFrozenBalance()
	if frozenBalance <= 0 {
		return errors.New("actuator: frozenBalance must be positive")
	}
	if frozenBalance < trxPrecision {
		return errors.New("actuator: frozenBalance must be greater than or equal to 1 TRX")
	}
	if n := len(acct.GetFrozen()); n != 0 && n != 1 {
		return fmt.Errorf("actuator: frozenCount must be 0 or 1, got %d", n)
	}
	if frozenBalance > acct.GetBalance() {
		return errors.New("actuator: frozenBalance must be less than or equal to accountBalance")
	}

	props := ctx.State.Properties
	minDays, err := props.MinFrozenTime()
	if err != nil {
		return err
	}
	maxDays, err := props.MaxFrozenTime()
	if err != nil {
		return err
	}
	if d := fc.GetFrozenDuration(); d < minDays || d > maxDays {
		return fmt.Errorf("actuator: frozenDuration must be between %d and %d days, got %d",
			minDays, maxDays, d)
	}

	switch fc.GetResource() {
	case core.ResourceCode_BANDWIDTH, core.ResourceCode_ENERGY:
	default:
		// TRON_POWER needs the new-resource-model proposal (default off), anything else is
		// invalid outright — both rejected, matching java-tron.
		return fmt.Errorf("actuator: ResourceCode error, valid ResourceCode[BANDWIDTH, ENERGY], got %v",
			fc.GetResource())
	}

	if len(fc.GetReceiverAddress()) > 0 {
		dr, err := props.SupportDR()
		if err != nil {
			return err
		}
		if dr {
			return errDelegateResourceDeferred // fail closed until the delegation slice lands
		}
		// supportDR off: java-tron ignores the receiver and self-freezes. Fall through.
	}

	v2, err := props.SupportUnfreezeDelay()
	if err != nil {
		return err
	}
	if v2 {
		return errors.New("actuator: freeze v2 is open, old freeze is closed")
	}
	return nil
}

func (a freezeBalanceActuator) Execute(ctx *Context) error {
	fc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(fc.GetOwnerAddress())
	if err != nil {
		return err
	}
	props := ctx.State.Properties
	now, err := props.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	newReward, err := props.AllowNewReward()
	if err != nil {
		return err
	}

	frozenBalance := fc.GetFrozenBalance()
	expireTime := now + fc.GetFrozenDuration()*frozenPeriodMs

	switch fc.GetResource() {
	case core.ResourceCode_BANDWIDTH:
		old := sumFrozen(acct)
		newTotal := frozenBalance + old
		// setFrozenForBandwidth: merge into the single V1 entry, expire time of the latest freeze.
		acct.Frozen = []*core.Account_Frozen{{FrozenBalance: newTotal, ExpireTime: expireTime}}
		weight := frozenBalance / trxPrecision
		if newReward {
			weight = newTotal/trxPrecision - old/trxPrecision
		}
		if err := props.AddTotalNetWeight(weight); err != nil {
			return err
		}
	case core.ResourceCode_ENERGY:
		if acct.AccountResource == nil {
			acct.AccountResource = &core.Account_AccountResource{}
		}
		old := energyFrozen(acct)
		newTotal := frozenBalance + old
		acct.AccountResource.FrozenBalanceForEnergy = &core.Account_Frozen{
			FrozenBalance: newTotal, ExpireTime: expireTime,
		}
		weight := frozenBalance / trxPrecision
		if newReward {
			weight = newTotal/trxPrecision - old/trxPrecision
		}
		if err := props.AddTotalEnergyWeight(weight); err != nil {
			return err
		}
	}

	acct.Balance -= frozenBalance
	return ctx.State.Accounts.Put(acct)
}

// unfreezeBalanceActuator applies UnfreezeBalanceContract.
type unfreezeBalanceActuator struct{}

func (unfreezeBalanceActuator) unpack(ctx *Context) (*core.UnfreezeBalanceContract, error) {
	uc := new(core.UnfreezeBalanceContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(uc); err != nil {
		return nil, fmt.Errorf("unpack UnfreezeBalanceContract: %w", err)
	}
	return uc, nil
}

func (a unfreezeBalanceActuator) Validate(ctx *Context) error {
	uc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(uc.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	acct, err := ctx.State.Accounts.Get(uc.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: unfreeze owner account missing: %w", err)
	}
	props := ctx.State.Properties
	now, err := props.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}

	if len(uc.GetReceiverAddress()) > 0 {
		dr, err := props.SupportDR()
		if err != nil {
			return err
		}
		if dr {
			return errDelegateResourceDeferred
		}
		// supportDR off: receiver ignored, self-unfreeze path (java-tron behavior).
	}

	switch uc.GetResource() {
	case core.ResourceCode_BANDWIDTH:
		if len(acct.GetFrozen()) == 0 {
			return errors.New("actuator: no frozenBalance(BANDWIDTH)")
		}
		expired := 0
		for _, f := range acct.GetFrozen() {
			if f.GetExpireTime() <= now {
				expired++
			}
		}
		if expired == 0 {
			return errors.New("actuator: it's not time to unfreeze(BANDWIDTH)")
		}
	case core.ResourceCode_ENERGY:
		fbe := acct.GetAccountResource().GetFrozenBalanceForEnergy()
		if fbe.GetFrozenBalance() <= 0 {
			return errors.New("actuator: no frozenBalance(Energy)")
		}
		if fbe.GetExpireTime() > now {
			return errors.New("actuator: it's not time to unfreeze(Energy)")
		}
	default:
		return fmt.Errorf("actuator: ResourceCode error, valid ResourceCode[BANDWIDTH, ENERGY], got %v",
			uc.GetResource())
	}
	return nil
}

func (a unfreezeBalanceActuator) Execute(ctx *Context) error {
	uc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(uc.GetOwnerAddress())
	if err != nil {
		return err
	}
	props := ctx.State.Properties
	now, err := props.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	newReward, err := props.AllowNewReward()
	if err != nil {
		return err
	}

	// NOTE: java-tron first runs MortgageService.withdrawReward(owner) — the vote-reward
	// settlement. go-tron has no reward subsystem yet (deferred with the vote/witness-pay
	// milestone); on a chain with no vote rewards it is a no-op.

	var unfreeze, decrease int64
	switch uc.GetResource() {
	case core.ResourceCode_BANDWIDTH:
		oldW := sumFrozen(acct) / trxPrecision
		var keep []*core.Account_Frozen
		for _, f := range acct.GetFrozen() {
			if f.GetExpireTime() <= now {
				unfreeze += f.GetFrozenBalance()
			} else {
				keep = append(keep, f)
			}
		}
		acct.Frozen = keep
		decrease = sumFrozen(acct)/trxPrecision - oldW
	case core.ResourceCode_ENERGY:
		oldW := energyFrozen(acct) / trxPrecision
		unfreeze = energyFrozen(acct)
		if acct.AccountResource != nil {
			acct.AccountResource.FrozenBalanceForEnergy = nil // clearFrozenBalanceForEnergy
		}
		decrease = -oldW
	}
	acct.Balance += unfreeze

	weight := -unfreeze / trxPrecision
	if newReward {
		weight = decrease
	}
	switch uc.GetResource() {
	case core.ResourceCode_BANDWIDTH:
		if err := props.AddTotalNetWeight(weight); err != nil {
			return err
		}
	case core.ResourceCode_ENERGY:
		if err := props.AddTotalEnergyWeight(weight); err != nil {
			return err
		}
	}

	// V1 unfreeze clears the account's votes (AccountCapsule.clearVotes). The VotesStore
	// entry that feeds the maintenance-window tally is deferred with the vote subsystem.
	acct.Votes = nil

	return ctx.State.Accounts.Put(acct)
}
