package actuator

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// Stake 2.0 resource DELEGATION: DelegateResourceContract / UnDelegateResourceContract — the
// Stake2.0 on-chain energy/bandwidth rental. Delegation moves a slice of the owner's FrozenV2
// stake into a DelegatedResourceV2(from,to) entry and credits the receiver's
// acquired-delegated-V2 balance, so the receiver's getAllFrozenBalanceForEnergy (which counts
// acquired-delegated-in) — and thus its staked-energy derivation — rises, letting it run
// contracts on the delegator's stake. The owner's OWN weight is unchanged: its frozenV2 drops
// but its delegated-out rises, and getFrozenV2BalanceWithDelegated counts both, so
// TOTAL_ENERGY_WEIGHT does not move on delegate/undelegate (only on freeze/unfreeze).
//
// Faithful to java-tron DelegateResourceActuator / UnDelegateResourceActuator for the
// UNLOCKED path. DEFERRED, each with a note below:
//   - LOCK (lock=true): time-locked delegation that cannot be recalled before expiry, with a
//     separate store namespace, MAX_DELEGATE_LOCK_PERIOD, and unLockExpireResource migration.
//     Rejected here; a from-genesis / no-lock chain never creates locked entries, so the
//     unLockExpireResource step is a no-op and the unlocked path is exact.
//   - The undelegate USAGE-transfer (EnergyProcessor.updateUsage + proportional transferUsage
//     + unDelegateIncrease): a secondary recovery-accounting adjustment of the receiver's and
//     owner's stored energy_usage. The PRIMARY stake/weight/acquired moves are exact; the
//     usage rebalance rides the broader resource-usage subsystem and is deferred.

var errLockedDelegateDeferred = errors.New(
	"actuator: locked resource delegation (lock=true) not implemented")

// delegatedV2 accessors: V2 energy delegated/acquired live on AccountResource; bandwidth on
// Account (matching java-tron AccountCapsule).
func addDelegatedV2(a *core.Account, res core.ResourceCode, d int64) {
	if res == core.ResourceCode_BANDWIDTH {
		a.DelegatedFrozenV2BalanceForBandwidth += d
		return
	}
	ensureResource(a)
	a.AccountResource.DelegatedFrozenV2BalanceForEnergy += d
}

func acquiredDelegatedV2(a *core.Account, res core.ResourceCode) int64 {
	if res == core.ResourceCode_BANDWIDTH {
		return a.GetAcquiredDelegatedFrozenV2BalanceForBandwidth()
	}
	return a.GetAccountResource().GetAcquiredDelegatedFrozenV2BalanceForEnergy()
}

func addAcquiredDelegatedV2(a *core.Account, res core.ResourceCode, d int64) {
	if res == core.ResourceCode_BANDWIDTH {
		a.AcquiredDelegatedFrozenV2BalanceForBandwidth += d
		return
	}
	ensureResource(a)
	a.AccountResource.AcquiredDelegatedFrozenV2BalanceForEnergy += d
}

func setAcquiredDelegatedV2(a *core.Account, res core.ResourceCode, v int64) {
	if res == core.ResourceCode_BANDWIDTH {
		a.AcquiredDelegatedFrozenV2BalanceForBandwidth = v
		return
	}
	ensureResource(a)
	a.AccountResource.AcquiredDelegatedFrozenV2BalanceForEnergy = v
}

func drBalance(d *core.DelegatedResource, res core.ResourceCode) int64 {
	if res == core.ResourceCode_BANDWIDTH {
		return d.GetFrozenBalanceForBandwidth()
	}
	return d.GetFrozenBalanceForEnergy()
}

func addDRBalance(d *core.DelegatedResource, res core.ResourceCode, delta int64) {
	if res == core.ResourceCode_BANDWIDTH {
		d.FrozenBalanceForBandwidth += delta
	} else {
		d.FrozenBalanceForEnergy += delta
	}
}

// ---- DelegateResource ----

type delegateResourceActuator struct{}

func (delegateResourceActuator) unpack(ctx *Context) (*core.DelegateResourceContract, error) {
	c := new(core.DelegateResourceContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack DelegateResourceContract: %w", err)
	}
	return c, nil
}

func (a delegateResourceActuator) Validate(ctx *Context) error {
	if err := requireStake2(ctx, "Delegate resource"); err != nil {
		return err
	}
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if c.GetLock() {
		return errLockedDelegateDeferred
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	owner, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: delegate owner account missing: %w", err)
	}
	if c.GetBalance() < trxPrecision {
		return errors.New("actuator: delegateBalance must be greater than or equal to 1 TRX")
	}
	if !validV2Resource(c.GetResource()) {
		return fmt.Errorf("actuator: no support for resource delegate, got %v", c.GetResource())
	}
	// The owner must have enough un-delegated FrozenV2 for the resource. (java-tron also
	// subtracts the frozenV2 already locked as consumed usage; that usage subsystem is
	// deferred, so this is the frozenV2 amount.)
	if frozenV2Amount(owner, c.GetResource()) < c.GetBalance() {
		return errors.New("actuator: delegateBalance exceeds the owner's frozenV2 available for delegate")
	}

	receiver := c.GetReceiverAddress()
	if _, err := address.FromBytes(receiver); err != nil {
		return fmt.Errorf("actuator: invalid receiverAddress: %w", err)
	}
	if bytes.Equal(receiver, c.GetOwnerAddress()) {
		return errors.New("actuator: receiverAddress must not be the same as ownerAddress")
	}
	rc, err := ctx.State.Accounts.Get(receiver)
	if err != nil {
		return fmt.Errorf("actuator: receiver account [%x] does not exist", receiver)
	}
	if rc.GetType() == core.AccountType_Contract {
		return errors.New("actuator: do not allow delegate resources to contract addresses")
	}
	return nil
}

func (a delegateResourceActuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	st := ctx.State
	owner, err := st.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}
	receiverAddr := c.GetReceiverAddress()
	rc, err := st.Accounts.Get(receiverAddr)
	if err != nil {
		return err
	}
	res := c.GetResource()
	balance := c.GetBalance()

	// Store the (unlocked) V2 delegation entry (expire time 0).
	dr, err := st.Delegated.GetV2(c.GetOwnerAddress(), receiverAddr, false)
	if err != nil {
		dr = &core.DelegatedResource{From: c.GetOwnerAddress(), To: receiverAddr}
	}
	addDRBalance(dr, res, balance)
	if err := st.Delegated.PutV2(dr, false); err != nil {
		return err
	}
	now, err := st.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	if err := st.DelegatedIndex.DelegateV2(c.GetOwnerAddress(), receiverAddr, now); err != nil {
		return err
	}

	// Receiver gains usable (acquired-delegated-in) resource.
	addAcquiredDelegatedV2(rc, res, balance)
	if err := st.Accounts.Put(rc); err != nil {
		return err
	}

	// Owner moves the slice from own frozenV2 to delegated-out (weight preserved).
	addDelegatedV2(owner, res, balance)
	addFrozenV2(owner, res, -balance)
	return st.Accounts.Put(owner)
}

// ---- UnDelegateResource ----

type unDelegateResourceActuator struct{}

func (unDelegateResourceActuator) unpack(ctx *Context) (*core.UnDelegateResourceContract, error) {
	c := new(core.UnDelegateResourceContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack UnDelegateResourceContract: %w", err)
	}
	return c, nil
}

func (a unDelegateResourceActuator) Validate(ctx *Context) error {
	if err := requireStake2(ctx, "unDelegate resource"); err != nil {
		return err
	}
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	if _, err := address.FromBytes(c.GetReceiverAddress()); err != nil {
		return fmt.Errorf("actuator: invalid receiverAddress: %w", err)
	}
	if !validV2Resource(c.GetResource()) {
		return fmt.Errorf("actuator: no support for resource delegate, got %v", c.GetResource())
	}
	if c.GetBalance() <= 0 {
		return errors.New("actuator: unDelegateBalance must be more than 0 TRX")
	}
	dr, err := ctx.State.Delegated.GetV2(c.GetOwnerAddress(), c.GetReceiverAddress(), false)
	if err != nil {
		return errors.New("actuator: delegated Resource does not exist")
	}
	if drBalance(dr, c.GetResource()) < c.GetBalance() {
		return fmt.Errorf("actuator: insufficient delegatedFrozenBalance, unDelegateBalance=%d", c.GetBalance())
	}
	return nil
}

func (a unDelegateResourceActuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	st := ctx.State
	owner, err := st.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}
	res := c.GetResource()
	balance := c.GetBalance()
	receiverAddr := c.GetReceiverAddress()

	// Receiver loses the acquired-delegated-in resource. A receiver holding less than the
	// unDelegate amount (a re-created TVM contract) is zeroed rather than driven negative.
	// (The proportional energy usage-transfer back to the owner is deferred — see file doc.)
	if rc, err := st.Accounts.Get(receiverAddr); err == nil {
		if acquiredDelegatedV2(rc, res) < balance {
			setAcquiredDelegatedV2(rc, res, 0)
		} else {
			addAcquiredDelegatedV2(rc, res, -balance)
		}
		if err := st.Accounts.Put(rc); err != nil {
			return err
		}
	}

	// Reduce the (unlocked) store entry; delete it when both sides are empty.
	dr, err := st.Delegated.GetV2(c.GetOwnerAddress(), receiverAddr, false)
	if err != nil {
		return errors.New("actuator: delegated Resource does not exist")
	}
	addDRBalance(dr, res, -balance)
	if dr.GetFrozenBalanceForBandwidth() == 0 && dr.GetFrozenBalanceForEnergy() == 0 {
		if err := st.Delegated.DeleteV2(c.GetOwnerAddress(), receiverAddr, false); err != nil {
			return err
		}
		if err := st.DelegatedIndex.UnDelegateV2(c.GetOwnerAddress(), receiverAddr); err != nil {
			return err
		}
	} else if err := st.Delegated.PutV2(dr, false); err != nil {
		return err
	}

	// Owner reclaims the slice: delegated-out -> own frozenV2 (weight preserved).
	addDelegatedV2(owner, res, -balance)
	addFrozenV2(owner, res, balance)
	return st.Accounts.Put(owner)
}
