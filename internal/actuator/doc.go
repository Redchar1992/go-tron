// Package actuator implements native (non-VM) transaction executors, dispatched by
// Transaction.Contract.ContractType (Transfer, Freeze/Unfreeze V1+V2, Delegate/
// UnDelegateResource, Vote, Proposal, Exchange, Market, AccountPermissionUpdate, ...).
//
// Mirrors java-tron's TransactionFactory/TransactionRegister registry pattern.
// CONSENSUS-CRITICAL: validate/execute/calcFee and bandwidth+energy accounting must be
// byte-identical to java-tron. Target: M1. M0: placeholder.
package actuator
