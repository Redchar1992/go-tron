package vmoracle

import "testing"

var validResults = map[string]bool{
	"SUCCESS": true, "REVERT": true, "OUT_OF_ENERGY": true, "ILLEGAL_OPERATION": true,
	"BAD_JUMP_DESTINATION": true, "STACK_TOO_SMALL": true, "STACK_TOO_LARGE": true,
	"STATE_CHANGE_IN_STATIC": true, "FAULT": true,
}

// checkInvariants asserts the properties that must hold for EVERY input, oracle or not:
//   - no panic (the test framework catches it),
//   - determinism — a consensus node that isn't deterministic is a chain split; two runs of
//     the same World must produce a byte-identical Execution,
//   - a well-formed, known result kind and non-negative energy.
func checkInvariants(t testing.TB, w World, tx Tx) {
	t.Helper()
	e1, err1 := Execute(w, tx)
	e2, err2 := Execute(w, tx)
	if (err1 == nil) != (err2 == nil) {
		t.Fatalf("nondeterministic error: %v vs %v", err1, err2)
	}
	if err1 != nil {
		return // malformed World (decode error) — not an execution to compare
	}
	if d := Diff(e1, e2); len(d) != 0 {
		t.Fatalf("Execute is nondeterministic: %+v", d)
	}
	if !validResults[e1.Result] {
		t.Fatalf("unknown result kind %q (%s)", e1.Result, e1.VMError)
	}
	if e1.EnergyUsed < 0 || e1.EnergyFee < 0 || e1.OriginEnergyUsage < 0 {
		t.Fatalf("negative energy: %+v", e1)
	}
}

// FuzzExecuteDeterminism drives coverage-guided fuzzing over the Execute loop, asserting the
// invariants. Run with `go test -fuzz=FuzzExecuteDeterminism ./internal/vmoracle`. Without
// -fuzz it still exercises the seed corpus.
func FuzzExecuteDeterminism(f *testing.F) {
	f.Add([]byte("seed-0"))
	f.Add([]byte{0x60, 0x2a, 0x60, 0x00, 0x55}) // SSTORE-ish
	f.Add([]byte{0xa1, 0xf1, 0xfa, 0x55, 0x54}) // LOG/CALL/STATICCALL/SSTORE/SLOAD
	f.Add([]byte{0x00})
	f.Fuzz(func(t *testing.T, seed []byte) {
		w, tx := GenCase(seed)
		checkInvariants(t, w, tx)
	})
}

// TestGenDeterministic: the same seed reproduces the same case (required for shrinking + a
// stable corpus).
func TestGenDeterministic(t *testing.T) {
	seed := []byte("reproduce-me")
	w1, tx1 := GenCase(seed)
	w2, tx2 := GenCase(seed)
	if w1.Version != w2.Version ||
		w1.Accounts[tx1.Contract].Code != w2.Accounts[tx2.Contract].Code ||
		w1.DynamicProps != w2.DynamicProps {
		t.Fatal("GenCase is not deterministic in its seed")
	}
}

// TestExecuteInvariantsCorpus runs a broad range of generated cases through the invariants so
// plain `go test` (no -fuzz) exercises the generator + Execute determinism in CI.
func TestExecuteInvariantsCorpus(t *testing.T) {
	for i := 0; i < 2000; i++ {
		seed := []byte{byte(i), byte(i >> 8), byte(i >> 16)}
		w, tx := GenCase(seed)
		checkInvariants(t, w, tx)
	}
}
