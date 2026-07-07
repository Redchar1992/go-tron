package actuator

import (
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// Stake 2.0 (FreezeV2) write-side: FreezeBalanceV2 / UnfreezeBalanceV2 / WithdrawExpireUnfreeze.
// Faithful to java-tron FreezeBalanceV2Actuator / UnfreezeBalanceV2Actuator /
// WithdrawExpireUnfreezeActuator. All three require supportUnfreezeDelay() (the Stake2.0
// proposal, UNFREEZE_DELAY_DAYS > 0) — off on a from-genesis chain, so these contracts are
// rejected there, exactly as java-tron. On a Stake2.0-era chain they grow/shrink the
// account's FrozenV2 stake and TOTAL_NET/ENERGY_WEIGHT, powering the (V2-fractional)
// staked-energy derivation.
//
// Model vs V1: V2 stake has NO expiry — it stays liquid-frozen until an explicit
// UnfreezeBalanceV2 moves it into an UnfrozenV2 "unfreezing" entry that matures after
// unfreezeDelayDays; WithdrawExpireUnfreeze (or the next UnfreezeV2's implicit sweep) then
// returns matured entries to the spendable balance. Weight delta = floor(new
// frozenV2+delegated-out) - floor(old) (not V1's per-freeze floor).
//
// DEFERRED: V2 resource DELEGATION (DelegateResource/UnDelegateResource, contract types
// 57/58) + CancelAllUnfreezeV2 (59) — the next slice; TRON_POWER (new-resource-model,
// proposal off); MortgageService.withdrawReward on unfreeze (no reward subsystem yet — vote
// clearing IS applied).

const unfreezeMaxTimes = 32 // UnfreezeBalanceV2Actuator.UNFREEZE_MAX_TIMES

// frozenV2Amount is AccountCapsule.getFrozenV2Balance(res): the account's own FrozenV2 stake
// for a resource, in sun.
func frozenV2Amount(a *core.Account, res core.ResourceCode) int64 {
	var total int64
	for _, f := range a.GetFrozenV2() {
		if f.GetType() == res {
			total += f.GetAmount()
		}
	}
	return total
}

// addFrozenV2 adds delta (may be negative) to the account's FrozenV2 entry for res, creating
// it when absent — AccountCapsule.addFrozenBalanceForResource.
func addFrozenV2(a *core.Account, res core.ResourceCode, delta int64) {
	for _, f := range a.GetFrozenV2() {
		if f.GetType() == res {
			f.Amount += delta
			return
		}
	}
	a.FrozenV2 = append(a.FrozenV2, &core.Account_FreezeV2{Type: res, Amount: delta})
}

// delegatedOutV2 is the account's delegated-OUT V2 balance for a resource — it still counts
// toward the account's own weight (getFrozenV2BalanceWithDelegated). Energy lives on
// AccountResource, bandwidth on Account.
func delegatedOutV2(a *core.Account, res core.ResourceCode) int64 {
	if res == core.ResourceCode_BANDWIDTH {
		return a.GetDelegatedFrozenV2BalanceForBandwidth()
	}
	return a.GetAccountResource().GetDelegatedFrozenV2BalanceForEnergy()
}

// weightV2 is floor(getFrozenV2BalanceWithDelegated(res)/TRX_PRECISION) — the whole-TRX
// weight the account contributes for a resource.
func weightV2(a *core.Account, res core.ResourceCode) int64 {
	return (frozenV2Amount(a, res) + delegatedOutV2(a, res)) / trxPrecision
}

// addResourceWeight routes a weight delta to the right network total.
func addResourceWeight(props *state.PropertyStore, res core.ResourceCode, delta int64) error {
	if res == core.ResourceCode_BANDWIDTH {
		return props.AddTotalNetWeight(delta)
	}
	return props.AddTotalEnergyWeight(delta)
}

// validV2Resource reports whether res is freezable under V2 (BANDWIDTH/ENERGY; TRON_POWER
// needs the new-resource-model proposal, which go-tron does not process).
func validV2Resource(res core.ResourceCode) bool {
	return res == core.ResourceCode_BANDWIDTH || res == core.ResourceCode_ENERGY
}

// requireStake2 fails a V2 contract closed unless the Stake2.0 proposal is active.
func requireStake2(ctx *Context, name string) error {
	on, err := ctx.State.Properties.SupportUnfreezeDelay()
	if err != nil {
		return err
	}
	if !on {
		return fmt.Errorf("actuator: not support %s transaction, need to be opened by the committee", name)
	}
	return nil
}

// ---- FreezeBalanceV2 ----

type freezeBalanceV2Actuator struct{}

func (freezeBalanceV2Actuator) unpack(ctx *Context) (*core.FreezeBalanceV2Contract, error) {
	c := new(core.FreezeBalanceV2Contract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack FreezeBalanceV2Contract: %w", err)
	}
	return c, nil
}

func (a freezeBalanceV2Actuator) Validate(ctx *Context) error {
	if err := requireStake2(ctx, "FreezeV2"); err != nil {
		return err
	}
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	acct, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: freezeV2 owner account missing: %w", err)
	}
	fb := c.GetFrozenBalance()
	if fb <= 0 {
		return errors.New("actuator: frozenBalance must be positive")
	}
	if fb < trxPrecision {
		return errors.New("actuator: frozenBalance must be greater than or equal to 1 TRX")
	}
	if fb > acct.GetBalance() {
		return errors.New("actuator: frozenBalance must be less than or equal to accountBalance")
	}
	if !validV2Resource(c.GetResource()) {
		return fmt.Errorf("actuator: ResourceCode error, valid ResourceCode[BANDWIDTH, ENERGY], got %v", c.GetResource())
	}
	return nil
}

func (a freezeBalanceV2Actuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}
	res := c.GetResource()
	old := weightV2(acct, res)
	addFrozenV2(acct, res, c.GetFrozenBalance())
	if err := addResourceWeight(ctx.State.Properties, res, weightV2(acct, res)-old); err != nil {
		return err
	}
	acct.Balance -= c.GetFrozenBalance()
	return ctx.State.Accounts.Put(acct)
}

// ---- UnfreezeBalanceV2 ----

type unfreezeBalanceV2Actuator struct{}

func (unfreezeBalanceV2Actuator) unpack(ctx *Context) (*core.UnfreezeBalanceV2Contract, error) {
	c := new(core.UnfreezeBalanceV2Contract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack UnfreezeBalanceV2Contract: %w", err)
	}
	return c, nil
}

func (a unfreezeBalanceV2Actuator) Validate(ctx *Context) error {
	if err := requireStake2(ctx, "UnfreezeV2"); err != nil {
		return err
	}
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	acct, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: unfreezeV2 owner account missing: %w", err)
	}
	if !validV2Resource(c.GetResource()) {
		return fmt.Errorf("actuator: ResourceCode error, valid ResourceCode[BANDWIDTH, Energy], got %v", c.GetResource())
	}
	if frozenV2Amount(acct, c.GetResource()) <= 0 {
		return fmt.Errorf("actuator: no frozenBalance(%v)", c.GetResource())
	}
	if ub := c.GetUnfreezeBalance(); ub <= 0 || ub > frozenV2Amount(acct, c.GetResource()) {
		return fmt.Errorf("actuator: invalid unfreeze_balance, [%d] is error", c.GetUnfreezeBalance())
	}
	now, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	if unfreezingV2Count(acct, now) >= unfreezeMaxTimes {
		return errors.New("actuator: invalid unfreeze operation, unfreezing times is over limit")
	}
	return nil
}

func (a unfreezeBalanceV2Actuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}
	props := ctx.State.Properties
	now, err := props.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	delayDays, err := props.UnfreezeDelayDays()
	if err != nil {
		return err
	}

	// java-tron sweeps matured unfreezing entries back to balance first (unfreezeExpire).
	sweepMaturedUnfrozenV2(acct, now)

	res := c.GetResource()
	// Append the new unfreezing entry (matures after unfreezeDelayDays), then reduce the
	// FrozenV2 stake + weight (updateTotalResourceWeight).
	expire := now + delayDays*frozenPeriodMs
	acct.UnfrozenV2 = append(acct.UnfrozenV2, &core.Account_UnFreezeV2{
		Type: res, UnfreezeAmount: c.GetUnfreezeBalance(), UnfreezeExpireTime: expire,
	})
	old := weightV2(acct, res)
	addFrozenV2(acct, res, -c.GetUnfreezeBalance())
	if err := addResourceWeight(props, res, weightV2(acct, res)-old); err != nil {
		return err
	}

	// V2 unfreeze also clears the account's votes (updateVote).
	acct.Votes = nil
	return ctx.State.Accounts.Put(acct)
}

// ---- WithdrawExpireUnfreeze ----

type withdrawExpireUnfreezeActuator struct{}

func (withdrawExpireUnfreezeActuator) unpack(ctx *Context) (*core.WithdrawExpireUnfreezeContract, error) {
	c := new(core.WithdrawExpireUnfreezeContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack WithdrawExpireUnfreezeContract: %w", err)
	}
	return c, nil
}

func (a withdrawExpireUnfreezeActuator) Validate(ctx *Context) error {
	if err := requireStake2(ctx, "WithdrawExpireUnfreeze"); err != nil {
		return err
	}
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	if _, err := ctx.State.Accounts.Get(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: withdraw owner account missing: %w", err)
	}
	return nil
}

func (a withdrawExpireUnfreezeActuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}
	now, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	sweepMaturedUnfrozenV2(acct, now)
	return ctx.State.Accounts.Put(acct)
}

// sweepMaturedUnfrozenV2 folds every UnfrozenV2 entry whose expiry has passed back into the
// spendable balance and drops it, keeping the rest — AccountCapsule.unfreezeExpire /
// WithdrawExpireUnfreezeActuator.getTotalWithdrawUnfreeze+getRemainWithdrawList.
func sweepMaturedUnfrozenV2(a *core.Account, now int64) {
	kept := a.UnfrozenV2[:0]
	var withdrawn int64
	for _, u := range a.GetUnfrozenV2() {
		if u.GetUnfreezeExpireTime() <= now {
			withdrawn += u.GetUnfreezeAmount()
		} else {
			kept = append(kept, u)
		}
	}
	a.UnfrozenV2 = kept
	a.Balance += withdrawn
}

// unfreezingV2Count is AccountCapsule.getUnfreezingV2Count: pending (not-yet-matured)
// unfreezing entries.
func unfreezingV2Count(a *core.Account, now int64) int {
	n := 0
	for _, u := range a.GetUnfrozenV2() {
		if u.GetUnfreezeExpireTime() > now {
			n++
		}
	}
	return n
}
