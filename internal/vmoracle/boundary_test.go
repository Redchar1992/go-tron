package vmoracle

import (
	"encoding/hex"
	"testing"
)

// TestM35dBoundaryInvariants runs hand-authored contracts that hit the M3.5d seams — a
// gated staking opcode, a bn128 precompile, and the multisig precompile — through the
// determinism/no-panic invariants. These double as the seed corpus for the eventual
// cross-VM diff (they exercise exactly the code java-tron and go-tron must agree on).
func TestM35dBoundaryInvariants(t *testing.T) {
	// CALL(to, in=(inOff,inSize), out=(outOff,outSize)) with value 0 and a large gas cap.
	call := func(to byte, inSize, outSize byte) []byte {
		return []byte{
			0x60, outSize, 0x60, 0x00, // outSize, outOff
			0x60, inSize, 0x60, 0x00, // inSize, inOff
			0x60, 0x00, // value
			0x60, to, // to (low byte -> 0x41..to precompile)
			0x62, 0xff, 0xff, 0xff, // gas
			0xf1, 0x00, // CALL; STOP
		}
	}

	cases := []struct {
		name       string
		code       []byte
		wantResult string // "" = only check invariants
	}{
		{"gated-freeze-opcode", []byte{0xd5}, "ILLEGAL_OPERATION"}, // FREEZE gated off at v23
		{"bn128-add-0x06", call(0x06, 0x80, 0x40), "SUCCESS"},      // zero input -> identity
		{"validatemultisign-0x0a", call(0x0a, 0x00, 0x20), "SUCCESS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, tx := baseWorld(hex.EncodeToString(c.code), 0, 0, 0) // single contract at v23
			checkInvariants(t, w, tx)
			if c.wantResult != "" {
				got, err := Execute(w, tx)
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if got.Result != c.wantResult {
					t.Fatalf("result = %s (%s), want %s", got.Result, got.VMError, c.wantResult)
				}
			}
		})
	}
}
