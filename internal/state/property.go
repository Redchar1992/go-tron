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
	propTotalEnergyWeight       = []byte("TOTAL_ENERGY_WEIGHT")
	propTotalEnergyLimit        = []byte("TOTAL_ENERGY_LIMIT")
	propTotalEnergyCurrentLimit = []byte("TOTAL_ENERGY_CURRENT_LIMIT")
	propUnfreezeDelayDays       = []byte("UNFREEZE_DELAY_DAYS")
	propAllowNewReward          = []byte("ALLOW_NEW_REWARD")
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
		{propUnfreezeDelayDays, 0},
		{propAllowNewReward, 0},
	} {
		if err := s.PutInt64(kv.k, kv.v); err != nil {
			return err
		}
	}
	return nil
}
