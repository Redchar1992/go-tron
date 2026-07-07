package actuator

import (
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// VoteWitnessContract actuator — DPoS vote casting. An account spends its "TRON power" (all
// staked TRX except TRON_POWER-typed V2 stake) on candidate witnesses; the votes are recorded
// on the account and in the VotesStore, and the maintenance window later tallies (new - old)
// into each witness's vote count to elect the 27 SRs.
//
// Faithful to java-tron VoteWitnessActuator.countVoteAccount. DEFERRED: the
// MortgageService.withdrawReward(owner) settlement countVoteAccount runs first (it flushes
// pending vote rewards into the account's allowance before re-voting) — go-tron has no reward
// subsystem yet (the per-cycle vote/block reward accounting + maintenance SR election + the
// WithdrawBalance actuator are the next slice), so on a chain with no accrued rewards it is a
// no-op. Vote CASTING + the VotesStore are exact; vote TALLYING (maintenance) is deferred.
const maxVoteNumber = 30 // Parameter.ChainConstant.MAX_VOTE_NUMBER

// tronPower is AccountCapsule.getTronPower(): the account's total vote power in sun — every
// staked TRX (V1 frozen bandwidth+energy + V1 delegated-out, V2 frozen (non-TRON_POWER) + V2
// delegated-out) counts as votes.
func tronPower(a *core.Account) int64 {
	res := a.GetAccountResource()
	tp := sumFrozen(a) // V1 bandwidth frozen
	tp += res.GetFrozenBalanceForEnergy().GetFrozenBalance()
	tp += a.GetDelegatedFrozenBalanceForBandwidth()
	tp += res.GetDelegatedFrozenBalanceForEnergy()
	for _, f := range a.GetFrozenV2() {
		if f.GetType() != core.ResourceCode_TRON_POWER {
			tp += f.GetAmount()
		}
	}
	tp += a.GetDelegatedFrozenV2BalanceForBandwidth()
	tp += res.GetDelegatedFrozenV2BalanceForEnergy()
	return tp
}

type voteWitnessActuator struct{}

func (voteWitnessActuator) unpack(ctx *Context) (*core.VoteWitnessContract, error) {
	c := new(core.VoteWitnessContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack VoteWitnessContract: %w", err)
	}
	return c, nil
}

func (a voteWitnessActuator) Validate(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	if _, err := address.FromBytes(c.GetOwnerAddress()); err != nil {
		return fmt.Errorf("actuator: invalid owner address: %w", err)
	}
	votes := c.GetVotes()
	if len(votes) == 0 {
		return errors.New("actuator: VoteNumber must be more than 0")
	}
	if len(votes) > maxVoteNumber {
		return fmt.Errorf("actuator: VoteNumber more than maxVoteNumber %d", maxVoteNumber)
	}
	owner, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return fmt.Errorf("actuator: vote owner account missing: %w", err)
	}
	var sum int64
	for _, v := range votes {
		if _, err := address.FromBytes(v.GetVoteAddress()); err != nil {
			return errors.New("actuator: invalid vote address!")
		}
		if v.GetVoteCount() <= 0 {
			return errors.New("actuator: vote count must be greater than 0")
		}
		has, err := ctx.State.Witnesses.Has(v.GetVoteAddress())
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf("actuator: witness [%x] does not exist", v.GetVoteAddress())
		}
		sum += v.GetVoteCount()
	}
	// Votes are in whole TRX; the account's power is in sun.
	if sum*trxPrecision > tronPower(owner) {
		return errors.New("actuator: the total number of votes exceeds the tronPower")
	}
	return nil
}

func (a voteWitnessActuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	owner, err := ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}

	// Settle pending vote rewards first (mortgageService.withdrawReward): flush accrued rewards
	// into the account's allowance before its votes are overwritten. No-op unless
	// allowChangeDelegation. Re-read the account afterward — WithdrawReward mutates its allowance.
	if err := WithdrawReward(ctx.State, c.GetOwnerAddress()); err != nil {
		return err
	}
	owner, err = ctx.State.Accounts.Get(c.GetOwnerAddress())
	if err != nil {
		return err
	}

	// The VotesStore entry keeps OLD votes (last maintenance tally). A first-time voter seeds
	// OldVotes from the account's current votes; then NewVotes is replaced by this cast.
	votes, err := ctx.State.Votes.Get(c.GetOwnerAddress())
	if err != nil {
		votes = &core.Votes{Address: c.GetOwnerAddress(), OldVotes: cloneVotes(owner.GetVotes())}
	}
	votes.NewVotes = nil
	owner.Votes = nil
	for _, v := range c.GetVotes() {
		votes.NewVotes = append(votes.NewVotes, &core.Vote{VoteAddress: v.GetVoteAddress(), VoteCount: v.GetVoteCount()})
		owner.Votes = append(owner.Votes, &core.Vote{VoteAddress: v.GetVoteAddress(), VoteCount: v.GetVoteCount()})
	}
	if err := ctx.State.Accounts.Put(owner); err != nil {
		return err
	}
	return ctx.State.Votes.Put(votes)
}

// cloneVotes deep-copies a vote list (so the VotesStore snapshot is independent of the
// account's mutated votes).
func cloneVotes(in []*core.Vote) []*core.Vote {
	out := make([]*core.Vote, 0, len(in))
	for _, v := range in {
		out = append(out, &core.Vote{VoteAddress: v.GetVoteAddress(), VoteCount: v.GetVoteCount()})
	}
	return out
}
