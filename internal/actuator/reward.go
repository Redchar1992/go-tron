package actuator

import (
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/Redchar1992/go-tron/internal/address"
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
)

// This file is go-tron's MortgageService — the DPoS reward settlement layer — plus the
// WithdrawBalanceContract actuator that draws an account's accrued allowance into its balance.
//
// The whole subsystem is dormant until proposal #34 (allowChangeDelegation) activates: while
// OFF, PayBlockReward mints nothing per-cycle, cycles never advance (Manager.RunMaintenance),
// and WithdrawReward is a no-op — so WithdrawBalance pays out only the pre-accrued allowance,
// matching a from-genesis java-tron node. Once ON, each produced block funds the current
// cycle's reward pool (brokerage to the SR, the remainder to its voters), the maintenance
// window folds that pool into a per-vote Vi index, and WithdrawReward walks an account's
// [beginCycle,endCycle) span to credit its share. Faithful to
// MortgageService.{payBlockReward,payReward,withdrawReward,queryReward,computeReward} and
// WithdrawBalanceActuator.
//
// DEFERRED (documented at each site): payStandbyWitness (needs the top-127 standby set from SR
// election), the !allowChangeDelegation direct-allowance block reward (needs coinbase-account
// provisioning + full-state replay), the guard-representative withdraw rejection (go-tron does
// not model genesis GRs), and the allowOldRewardOpt fast path (an optimization, not a rule).

// votePair is a flattened (witness, voteCount) — the shape computeReward consumes, from either
// the live account's votes or a cycle's stored AccountVote snapshot.
type votePair struct {
	addr  []byte
	count int64
}

func votePairsFromAccount(a *core.Account) []votePair {
	out := make([]votePair, 0, len(a.GetVotes()))
	for _, v := range a.GetVotes() {
		out = append(out, votePair{addr: v.GetVoteAddress(), count: v.GetVoteCount()})
	}
	return out
}

func votePairsFromSnapshot(av *state.AccountVote) []votePair {
	out := make([]votePair, 0, len(av.Addresses))
	for i, a := range av.Addresses {
		out = append(out, votePair{addr: a, count: av.Counts[i]})
	}
	return out
}

func snapshotFromAccount(a *core.Account) *state.AccountVote {
	av := &state.AccountVote{}
	for _, v := range a.GetVotes() {
		av.Addresses = append(av.Addresses, v.GetVoteAddress())
		av.Counts = append(av.Counts, v.GetVoteCount())
	}
	return av
}

// PayBlockReward credits the block-producing witness's per-block pay into the current cycle's
// reward pool (MortgageService.payBlockReward → payReward). No-op unless allowChangeDelegation.
func PayBlockReward(st *state.State, witnessAddr []byte, value int64) error {
	on, err := st.Properties.AllowChangeDelegation()
	if err != nil {
		return err
	}
	if !on {
		return nil
	}
	return payReward(st, witnessAddr, value)
}

// payReward splits value by the witness's per-cycle brokerage — the brokerage cut goes straight
// to the witness's own allowance, the remainder into the cycle's voter reward pool
// (MortgageService.payReward). The brokerage arithmetic reproduces java-tron's double math
// (percent/100 * value, truncated toward zero) bit-for-bit.
func payReward(st *state.State, witnessAddr []byte, value int64) error {
	cycle, err := st.Properties.CurrentCycleNumber()
	if err != nil {
		return err
	}
	brokerage, err := st.Delegation.GetBrokerageAt(cycle, witnessAddr)
	if err != nil {
		return err
	}
	brokerageAmount := int64(float64(brokerage) / 100 * float64(value))
	value -= brokerageAmount
	if err := st.Delegation.AddReward(cycle, witnessAddr, value); err != nil {
		return err
	}
	return adjustAllowance(st, witnessAddr, brokerageAmount)
}

// adjustAllowance adds a non-negative reward amount to an account's allowance
// (MortgageService.adjustAllowance's reward-crediting path, which early-returns on amount <= 0).
// A missing account is materialized as a default capsule (java-tron getUnchecked).
func adjustAllowance(st *state.State, addr []byte, amount int64) error {
	if amount <= 0 {
		return nil
	}
	acct, err := st.Accounts.Get(addr)
	if err != nil {
		acct = &core.Account{Address: addr}
	}
	acct.Allowance += amount
	return st.Accounts.Put(acct)
}

// computeReward totals an account's reward over [beginCycle, endCycle) for its votes, mirroring
// MortgageService.computeReward: the pre-effective-cycle span uses the old per-cycle algorithm,
// the rest uses the O(1) Vi-index delta. endCycle is exclusive.
func computeReward(st *state.State, beginCycle, endCycle int64, votes []votePair) (int64, error) {
	if beginCycle >= endCycle {
		return 0, nil
	}
	var reward int64
	newAlgoCycle, err := st.Properties.NewRewardAlgorithmEffectiveCycle()
	if err != nil {
		return 0, err
	}
	if beginCycle < newAlgoCycle {
		oldEnd := endCycle
		if newAlgoCycle < oldEnd {
			oldEnd = newAlgoCycle
		}
		r, err := getOldReward(st, beginCycle, oldEnd, votes)
		if err != nil {
			return 0, err
		}
		reward = r
		beginCycle = oldEnd
	}
	if beginCycle < endCycle {
		for _, v := range votes {
			beginVi, err := st.Delegation.GetWitnessVi(beginCycle-1, v.addr)
			if err != nil {
				return 0, err
			}
			endVi, err := st.Delegation.GetWitnessVi(endCycle-1, v.addr)
			if err != nil {
				return 0, err
			}
			deltaVi := new(big.Int).Sub(endVi, beginVi)
			if deltaVi.Sign() <= 0 {
				continue
			}
			term := new(big.Int).Mul(deltaVi, big.NewInt(v.count))
			term.Div(term, state.DecimalOfViReward)
			reward += term.Int64()
		}
	}
	return reward, nil
}

// getOldReward sums the old per-cycle reward across [begin, end) (MortgageService.getOldReward,
// non-optimized path — the allowOldRewardOpt fast path is deferred).
func getOldReward(st *state.State, begin, end int64, votes []votePair) (int64, error) {
	var reward int64
	for cycle := begin; cycle < end; cycle++ {
		r, err := computeRewardOldCycle(st, cycle, votes)
		if err != nil {
			return 0, err
		}
		reward += r
	}
	return reward, nil
}

// computeRewardOldCycle is the pre-new-algorithm single-cycle payout: each vote earns its
// pro-rata (userVote/totalVote) slice of the witness's cycle reward pool. It reproduces
// java-tron's double accumulation with a truncating cast per iteration.
func computeRewardOldCycle(st *state.State, cycle int64, votes []votePair) (int64, error) {
	var reward int64
	for _, v := range votes {
		totalReward, err := st.Delegation.GetReward(cycle, v.addr)
		if err != nil {
			return 0, err
		}
		if totalReward <= 0 {
			continue
		}
		totalVote, err := st.Delegation.GetWitnessVote(cycle, v.addr)
		if err != nil {
			return 0, err
		}
		if totalVote == state.RemarkNoVote || totalVote == 0 {
			continue
		}
		voteRate := float64(v.count) / float64(totalVote)
		reward = int64(float64(reward) + voteRate*float64(totalReward))
	}
	return reward, nil
}

// WithdrawReward settles an account's pending vote rewards into its allowance and advances its
// withdraw cursors, faithful to MortgageService.withdrawReward. No-op unless
// allowChangeDelegation. Called at the head of VoteWitness / UnfreezeBalance(V1/V2) execution
// and by the WithdrawBalance actuator, so rewards are flushed before votes/stake change.
func WithdrawReward(st *state.State, addr []byte) error {
	on, err := st.Properties.AllowChangeDelegation()
	if err != nil {
		return err
	}
	if !on {
		return nil
	}
	acct, accErr := st.Accounts.Get(addr) // accErr != nil ⇔ java's accountCapsule == null
	beginCycle, err := st.Delegation.GetBeginCycle(addr)
	if err != nil {
		return err
	}
	endCycle, err := st.Delegation.GetEndCycle(addr)
	if err != nil {
		return err
	}
	currentCycle, err := st.Properties.CurrentCycleNumber()
	if err != nil {
		return err
	}
	var reward int64
	if beginCycle > currentCycle || accErr != nil {
		return nil
	}
	if beginCycle == currentCycle {
		av, err := st.Delegation.GetAccountVote(beginCycle, addr)
		if err != nil {
			return err
		}
		if av != nil {
			return nil
		}
	}
	// Settle the just-closed cycle if the cursor sits exactly one cycle back.
	if beginCycle+1 == endCycle && beginCycle < currentCycle {
		av, err := st.Delegation.GetAccountVote(beginCycle, addr)
		if err != nil {
			return err
		}
		if av != nil {
			reward, err = computeReward(st, beginCycle, endCycle, votePairsFromSnapshot(av))
			if err != nil {
				return err
			}
			if err := adjustAllowance(st, addr, reward); err != nil {
				return err
			}
			reward = 0
		}
		beginCycle++
	}
	endCycle = currentCycle
	if len(acct.GetVotes()) == 0 {
		return st.Delegation.SetBeginCycle(addr, endCycle+1)
	}
	if beginCycle < endCycle {
		r, err := computeReward(st, beginCycle, endCycle, votePairsFromAccount(acct))
		if err != nil {
			return err
		}
		reward += r
		if err := adjustAllowance(st, addr, reward); err != nil {
			return err
		}
	}
	if err := st.Delegation.SetBeginCycle(addr, endCycle); err != nil {
		return err
	}
	if err := st.Delegation.SetEndCycle(addr, endCycle+1); err != nil {
		return err
	}
	return st.Delegation.SetAccountVote(endCycle, addr, snapshotFromAccount(acct))
}

// queryReward returns the total withdrawable amount (pending vote rewards + current allowance)
// without mutating state — MortgageService.queryReward. Used by the WithdrawBalance validation.
func queryReward(st *state.State, addr []byte) (int64, error) {
	on, err := st.Properties.AllowChangeDelegation()
	if err != nil {
		return 0, err
	}
	if !on {
		return 0, nil
	}
	acct, err := st.Accounts.Get(addr)
	if err != nil {
		return 0, nil // account == null → 0
	}
	beginCycle, err := st.Delegation.GetBeginCycle(addr)
	if err != nil {
		return 0, err
	}
	endCycle, err := st.Delegation.GetEndCycle(addr)
	if err != nil {
		return 0, err
	}
	currentCycle, err := st.Properties.CurrentCycleNumber()
	if err != nil {
		return 0, err
	}
	var reward int64
	if beginCycle > currentCycle {
		return acct.GetAllowance(), nil
	}
	if beginCycle+1 == endCycle && beginCycle < currentCycle {
		av, err := st.Delegation.GetAccountVote(beginCycle, addr)
		if err != nil {
			return 0, err
		}
		if av != nil {
			reward, err = computeReward(st, beginCycle, endCycle, votePairsFromSnapshot(av))
			if err != nil {
				return 0, err
			}
		}
		beginCycle++
	}
	endCycle = currentCycle
	if len(acct.GetVotes()) == 0 {
		return reward + acct.GetAllowance(), nil
	}
	if beginCycle < endCycle {
		r, err := computeReward(st, beginCycle, endCycle, votePairsFromAccount(acct))
		if err != nil {
			return 0, err
		}
		reward += r
	}
	return reward + acct.GetAllowance(), nil
}

// withdrawBalanceActuator implements WithdrawBalanceContract (type 13): flush accrued vote
// rewards, then move the account's whole allowance into its spendable balance and stamp the
// withdraw time. Faithful to WithdrawBalanceActuator.
type withdrawBalanceActuator struct{}

func (withdrawBalanceActuator) unpack(ctx *Context) (*core.WithdrawBalanceContract, error) {
	c := new(core.WithdrawBalanceContract)
	if err := ctx.Contract.GetParameter().UnmarshalTo(c); err != nil {
		return nil, fmt.Errorf("unpack WithdrawBalanceContract: %w", err)
	}
	return c, nil
}

func (a withdrawBalanceActuator) Validate(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	owner := c.GetOwnerAddress()
	if _, err := address.FromBytes(owner); err != nil {
		return errors.New("actuator: Invalid address")
	}
	acct, err := ctx.State.Accounts.Get(owner)
	if err != nil {
		return fmt.Errorf("actuator: Account[%x] does not exist", owner)
	}
	// DEFERRED: the guard-representative rejection (a genesis SR may not withdraw) — go-tron does
	// not model genesis GR status.
	now, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	frozenTime, err := ctx.State.Properties.WitnessAllowanceFrozenTime()
	if err != nil {
		return err
	}
	if now-acct.GetLatestWithdrawTime() < frozenTime*frozenPeriodMs {
		return fmt.Errorf("actuator: The last withdraw time is %d, less than 24 hours",
			acct.GetLatestWithdrawTime())
	}
	q, err := queryReward(ctx.State, owner)
	if err != nil {
		return err
	}
	if acct.GetAllowance() <= 0 && q <= 0 {
		return errors.New("actuator: witnessAccount does not have any reward")
	}
	if acct.GetBalance() > math.MaxInt64-acct.GetAllowance() {
		return errors.New("actuator: balance + allowance overflow")
	}
	return nil
}

func (a withdrawBalanceActuator) Execute(ctx *Context) error {
	c, err := a.unpack(ctx)
	if err != nil {
		return err
	}
	owner := c.GetOwnerAddress()
	if err := WithdrawReward(ctx.State, owner); err != nil {
		return err
	}
	acct, err := ctx.State.Accounts.Get(owner)
	if err != nil {
		return err
	}
	now, err := ctx.State.Properties.LatestBlockHeaderTimestamp()
	if err != nil {
		return err
	}
	acct.Balance += acct.GetAllowance()
	acct.Allowance = 0
	acct.LatestWithdrawTime = now
	return ctx.State.Accounts.Put(acct)
}
