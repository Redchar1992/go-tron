// Package actuator implements native (non-VM) transaction executors, dispatched by
// Transaction.Contract.ContractType, mirroring java-tron's TransactionFactory registry.
//
// M2 scope: the block-pipeline + replay milestone targets *root* equivalence (block id
// + txTrieRoot), which does not depend on state mutation. To make the pipeline real and
// to exercise the revoking-session rollback that switchFork relies on, this package
// implements TransferContract apply (debit owner / credit recipient / create recipient
// account) WITHOUT fee, bandwidth, or energy accounting — those, plus the remaining
// contract types, are byte-exact concerns deferred to later milestones. Unregistered
// contract types are accepted as no-ops so contract-bearing blocks still replay for the
// purpose of root verification.
//
// CONSENSUS-CRITICAL (eventually): validate/execute/calcFee must become byte-identical
// to java-tron. Today it is deliberately partial; see per-actuator notes.
package actuator

import (
	"bytes"
	"errors"
	"fmt"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// ErrInsufficientBalance is returned when an owner cannot cover a transfer amount.
var ErrInsufficientBalance = errors.New("actuator: insufficient balance")

// Context carries the mutable state stores and the contract under execution.
type Context struct {
	State    *state.State
	Contract *core.Transaction_Contract
}

// Actuator validates and executes one contract type against state.
type Actuator interface {
	Validate(*Context) error
	Execute(*Context) error
}

// registry maps a ContractType to its actuator. Types absent from the registry are
// treated as no-ops in M2 (see package doc).
var registry = map[core.Transaction_Contract_ContractType]Actuator{
	core.Transaction_Contract_TransferContract: transferActuator{},
}

// Apply runs every contract in a transaction against state: validate then execute, in
// order. Unregistered contract types are skipped (no-op) and reported via the returned
// count of unhandled contracts.
func Apply(st *state.State, tx *core.Transaction) (unhandled int, err error) {
	for i, c := range tx.GetRawData().GetContract() {
		act, ok := registry[c.GetType()]
		if !ok {
			unhandled++
			continue
		}
		ctx := &Context{State: st, Contract: c}
		if err := act.Validate(ctx); err != nil {
			return unhandled, fmt.Errorf("contract %d (%v) validate: %w", i, c.GetType(), err)
		}
		if err := act.Execute(ctx); err != nil {
			return unhandled, fmt.Errorf("contract %d (%v) execute: %w", i, c.GetType(), err)
		}
	}
	return unhandled, nil
}

// transferActuator handles TransferContract (TRX transfer). Fee/bandwidth accounting is
// intentionally omitted in M2 (see package doc).
type transferActuator struct{}

func (transferActuator) unpack(ctx *Context) (*core.TransferContract, error) {
	tc := new(core.TransferContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(tc); err != nil {
		return nil, fmt.Errorf("unpack TransferContract: %w", err)
	}
	return tc, nil
}

func (a transferActuator) Validate(ctx *Context) error {
	tc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if tc.GetAmount() <= 0 {
		return fmt.Errorf("actuator: transfer amount must be positive, got %d", tc.GetAmount())
	}
	if bytes.Equal(tc.GetOwnerAddress(), tc.GetToAddress()) {
		return errors.New("actuator: cannot transfer to self")
	}
	owner, err := ctx.State.Accounts.Get(tc.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: owner account missing: %w", err)
	}
	if owner.GetBalance() < tc.GetAmount() {
		return ErrInsufficientBalance
	}
	return nil
}

func (a transferActuator) Execute(ctx *Context) error {
	tc, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	owner, err := ctx.State.Accounts.Get(tc.GetOwnerAddress())
	if err != nil {
		return err
	}
	to, err := ctx.State.Accounts.Get(tc.GetToAddress())
	if err != nil {
		// Recipient does not exist yet: create a default Normal account (java-tron
		// createDefaultAccount). The create-account fee is deferred to a later milestone.
		to = &core.Account{Address: tc.GetToAddress(), Type: core.AccountType_Normal}
	}
	owner.Balance -= tc.GetAmount()
	to.Balance += tc.GetAmount()
	if err := ctx.State.Accounts.Put(owner); err != nil {
		return err
	}
	return ctx.State.Accounts.Put(to)
}
