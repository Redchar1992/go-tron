package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Redchar1992/go-tron/internal/db"
)

// PropertyStore is go-tron's analog of java-tron's DynamicPropertiesStore: a flat namespace
// of named scalar chain properties. Each value is an int64 serialized big-endian over 8
// bytes, matching java-tron's ByteArray.fromLong / ByteArray.toLong, so a go-tron property
// value is byte-identical to the one java-tron stores under the same key.
//
// It surfaces the network-global resource parameters the staked-energy derivation reads
// (internal/actuator/energy.go): TOTAL_ENERGY_WEIGHT, TOTAL_ENERGY_CURRENT_LIMIT, and the
// UNFREEZE_DELAY_DAYS / ALLOW_NEW_REWARD gate flags. GetInt64/PutInt64 accept any string
// key, so further properties are added without a schema change — exactly as
// DynamicPropertiesStore grows one key at a time.
type PropertyStore struct{ db *db.Database }

// Property keys — kept byte-identical to java-tron DynamicPropertiesStore's string keys
// (DynamicResourceProperties.* / DynamicProperties.*) so a go-tron property DB is legible
// against a java-tron one.
var (
	propTotalEnergyWeight          = []byte("TOTAL_ENERGY_WEIGHT")
	propTotalEnergyLimit           = []byte("TOTAL_ENERGY_LIMIT")
	propTotalEnergyCurrentLimit    = []byte("TOTAL_ENERGY_CURRENT_LIMIT")
	propTotalNetWeight             = []byte("TOTAL_NET_WEIGHT")
	propUnfreezeDelayDays          = []byte("UNFREEZE_DELAY_DAYS")
	propAllowNewReward             = []byte("ALLOW_NEW_REWARD")
	propAllowDelegateResource      = []byte("ALLOW_DELEGATE_RESOURCE")
	propMinFrozenTime              = []byte("MIN_FROZEN_TIME")
	propMaxFrozenTime              = []byte("MAX_FROZEN_TIME")
	propLatestBlockHeaderTimestamp = []byte("LATEST_BLOCK_HEADER_TIMESTAMP")
	propAllowMultiSign             = []byte("ALLOW_MULTI_SIGN")
	propAllowTvmConstantinople     = []byte("ALLOW_TVM_CONSTANTINOPLE")
	propAllowTvmSolidity059        = []byte("ALLOW_TVM_SOLIDITY_059")
	propAllowDelegateOptimization  = []byte("ALLOW_DELEGATE_OPTIMIZATION")
)

// DefaultTotalEnergyLimit is java-tron's genesis TOTAL_ENERGY_LIMIT — the value the
// DynamicPropertiesStore constructor persists on a fresh DB, before any on-chain proposal
// raises it (mainnet later moved it to 90e9 via proposal, which go-tron does not process
// yet). TOTAL_ENERGY_CURRENT_LIMIT seeds to the same value.
const DefaultTotalEnergyLimit int64 = 50_000_000_000

// PutInt64 stores v under key as an 8-byte big-endian value (java-tron ByteArray.fromLong).
func (s *PropertyStore) PutInt64(key []byte, v int64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return s.db.Put(nsKey(propertyPrefix, key), b[:])
}

// GetInt64 returns (value, true) for a set property, or (0, false) when it is absent —
// mirroring the way DynamicPropertiesStore falls back to a genesis default for a key its
// constructor has not written.
func (s *PropertyStore) GetInt64(key []byte) (int64, bool, error) {
	b, err := s.db.Get(nsKey(propertyPrefix, key))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if len(b) != 8 {
		return 0, false, fmt.Errorf("property %q: want 8 bytes, got %d", key, len(b))
	}
	return int64(binary.BigEndian.Uint64(b)), true, nil
}

// getOr returns the property's stored value, or def when it is unset.
func (s *PropertyStore) getOr(key []byte, def int64) (int64, error) {
	v, ok, err := s.GetInt64(key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return def, nil
	}
	return v, nil
}

// TotalEnergyWeight returns TOTAL_ENERGY_WEIGHT: the whole-TRX weight of all energy stake on
// the network (getTotalEnergyWeight). Genesis default 0.
func (s *PropertyStore) TotalEnergyWeight() (int64, error) {
	return s.getOr(propTotalEnergyWeight, 0)
}

// TotalEnergyCurrentLimit returns TOTAL_ENERGY_CURRENT_LIMIT (getTotalEnergyCurrentLimit).
// Genesis default = TOTAL_ENERGY_LIMIT.
func (s *PropertyStore) TotalEnergyCurrentLimit() (int64, error) {
	return s.getOr(propTotalEnergyCurrentLimit, DefaultTotalEnergyLimit)
}

// SupportUnfreezeDelay reports getUnfreezeDelayDays() > 0 — i.e. Stake2.0 is active and the
// V2 global-energy-limit formula applies. Genesis default false.
func (s *PropertyStore) SupportUnfreezeDelay() (bool, error) {
	d, err := s.getOr(propUnfreezeDelayDays, 0)
	return d > 0, err
}

// AllowNewReward reports getAllowNewReward() == 1, which gates the V1 totalEnergyWeight<=0
// -> 0 branch in calculateGlobalEnergyLimit. Genesis default false.
func (s *PropertyStore) AllowNewReward() (bool, error) {
	v, err := s.getOr(propAllowNewReward, 0)
	return v == 1, err
}

// TotalNetWeight returns TOTAL_NET_WEIGHT: the whole-TRX weight of all bandwidth stake on
// the network (getTotalNetWeight). Genesis default 0.
func (s *PropertyStore) TotalNetWeight() (int64, error) {
	return s.getOr(propTotalNetWeight, 0)
}

// SupportDR reports getAllowDelegateResource() == 1 — V1 resource delegation (proposal #15)
// is active. Genesis default false.
func (s *PropertyStore) SupportDR() (bool, error) {
	v, err := s.getOr(propAllowDelegateResource, 0)
	return v == 1, err
}

// SupportAllowDelegateOptimization reports getAllowDelegateOptimization() == 1: the
// optimized per-edge DelegatedResourceAccountIndex layout is active. Genesis default false.
func (s *PropertyStore) SupportAllowDelegateOptimization() (bool, error) {
	v, err := s.getOr(propAllowDelegateOptimization, 0)
	return v == 1, err
}

// AllowMultiSign reports getAllowMultiSign() == 1 (proposal #16). Genesis default false.
// Its consensus-relevant side effect here: while OFF, a delegated-ENERGY unfreeze's expiry
// check reads the BANDWIDTH expire time (DelegatedResourceCapsule.getExpireTimeForEnergy) —
// a preserved historical bug.
func (s *PropertyStore) AllowMultiSign() (bool, error) {
	v, err := s.getOr(propAllowMultiSign, 0)
	return v == 1, err
}

// AllowTvmConstantinople reports the ALLOW_TVM_CONSTANTINOPLE chain parameter (#30) as the
// ACTUATOR-side gate (contract-receiver checks in V1 delegation). Genesis default 0. Note
// this is the proposal-store view; the TVM's own opcode gating is derived from the block
// header version (tvm.VMConfigForVersion) — java-tron likewise reads the property here.
func (s *PropertyStore) AllowTvmConstantinople() (bool, error) {
	v, err := s.getOr(propAllowTvmConstantinople, 0)
	return v == 1, err
}

// AllowTvmSolidity059 reports ALLOW_TVM_SOLIDITY_059 (#32) as the actuator-side gate (the
// under-acquired clamp in delegated unfreeze). Genesis default 0.
func (s *PropertyStore) AllowTvmSolidity059() (bool, error) {
	v, err := s.getOr(propAllowTvmSolidity059, 0)
	return v == 1, err
}

// MinFrozenTime / MaxFrozenTime are the V1 freeze duration bounds in days
// (getMinFrozenTime/getMaxFrozenTime). Genesis default 3/3 — mainnet never changed them.
func (s *PropertyStore) MinFrozenTime() (int64, error) { return s.getOr(propMinFrozenTime, 3) }

// MaxFrozenTime — see MinFrozenTime.
func (s *PropertyStore) MaxFrozenTime() (int64, error) { return s.getOr(propMaxFrozenTime, 3) }

// LatestBlockHeaderTimestamp returns LATEST_BLOCK_HEADER_TIMESTAMP (ms): java-tron's
// getLatestBlockHeaderTimestamp. CONSENSUS-CRITICAL ordering note: java-tron saves it in
// updateDynamicProperties AFTER a block's transactions are processed, so during block-N
// execution actuators read block N-1's timestamp as "now" (freeze expire times, unfreeze
// eligibility, resource-usage slots). The Manager mirrors that ordering. Default 0 when a
// chain root was never installed (bare unit tests).
func (s *PropertyStore) LatestBlockHeaderTimestamp() (int64, error) {
	return s.getOr(propLatestBlockHeaderTimestamp, 0)
}

// SaveLatestBlockHeaderTimestamp persists the header-timestamp property (ms).
func (s *PropertyStore) SaveLatestBlockHeaderTimestamp(ms int64) error {
	return s.PutInt64(propLatestBlockHeaderTimestamp, ms)
}

// addWeight implements DynamicPropertiesStore.addTotalNetWeight/addTotalEnergyWeight: add
// amount to the property, clamping at 0 only once allowNewReward is active. A zero amount
// is a no-op (java-tron early-returns without touching the store).
func (s *PropertyStore) addWeight(key []byte, amount int64) error {
	if amount == 0 {
		return nil
	}
	w, err := s.getOr(key, 0)
	if err != nil {
		return err
	}
	w += amount
	newReward, err := s.AllowNewReward()
	if err != nil {
		return err
	}
	if newReward && w < 0 {
		w = 0
	}
	return s.PutInt64(key, w)
}

// AddTotalNetWeight adds amount (whole TRX, may be negative) to TOTAL_NET_WEIGHT.
func (s *PropertyStore) AddTotalNetWeight(amount int64) error {
	return s.addWeight(propTotalNetWeight, amount)
}

// AddTotalEnergyWeight adds amount (whole TRX, may be negative) to TOTAL_ENERGY_WEIGHT.
func (s *PropertyStore) AddTotalEnergyWeight(amount int64) error {
	return s.addWeight(propTotalEnergyWeight, amount)
}

// SeedGenesisDefaults persists the fresh-chain resource-property defaults java-tron's
// DynamicPropertiesStore constructor writes at DB init (pre-proposal mainnet config):
// TOTAL_ENERGY_WEIGHT=0, TOTAL_ENERGY_LIMIT=TOTAL_ENERGY_CURRENT_LIMIT=DefaultTotalEnergyLimit,
// UNFREEZE_DELAY_DAYS=0, ALLOW_NEW_REWARD=0. Genesis loading calls this so the properties are
// materialized in state (revocable with the rest of the stores) rather than only implied by
// the getOr fallbacks.
func (s *PropertyStore) SeedGenesisDefaults() error {
	for _, kv := range []struct {
		k []byte
		v int64
	}{
		{propTotalEnergyWeight, 0},
		{propTotalEnergyLimit, DefaultTotalEnergyLimit},
		{propTotalEnergyCurrentLimit, DefaultTotalEnergyLimit},
		{propTotalNetWeight, 0},
		{propUnfreezeDelayDays, 0},
		{propAllowNewReward, 0},
		{propAllowDelegateResource, 0},
		{propMinFrozenTime, 3},
		{propMaxFrozenTime, 3},
		{propAllowMultiSign, 0},
		{propAllowTvmConstantinople, 0},
		{propAllowTvmSolidity059, 0},
		{propAllowDelegateOptimization, 0},
	} {
		if err := s.PutInt64(kv.k, kv.v); err != nil {
			return err
		}
	}
	return nil
}
