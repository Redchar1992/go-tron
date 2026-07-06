package vmoracle

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/holiman/uint256"

	"github.com/Redchar1992/go-tron/internal/tvm"
)

// trackingStateDB wraps a tvm.StateDB and records the (address, slot) pairs written during
// execution, so the executor can report the net storage delta. Snapshot/Revert flow through
// to the wrapped DB unchanged (reverted writes read back their pre-value, so netWrites
// naturally excludes them); the write set itself is not reverted — it only bounds which slots
// netWrites re-reads.
type trackingStateDB struct {
	tvm.StateDB
	writes map[string]map[[32]byte]struct{}
}

func (t *trackingStateDB) SetStorage(addr []byte, key [32]byte, value [32]byte) {
	m := t.writes[string(addr)]
	if m == nil {
		m = map[[32]byte]struct{}{}
		t.writes[string(addr)] = m
	}
	m[key] = struct{}{}
	t.StateDB.SetStorage(addr, key, value)
}

// netWrites returns 41-addr(hex) -> slot(hex) -> value(hex) for every written slot whose
// FINAL value differs from the World's input value — the unambiguous net storage effect that
// both VMs can compute regardless of write-set-semantics nuances.
func (t *trackingStateDB) netWrites(input map[string]map[[32]byte][32]byte) map[string]map[string]string {
	out := map[string]map[string]string{}
	for addrStr, slots := range t.writes {
		for slot := range slots {
			final, _ := t.StateDB.GetStorage([]byte(addrStr), slot)
			var in [32]byte
			if im := input[addrStr]; im != nil {
				in = im[slot]
			}
			if final == in {
				continue
			}
			addrHex := hex.EncodeToString([]byte(addrStr))
			if out[addrHex] == nil {
				out[addrHex] = map[string]string{}
			}
			out[addrHex][hex.EncodeToString(slot[:])] = hex.EncodeToString(final[:])
		}
	}
	return out
}

// loadWorld seeds a MemStateDB from the World and returns the input storage (for netWrites'
// delta comparison). Called before wrapping in trackingStateDB so setup writes aren't tracked.
func loadWorld(mem *tvm.MemStateDB, w World) (map[string]map[[32]byte][32]byte, error) {
	input := map[string]map[[32]byte][32]byte{}
	for addrHex, acct := range w.Accounts {
		addr, err := decodeAddr(addrHex)
		if err != nil {
			return nil, fmt.Errorf("account %q: %w", addrHex, err)
		}
		if acct.Balance > 0 {
			mem.AddBalance(addr, uint256.NewInt(uint64(acct.Balance)))
		}
		if acct.Code != "" {
			code, err := decodeHex(acct.Code)
			if err != nil {
				return nil, fmt.Errorf("account %q code: %w", addrHex, err)
			}
			mem.SetCode(addr, code)
		}
		if len(acct.Storage) > 0 {
			sm := map[[32]byte][32]byte{}
			for slotHex, valHex := range acct.Storage {
				slot, err := word32(slotHex)
				if err != nil {
					return nil, fmt.Errorf("account %q slot %q: %w", addrHex, slotHex, err)
				}
				val, err := word32(valHex)
				if err != nil {
					return nil, fmt.Errorf("account %q value %q: %w", addrHex, valHex, err)
				}
				mem.SetStorage(addr, slot, val)
				sm[slot] = val
			}
			input[string(addr)] = sm
		}
	}
	return input, nil
}

func decodeHex(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

func decodeAddr(s string) ([]byte, error) {
	b, err := decodeHex(s)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("empty address")
	}
	return b, nil
}

// word32 parses a hex string into a right-aligned (big-endian) 32-byte word.
func word32(s string) ([32]byte, error) {
	b, err := decodeHex(s)
	if err != nil {
		return [32]byte{}, err
	}
	var w [32]byte
	if len(b) > 32 {
		return w, fmt.Errorf("word > 32 bytes (%d)", len(b))
	}
	copy(w[32-len(b):], b)
	return w, nil
}
