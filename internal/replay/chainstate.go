package replay

import (
	"encoding/json"
	"fmt"
	"os"
)

// ChainState is the network + per-account RESOURCE snapshot a mid-chain replay needs so the
// staked-energy derivation returns the same values as the on-chain receipts (M3.5e §4.1). An
// archive node captures it at the replay window's starting height; the node seeds the
// PropertyStore globals and each account's resource fields from it before the window runs,
// after which the freeze/unfreeze actuators maintain them forward.
//
// It is the resource-state complement to PreState: PreState carries VM code/storage/balance
// (feeding cross-contract reads); ChainState carries the energy-accounting inputs (network
// weights, the ENERGY_FEE of the era, and each caller's stake/usage) — the §4.2 blocker for
// real-block energy sign-off.
type ChainState struct {
	Number       int64                      `json:"number"`
	DynamicProps DynamicProps               `json:"dynamicProps"`
	Accounts     map[string]AccountResource `json:"accounts"` // hex 41-address -> resource state
}

// DynamicProps are the DynamicPropertiesStore globals at the height. A zero field means
// "leave the genesis default" (so a fixture need only set what it overrides).
type DynamicProps struct {
	TotalEnergyWeight       int64 `json:"totalEnergyWeight"`
	TotalEnergyCurrentLimit int64 `json:"totalEnergyCurrentLimit"`
	TotalNetWeight          int64 `json:"totalNetWeight"`
	EnergyFee               int64 `json:"energyFee"` // sun/energy for this era (100/140/280/420)
	UnfreezeDelayDays       int64 `json:"unfreezeDelayDays"`
	AllowNewReward          int64 `json:"allowNewReward"`
}

// AccountResource is one account's staking/resource state at the height (all in sun where
// applicable). A zero field is left at its stored/default value.
type AccountResource struct {
	Balance                    int64 `json:"balance"`
	FrozenBalanceForEnergy     int64 `json:"frozenBalanceForEnergy"`     // V1 self energy stake
	FrozenV2Energy             int64 `json:"frozenV2Energy"`             // V2 self energy stake
	AcquiredDelegatedEnergy    int64 `json:"acquiredDelegatedEnergy"`    // V1 delegated-in energy
	AcquiredDelegatedV2Energy  int64 `json:"acquiredDelegatedV2Energy"`  // V2 delegated-in energy
	EnergyUsage                int64 `json:"energyUsage"`                // pre-recovery usage
	LatestConsumeTimeForEnergy int64 `json:"latestConsumeTimeForEnergy"` // consume slot
	EnergyWindowSize           int64 `json:"energyWindowSize"`
	EnergyWindowOptimized      bool  `json:"energyWindowOptimized"`
}

// LoadChainState reads a resource-state fixture (chainstate.json).
func LoadChainState(path string) (*ChainState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cs ChainState
	if err := json.Unmarshal(raw, &cs); err != nil {
		return nil, fmt.Errorf("replay: parse %s: %w", path, err)
	}
	return &cs, nil
}
