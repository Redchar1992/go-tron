package replay

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// PreState is a captured snapshot of the account/contract state a mid-chain contract replay
// depends on — balances, code, and storage slots that predate the replay window. It is the
// offline form of the M3.5c historical-state oracle: an archive node produces it (see
// capture_fixtures.py), and MapProvider serves it to the node's VM state on a store miss.
//
// MapProvider satisfies actuator.StateProvider structurally (its methods use only builtin
// types), so this package needs no dependency on internal/actuator.
type PreState struct {
	Accounts map[string]PreAccount `json:"accounts"` // hex 0x41 address -> account pre-state
}

// PreAccount is one account's pre-state. Code is "" for a plain account; Storage maps a hex
// 32-byte slot to its hex 32-byte value (shorter hex is right-aligned into the word).
type PreAccount struct {
	Balance int64             `json:"balance"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

// LoadPreState reads a pre-state fixture (prestate.json).
func LoadPreState(path string) (*PreState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps PreState
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("replay: parse %s: %w", path, err)
	}
	return &ps, nil
}

// MapProvider serves a decoded PreState as the VM's historical-state fall-through.
type MapProvider struct {
	balance map[string]int64
	code    map[string][]byte
	storage map[string]map[[32]byte][32]byte
}

// word32 right-aligns hex bytes into a 32-byte word (keeping the low 32 bytes if longer).
func word32(h string) ([32]byte, error) {
	var w [32]byte
	b, err := hex.DecodeString(h)
	if err != nil {
		return w, err
	}
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(w[32-len(b):], b)
	return w, nil
}

// NewMapProvider decodes a PreState into a provider, validating its hex once up front.
func NewMapProvider(ps *PreState) (*MapProvider, error) {
	p := &MapProvider{
		balance: map[string]int64{},
		code:    map[string][]byte{},
		storage: map[string]map[[32]byte][32]byte{},
	}
	for addrHex, acc := range ps.Accounts {
		addr, err := hex.DecodeString(addrHex)
		if err != nil {
			return nil, fmt.Errorf("replay: prestate addr %q: %w", addrHex, err)
		}
		k := string(addr)
		p.balance[k] = acc.Balance
		if acc.Code != "" {
			c, err := hex.DecodeString(acc.Code)
			if err != nil {
				return nil, fmt.Errorf("replay: prestate %s code: %w", addrHex, err)
			}
			p.code[k] = c
		}
		if len(acc.Storage) > 0 {
			slots := make(map[[32]byte][32]byte, len(acc.Storage))
			for slotHex, valHex := range acc.Storage {
				slot, err := word32(slotHex)
				if err != nil {
					return nil, fmt.Errorf("replay: prestate %s slot %q: %w", addrHex, slotHex, err)
				}
				val, err := word32(valHex)
				if err != nil {
					return nil, fmt.Errorf("replay: prestate %s value %q: %w", addrHex, valHex, err)
				}
				slots[slot] = val
			}
			p.storage[k] = slots
		}
	}
	return p, nil
}

// Balance returns the pre-state balance and whether the account is known.
func (p *MapProvider) Balance(addr []byte) (int64, bool) {
	v, ok := p.balance[string(addr)]
	return v, ok
}

// Code returns the pre-state runtime code and whether it is known.
func (p *MapProvider) Code(addr []byte) ([]byte, bool) {
	c, ok := p.code[string(addr)]
	return c, ok
}

// Storage returns the pre-state value at (addr, slot) and whether the slot is known.
func (p *MapProvider) Storage(addr []byte, slot [32]byte) ([32]byte, bool) {
	slots, ok := p.storage[string(addr)]
	if !ok {
		return [32]byte{}, false
	}
	v, ok := slots[slot]
	return v, ok
}
