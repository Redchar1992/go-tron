package actuator

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// enableDelegation turns on ALLOW_DELEGATE_RESOURCE so a receiver-set freeze delegates.
func enableDelegation(t *testing.T, st *state.State) {
	t.Helper()
	if err := st.Properties.PutInt64([]byte("ALLOW_DELEGATE_RESOURCE"), 1); err != nil {
		t.Fatal(err)
	}
}

func freezeToTx(t *testing.T, owner, receiver []byte, amount, days int64, res core.ResourceCode) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.FreezeBalanceContract{
		OwnerAddress: owner, ReceiverAddress: receiver, FrozenBalance: amount,
		FrozenDuration: days, Resource: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_FreezeBalanceContract, Parameter: param,
	}}}}
}

func unfreezeToTx(t *testing.T, owner, receiver []byte, res core.ResourceCode) *core.Transaction {
	t.Helper()
	param, err := anypb.New(&core.UnfreezeBalanceContract{
		OwnerAddress: owner, ReceiverAddress: receiver, Resource: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Transaction{RawData: &core.TransactionRaw{Contract: []*core.Transaction_Contract{{
		Type: core.Transaction_Contract_UnfreezeBalanceContract, Parameter: param,
	}}}}
}

func TestDelegateEnergyLifecycle(t *testing.T) {
	from, to := addr21(0x21), addr21(0x22)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, from, 10_000_000, t0)
	// Receiver must exist.
	if err := st.Accounts.Put(&core.Account{Address: to, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	enableDelegation(t, st)
	// ALLOW_MULTI_SIGN on so the delegated-energy expiry check reads the real energy expiry
	// (see TestDelegateEnergyExpiryQuirk for the off behavior).
	if err := st.Properties.PutInt64([]byte("ALLOW_MULTI_SIGN"), 1); err != nil {
		t.Fatal(err)
	}

	mustApply(t, st, freezeToTx(t, from, to, 5_000_000, 3, core.ResourceCode_ENERGY))

	// from: 5 TRX moved into a delegation entry; its balance drops; delegated-out recorded.
	fromAcct, _ := st.Accounts.Get(from)
	if fromAcct.GetBalance() != 5_000_000 {
		t.Fatalf("from balance = %d, want 5_000_000", fromAcct.GetBalance())
	}
	if fromAcct.GetAccountResource().GetDelegatedFrozenBalanceForEnergy() != 5_000_000 {
		t.Fatalf("from delegated-out energy = %d, want 5_000_000",
			fromAcct.GetAccountResource().GetDelegatedFrozenBalanceForEnergy())
	}
	// to: credited acquired-delegated-in energy -> its getAllFrozenBalanceForEnergy is 5 TRX.
	toAcct, _ := st.Accounts.Get(to)
	if toAcct.GetAccountResource().GetAcquiredDelegatedFrozenBalanceForEnergy() != 5_000_000 {
		t.Fatalf("to acquired energy = %d, want 5_000_000",
			toAcct.GetAccountResource().GetAcquiredDelegatedFrozenBalanceForEnergy())
	}
	if got := allFrozenBalanceForEnergy(toAcct); got != 5_000_000 {
		t.Fatalf("to getAllFrozenBalanceForEnergy = %d, want 5_000_000", got)
	}
	// weight credited to the network + the delegation entry + index recorded.
	if w, _ := st.Properties.TotalEnergyWeight(); w != 5 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 5", w)
	}
	dr, err := st.Delegated.Get(from, to)
	if err != nil || dr.GetFrozenBalanceForEnergy() != 5_000_000 {
		t.Fatalf("delegation entry = %+v (err %v)", dr, err)
	}
	idx, _ := st.DelegatedIndex.Get(from)
	if len(idx.GetToAccounts()) != 1 {
		t.Fatalf("from index toAccounts = %v", idx.GetToAccounts())
	}

	// Early unfreeze rejected; after expiry it releases and reverses every effect.
	if _, err := Apply(st, unfreezeToTx(t, from, to, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "not time") {
		t.Fatalf("early delegated unfreeze: err = %v", err)
	}
	if err := st.Properties.SaveLatestBlockHeaderTimestamp(t0 + 3*dayMs); err != nil {
		t.Fatal(err)
	}
	mustApply(t, st, unfreezeToTx(t, from, to, core.ResourceCode_ENERGY))

	fromAcct, _ = st.Accounts.Get(from)
	if fromAcct.GetBalance() != 10_000_000 ||
		fromAcct.GetAccountResource().GetDelegatedFrozenBalanceForEnergy() != 0 {
		t.Fatalf("from after unfreeze: bal %d delegated-out %d", fromAcct.GetBalance(),
			fromAcct.GetAccountResource().GetDelegatedFrozenBalanceForEnergy())
	}
	toAcct, _ = st.Accounts.Get(to)
	if toAcct.GetAccountResource().GetAcquiredDelegatedFrozenBalanceForEnergy() != 0 {
		t.Fatal("to acquired energy must be zeroed after unfreeze")
	}
	if _, err := st.Delegated.Get(from, to); err == nil {
		t.Fatal("delegation entry must be deleted when fully unfrozen")
	}
	if w, _ := st.Properties.TotalEnergyWeight(); w != 0 {
		t.Fatalf("TOTAL_ENERGY_WEIGHT = %d, want 0", w)
	}
}

// TestDelegateEnergyExpiryQuirk reproduces the DelegatedResourceCapsule.getExpireTimeForEnergy
// bug: while ALLOW_MULTI_SIGN is off, a delegated-ENERGY unfreeze's expiry check reads the
// (never-set, zero) BANDWIDTH expire time, so the stake is unfreezable IMMEDIATELY — even
// though its real energy expiry is days away.
func TestDelegateEnergyExpiryQuirk(t *testing.T) {
	from, to := addr21(0x2a), addr21(0x2b)
	const t0 = int64(1_600_000_000_000)
	st, _ := newChainState(t, from, 10_000_000, t0) // ALLOW_MULTI_SIGN defaults 0
	if err := st.Accounts.Put(&core.Account{Address: to, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	enableDelegation(t, st)

	mustApply(t, st, freezeToTx(t, from, to, 5_000_000, 3, core.ResourceCode_ENERGY))
	// "now" is still t0 (well before the 3-day energy expiry), yet the unfreeze succeeds
	// because the quirk reads the zero bandwidth expiry.
	mustApply(t, st, unfreezeToTx(t, from, to, core.ResourceCode_ENERGY))
	if _, err := st.Delegated.Get(from, to); err == nil {
		t.Fatal("quirk: delegated energy should have unfrozen immediately")
	}
}

// TestDelegateEnergyPowersReceiverVM: A freezes energy FOR B (energy rental); B's contract
// call is then paid from the delegated-in stake — the receiver, holding no stake of its own,
// spends the delegator's.
func TestDelegateEnergyPowersReceiverVM(t *testing.T) {
	from, to := addr21(0x23), addr21(0x24)
	st, d := newChainState(t, from, 2_000_000_000, 1_600_000_000_000)
	if err := st.Accounts.Put(&core.Account{Address: to, Balance: 100_000_000, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	enableDelegation(t, st)

	// B deploys a contract that SSTOREs on trigger.
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x00}
	res := applyInSession(t, st, d, createTx(t, to, deployer(runtime)), 1)
	contractAddr := res.Receipts[0].ContractAddress

	// A freezes 1000 TRX of ENERGY for B.
	d.BuildSession()
	if _, err := Apply(st, freezeToTx(t, from, to, 1_000_000_000, 3, core.ResourceCode_ENERGY),
		BlockContext{Number: 2, Timestamp: 6000, Version: 35}); err != nil {
		t.Fatalf("delegate freeze: %v", err)
	}
	if _, err := d.Commit(); err != nil {
		t.Fatal(err)
	}

	// B triggers its contract: covered by the delegated-in energy — no TRX burned.
	res = applyInSession(t, st, d, triggerTx(t, to, contractAddr, nil), 3)
	bill := res.Receipts[0].Energy
	if bill.EnergyUsage <= 0 || bill.EnergyFee != 0 {
		t.Fatalf("receiver receipt = %+v, want delegated-stake-covered (usage>0, fee 0)", bill)
	}
}

// TestDelegateIgnoredWhenGateOff: with ALLOW_DELEGATE_RESOURCE off, a receiver-set freeze is
// treated as a self-freeze (java-tron supportDR()==false), leaving no delegation entry.
func TestDelegateIgnoredWhenGateOff(t *testing.T) {
	from, to := addr21(0x25), addr21(0x26)
	st, _ := newChainState(t, from, 10_000_000, 1_600_000_000_000)
	if err := st.Accounts.Put(&core.Account{Address: to, Type: core.AccountType_Normal}); err != nil {
		t.Fatal(err)
	}
	// gate left OFF.
	mustApply(t, st, freezeToTx(t, from, to, 5_000_000, 3, core.ResourceCode_ENERGY))

	fromAcct, _ := st.Accounts.Get(from)
	if energyFrozen(fromAcct) != 5_000_000 {
		t.Fatalf("gate off: expected self-freeze on owner, got energyFrozen %d", energyFrozen(fromAcct))
	}
	if _, err := st.Delegated.Get(from, to); err == nil {
		t.Fatal("gate off: no delegation entry may be created")
	}
	toAcct, _ := st.Accounts.Get(to)
	if toAcct.GetAccountResource().GetAcquiredDelegatedFrozenBalanceForEnergy() != 0 {
		t.Fatal("gate off: receiver must not be credited")
	}
}

// TestDelegateValidation covers the delegated-specific validation branches.
func TestDelegateValidation(t *testing.T) {
	from := addr21(0x27)
	st, _ := newChainState(t, from, 10_000_000, 1_600_000_000_000)
	enableDelegation(t, st)

	// self-delegation rejected.
	if _, err := Apply(st, freezeToTx(t, from, from, 5_000_000, 3, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "same as ownerAddress") {
		t.Fatalf("self-delegate: err = %v", err)
	}
	// receiver does not exist.
	if _, err := Apply(st, freezeToTx(t, from, addr21(0x99), 5_000_000, 3, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing receiver: err = %v", err)
	}
	// contract receiver rejected once ALLOW_TVM_CONSTANTINOPLE is on.
	cAddr := addr21(0x28)
	if err := st.Accounts.Put(&core.Account{Address: cAddr, Type: core.AccountType_Contract}); err != nil {
		t.Fatal(err)
	}
	if err := st.Properties.PutInt64([]byte("ALLOW_TVM_CONSTANTINOPLE"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(st, freezeToTx(t, from, cAddr, 5_000_000, 3, core.ResourceCode_ENERGY), BlockContext{}); err == nil ||
		!strings.Contains(err.Error(), "contract addresses") {
		t.Fatalf("contract receiver: err = %v", err)
	}
}
