package actuator

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
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
// V1 resource DELEGATION (receiver set + ALLOW_DELEGATE_RESOURCE on) IS implemented: a
// freeze/unfreeze with a receiver moves the stake into a DelegatedResource(from,to) entry and
// credits/debits the receiver's acquired-delegated balance, so the receiver's staked energy
// (getAllFrozenBalanceForEnergy counts acquired-delegated-in) powers ITS contract calls.
// While ALLOW_DELEGATE_RESOURCE is off (from-genesis default), a set receiver is IGNORED and
// the tx self-freezes — java-tron's supportDR()==false behavior.
//
// DEFERRED (each fails closed / is unreachable from genesis):
//   - The delegate-optimization index layout (supportAllowDelegateOptimization, proposal-
//     gated, default off): the un-optimized from/to index lists are maintained; the optimized
//     timestamp-paged layout arrives with proposal processing.
//   - TRON_POWER (new-resource-model, proposal-gated, default off): rejected, matching
//     java-tron with supportAllowNewResourceModel()==false.
//   - VotesStore bookkeeping + reward withdrawal on unfreeze (MortgageService.withdrawReward):
//     go-tron has no vote-tally/reward subsystem yet; the account-side vote clearing IS
//     applied. No effect on resource receipts.
const frozenPeriodMs int64 = 86_400_000 // Parameter.ChainConstant.FROZEN_PERIOD (1 day)

// trxPrecision is Parameter.ChainConstant.TRX_PRECISION (sun per TRX); stake weights are
// measured in whole TRX.
const trxPrecision int64 = 1_000_000

// delegatedForResource reports whether a tx delegates (receiver set AND supportDR on).
func delegatedForResource(ctx *Context, receiver []byte) (bool, error) {
	if len(receiver) == 0 {
		return false, nil
	}
	return ctx.State.Properties.SupportDR()
}

// recordDelegationIndex records the from->to delegation edge, choosing the layout by
// ALLOW_DELEGATE_OPTIMIZATION: legacy list-form (default) or the optimized per-edge form
// (which first converts any legacy entries, then delegates stamped with the block time).
func recordDelegationIndex(ctx *Context, from, to []byte) error {
	opt, err := ctx.State.Properties.SupportAllowDelegateOptimization()
	if err != nil {
		return err
	}
	if !opt {
		return addDelegationIndex(ctx.State, from, to)
	}
	if err := ctx.State.DelegatedIndex.Convert(from); err != nil {
		return err
	}
	if err := ctx.State.DelegatedIndex.Convert(to); err != nil {
		return err
	}
	now, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	return ctx.State.DelegatedIndex.Delegate(from, to, now)
}

// dropDelegationIndex removes the from->to delegation edge under the active layout.
func dropDelegationIndex(ctx *Context, from, to []byte) error {
	opt, err := ctx.State.Properties.SupportAllowDelegateOptimization()
	if err != nil {
		return err
	}
	if !opt {
		return removeDelegationIndex(ctx.State, from, to)
	}
	if err := ctx.State.DelegatedIndex.Convert(from); err != nil {
		return err
	}
	if err := ctx.State.DelegatedIndex.Convert(to); err != nil {
		return err
	}
	return ctx.State.DelegatedIndex.UnDelegate(from, to)
}

// addDelegationIndex records the from->to edge in both accounts' delegation index (the
// un-optimized DelegatedResourceAccountIndexStore layout; idempotent per edge).
func addDelegationIndex(st *state.State, from, to []byte) error {
	fromIdx, err := st.DelegatedIndex.Get(from)
	if err != nil {
		fromIdx = &core.DelegatedResourceAccountIndex{Account: from}
	}
	if !containsAddr(fromIdx.GetToAccounts(), to) {
		fromIdx.ToAccounts = append(fromIdx.ToAccounts, append([]byte(nil), to...))
		if err := st.DelegatedIndex.Put(fromIdx); err != nil {
			return err
		}
	}
	toIdx, err := st.DelegatedIndex.Get(to)
	if err != nil {
		toIdx = &core.DelegatedResourceAccountIndex{Account: to}
	}
	if !containsAddr(toIdx.GetFromAccounts(), from) {
		toIdx.FromAccounts = append(toIdx.FromAccounts, append([]byte(nil), from...))
		if err := st.DelegatedIndex.Put(toIdx); err != nil {
			return err
		}
	}
	return nil
}

// removeDelegationIndex drops the from->to edge from both accounts' index (called when the
// (from,to) DelegatedResource entry becomes fully empty).
func removeDelegationIndex(st *state.State, from, to []byte) error {
	if fromIdx, err := st.DelegatedIndex.Get(from); err == nil {
		fromIdx.ToAccounts = removeAddr(fromIdx.GetToAccounts(), to)
		if err := st.DelegatedIndex.Put(fromIdx); err != nil {
			return err
		}
	}
	if toIdx, err := st.DelegatedIndex.Get(to); err == nil {
		toIdx.FromAccounts = removeAddr(toIdx.GetFromAccounts(), from)
		if err := st.DelegatedIndex.Put(toIdx); err != nil {
			return err
		}
	}
	return nil
}

func containsAddr(list [][]byte, a []byte) bool {
	for _, x := range list {
		if bytes.Equal(x, a) {
			return true
		}
	}
	return false
}

func removeAddr(list [][]byte, a []byte) [][]byte {
	out := list[:0]
	for _, x := range list {
		if !bytes.Equal(x, a) {
			out = append(out, x)
		}
	}
	return out
}

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

// Energy delegated balances live on Account.AccountResource (java-tron AccountCapsule
// delegates these to the sub-message); bandwidth ones live directly on Account.
func ensureResource(a *core.Account) {
	if a.AccountResource == nil {
		a.AccountResource = &core.Account_AccountResource{}
	}
}

func acquiredDelegatedEnergy(a *core.Account) int64 {
	return a.GetAccountResource().GetAcquiredDelegatedFrozenBalanceForEnergy()
}

func addAcquiredDelegatedEnergy(a *core.Account, d int64) {
	ensureResource(a)
	a.AccountResource.AcquiredDelegatedFrozenBalanceForEnergy += d
}

func setAcquiredDelegatedEnergy(a *core.Account, v int64) {
	ensureResource(a)
	a.AccountResource.AcquiredDelegatedFrozenBalanceForEnergy = v
}

func addDelegatedEnergy(a *core.Account, d int64) {
	ensureResource(a)
	a.AccountResource.DelegatedFrozenBalanceForEnergy += d
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

	delegated, err := delegatedForResource(ctx, fc.GetReceiverAddress())
	if err != nil {
		return err
	}
	if delegated {
		receiver := fc.GetReceiverAddress()
		if bytes.Equal(receiver, fc.GetOwnerAddress()) {
			return errors.New("actuator: receiverAddress must not be the same as ownerAddress")
		}
		if _, err := address.FromBytes(receiver); err != nil {
			return fmt.Errorf("actuator: invalid receiverAddress: %w", err)
		}
		rc, err := ctx.State.Accounts.Get(receiver)
		if err != nil {
			return fmt.Errorf("actuator: receiver account [%x] does not exist", receiver)
		}
		constantinople, err := props.AllowTvmConstantinople()
		if err != nil {
			return err
		}
		if constantinople && rc.GetType() == core.AccountType_Contract {
			return errors.New("actuator: do not allow delegate resources to contract addresses")
		}
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
	isBandwidth := fc.GetResource() == core.ResourceCode_BANDWIDTH

	delegated, err := delegatedForResource(ctx, fc.GetReceiverAddress())
	if err != nil {
		return err
	}

	var increment int64 // whole-TRX weight delta the network gains (allowNewReward path)
	if delegated {
		increment, err = a.delegate(ctx, fc.GetOwnerAddress(), fc.GetReceiverAddress(),
			isBandwidth, frozenBalance, expireTime)
		if err != nil {
			return err
		}
		// The owner records how much it has delegated OUT (its own stake still counts toward
		// its weight; the acquired-side credit went to the receiver in delegate()).
		if isBandwidth {
			acct.DelegatedFrozenBalanceForBandwidth += frozenBalance
		} else {
			addDelegatedEnergy(acct, frozenBalance)
		}
	} else if isBandwidth {
		old := sumFrozen(acct)
		newTotal := frozenBalance + old
		acct.Frozen = []*core.Account_Frozen{{FrozenBalance: newTotal, ExpireTime: expireTime}}
		increment = newTotal/trxPrecision - old/trxPrecision
	} else {
		if acct.AccountResource == nil {
			acct.AccountResource = &core.Account_AccountResource{}
		}
		old := energyFrozen(acct)
		newTotal := frozenBalance + old
		acct.AccountResource.FrozenBalanceForEnergy = &core.Account_Frozen{
			FrozenBalance: newTotal, ExpireTime: expireTime,
		}
		increment = newTotal/trxPrecision - old/trxPrecision
	}

	// addTotalWeight: allowNewReward adds the floor-drift-aware increment, else the flat
	// frozenBalance/TRX_PRECISION.
	weight := frozenBalance / trxPrecision
	if newReward {
		weight = increment
	}
	if isBandwidth {
		if err := props.AddTotalNetWeight(weight); err != nil {
			return err
		}
	} else {
		if err := props.AddTotalEnergyWeight(weight); err != nil {
			return err
		}
	}

	acct.Balance -= frozenBalance
	return ctx.State.Accounts.Put(acct)
}

// delegate moves `balance` of `from`'s stake into the DelegatedResource(from,to) entry and
// credits `to`'s acquired-delegated balance (the resource it can now spend). Returns the
// receiver's whole-TRX acquired-weight increment. Mirrors FreezeBalanceActuator.delegateResource.
func (freezeBalanceActuator) delegate(ctx *Context, from, to []byte, isBandwidth bool,
	balance, expireTime int64) (int64, error) {
	st := ctx.State
	dr, err := st.Delegated.Get(from, to)
	if err != nil {
		dr = &core.DelegatedResource{From: from, To: to}
	}
	if isBandwidth {
		dr.FrozenBalanceForBandwidth += balance
		dr.ExpireTimeForBandwidth = expireTime
	} else {
		dr.FrozenBalanceForEnergy += balance
		dr.ExpireTimeForEnergy = expireTime
	}
	if err := st.Delegated.Put(dr); err != nil {
		return 0, err
	}
	if err := recordDelegationIndex(ctx, from, to); err != nil {
		return 0, err
	}

	rc, err := st.Accounts.Get(to)
	if err != nil {
		return 0, fmt.Errorf("actuator: receiver account [%x] does not exist", to)
	}
	var oldW, newW int64
	if isBandwidth {
		oldW = rc.GetAcquiredDelegatedFrozenBalanceForBandwidth() / trxPrecision
		rc.AcquiredDelegatedFrozenBalanceForBandwidth += balance
		newW = rc.GetAcquiredDelegatedFrozenBalanceForBandwidth() / trxPrecision
	} else {
		oldW = acquiredDelegatedEnergy(rc) / trxPrecision
		addAcquiredDelegatedEnergy(rc, balance)
		newW = acquiredDelegatedEnergy(rc) / trxPrecision
	}
	if err := st.Accounts.Put(rc); err != nil {
		return 0, err
	}
	return newW - oldW, nil
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

	delegated, err := delegatedForResource(ctx, uc.GetReceiverAddress())
	if err != nil {
		return err
	}
	if delegated {
		return a.validateDelegated(ctx, uc, acct, now)
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

// validateDelegated checks a delegated (receiver-set) unfreeze: the receiver and the
// (from,to) entry must exist, the entry must hold a positive balance for the resource, and
// its expiry must have passed. Mirrors UnfreezeBalanceActuator.validate's delegated branch;
// the energy expiry read goes through the getExpireTimeForEnergy(multiSign) quirk.
func (a unfreezeBalanceActuator) validateDelegated(ctx *Context, uc *core.UnfreezeBalanceContract,
	acct *core.Account, now int64) error {
	receiver := uc.GetReceiverAddress()
	if bytes.Equal(receiver, uc.GetOwnerAddress()) {
		return errors.New("actuator: receiverAddress must not be the same as ownerAddress")
	}
	if _, err := address.FromBytes(receiver); err != nil {
		return fmt.Errorf("actuator: invalid receiverAddress: %w", err)
	}
	constantinople, err := ctx.State.Properties.AllowTvmConstantinople()
	if err != nil {
		return err
	}
	if _, err := ctx.State.Accounts.Get(receiver); err != nil && !constantinople {
		return fmt.Errorf("actuator: receiver account [%x] does not exist", receiver)
	}
	dr, err := ctx.State.Delegated.Get(uc.GetOwnerAddress(), receiver)
	if err != nil {
		return errors.New("actuator: delegated Resource does not exist")
	}
	switch uc.GetResource() {
	case core.ResourceCode_BANDWIDTH:
		if dr.GetFrozenBalanceForBandwidth() <= 0 {
			return errors.New("actuator: no delegatedFrozenBalance(BANDWIDTH)")
		}
		if dr.GetExpireTimeForBandwidth() > now {
			return errors.New("actuator: it's not time to unfreeze")
		}
	case core.ResourceCode_ENERGY:
		if dr.GetFrozenBalanceForEnergy() <= 0 {
			return errors.New("actuator: no delegateFrozenBalance(Energy)")
		}
		if energyDelegateExpiry(ctx, dr) > now {
			return errors.New("actuator: it's not time to unfreeze")
		}
	default:
		return errors.New("actuator: ResourceCode error, valid ResourceCode[BANDWIDTH, Energy]")
	}
	return nil
}

func (a unfreezeBalanceActuator) Execute(ctx *Context) error {
	uc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	// Settle pending vote rewards first (mortgageService.withdrawReward) — before the account is
	// read, so the fetch below picks up the credited allowance. No-op unless allowChangeDelegation.
	if err := WithdrawReward(ctx.State, uc.GetOwnerAddress()); err != nil {
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

	delegated, err := delegatedForResource(ctx, uc.GetReceiverAddress())
	if err != nil {
		return err
	}
	if delegated {
		return a.executeDelegated(ctx, uc, acct, newReward)
	}

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

// energyDelegateExpiry is DelegatedResourceCapsule.getExpireTimeForEnergy(dynamicStore): a
// preserved historical quirk — while ALLOW_MULTI_SIGN is off, the ENERGY delegation's expiry
// check reads the BANDWIDTH expire-time field.
func energyDelegateExpiry(ctx *Context, dr *core.DelegatedResource) int64 {
	multi, err := ctx.State.Properties.AllowMultiSign()
	if err != nil || !multi {
		return dr.GetExpireTimeForBandwidth()
	}
	return dr.GetExpireTimeForEnergy()
}

// executeDelegated releases a delegated (receiver-set) stake: zero the (from,to) entry's
// resource side, debit the owner's delegated-out balance, and debit the receiver's acquired
// balance (with the solidity059 under-acquired clamp and the constantinople contract-receiver
// carve-out); the freed TRX returns to the owner's spendable balance. Mirrors
// UnfreezeBalanceActuator.execute's delegated branch.
func (a unfreezeBalanceActuator) executeDelegated(ctx *Context, uc *core.UnfreezeBalanceContract,
	acct *core.Account, newReward bool) error {
	st := ctx.State
	owner, receiver := uc.GetOwnerAddress(), uc.GetReceiverAddress()
	isBandwidth := uc.GetResource() == core.ResourceCode_BANDWIDTH

	dr, err := st.Delegated.Get(owner, receiver)
	if err != nil {
		return errors.New("actuator: delegated Resource does not exist")
	}
	var unfreeze int64
	if isBandwidth {
		unfreeze = dr.GetFrozenBalanceForBandwidth()
		dr.FrozenBalanceForBandwidth, dr.ExpireTimeForBandwidth = 0, 0
		acct.DelegatedFrozenBalanceForBandwidth -= unfreeze
	} else {
		unfreeze = dr.GetFrozenBalanceForEnergy()
		dr.FrozenBalanceForEnergy, dr.ExpireTimeForEnergy = 0, 0
		addDelegatedEnergy(acct, -unfreeze)
	}

	constantinople, err := st.Properties.AllowTvmConstantinople()
	if err != nil {
		return err
	}
	solidity059, err := st.Properties.AllowTvmSolidity059()
	if err != nil {
		return err
	}
	rc, rcErr := st.Accounts.Get(receiver)

	var decrease int64
	if !constantinople || (rcErr == nil && rc.GetType() != core.AccountType_Contract) {
		var oldW, newW int64
		if isBandwidth {
			oldW = rc.GetAcquiredDelegatedFrozenBalanceForBandwidth() / trxPrecision
			if solidity059 && rc.GetAcquiredDelegatedFrozenBalanceForBandwidth() < unfreeze {
				oldW = unfreeze / trxPrecision
				rc.AcquiredDelegatedFrozenBalanceForBandwidth = 0
			} else {
				rc.AcquiredDelegatedFrozenBalanceForBandwidth -= unfreeze
			}
			newW = rc.GetAcquiredDelegatedFrozenBalanceForBandwidth() / trxPrecision
		} else {
			oldW = acquiredDelegatedEnergy(rc) / trxPrecision
			if solidity059 && acquiredDelegatedEnergy(rc) < unfreeze {
				oldW = unfreeze / trxPrecision
				setAcquiredDelegatedEnergy(rc, 0)
			} else {
				addAcquiredDelegatedEnergy(rc, -unfreeze)
			}
			newW = acquiredDelegatedEnergy(rc) / trxPrecision
		}
		decrease = newW - oldW
		if err := st.Accounts.Put(rc); err != nil {
			return err
		}
	} else {
		decrease = -unfreeze / trxPrecision
	}

	acct.Balance += unfreeze

	if dr.GetFrozenBalanceForBandwidth() == 0 && dr.GetFrozenBalanceForEnergy() == 0 {
		if err := st.Delegated.Delete(owner, receiver); err != nil {
			return err
		}
		if err := dropDelegationIndex(ctx, owner, receiver); err != nil {
			return err
		}
	} else if err := st.Delegated.Put(dr); err != nil {
		return err
	}

	weight := -unfreeze / trxPrecision
	if newReward {
		weight = decrease
	}
	if isBandwidth {
		if err := st.Properties.AddTotalNetWeight(weight); err != nil {
			return err
		}
	} else if err := st.Properties.AddTotalEnergyWeight(weight); err != nil {
		return err
	}

	// Delegated unfreeze clears the OWNER's votes too (java-tron clears votes on both paths).
	acct.Votes = nil
	return st.Accounts.Put(acct)
}
