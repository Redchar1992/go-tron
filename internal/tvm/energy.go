package tvm

import "errors"

// ErrOutOfEnergy is returned when an operation's energy cost exceeds the remaining
// energy budget (java-tron OutOfEnergyException).
var ErrOutOfEnergy = errors.New("tvm: out of energy")

// energyMeter tracks energy consumption against a fixed limit, mirroring java-tron's
// Program energy accounting (spendEnergy / getEnergyLimitLeft). Energy is TRON's name
// for EVM gas; the per-opcode costs are the same schedule.
type energyMeter struct {
	limit  uint64
	used   uint64
	refund uint64 // accumulated SSTORE-clear refund, applied at the end of execution
}

func newEnergyMeter(limit uint64) *energyMeter { return &energyMeter{limit: limit} }

// spend charges cost energy, returning ErrOutOfEnergy if the budget is exceeded. The
// check is done before mutating `used`, so on failure the meter is left at the limit
// boundary and no partial charge leaks.
func (m *energyMeter) spend(cost uint64) error {
	if cost > m.limit-m.used {
		m.used = m.limit
		return ErrOutOfEnergy
	}
	m.used += cost
	return nil
}

// remaining returns the energy left in the budget.
func (m *energyMeter) remaining() uint64 { return m.limit - m.used }

// addRefund accumulates refund energy (SSTORE clearing). Applied, capped, at the end.
func (m *energyMeter) addRefund(v uint64) { m.refund += v }
