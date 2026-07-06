package vmoracle

import "testing"

func sampleExec() Execution {
	return Execution{
		Result:        "SUCCESS",
		Return:        "2a",
		EnergyUsed:    100,
		EnergyFee:     10000,
		StorageWrites: map[string]map[string]string{"41c2": {"00": "2a"}},
		Logs:          []LogEntry{{Address: "41c2", Topics: []string{"cc"}, Data: "2a"}},
	}
}

func fieldsOf(d []Divergence) map[string]bool {
	m := map[string]bool{}
	for _, x := range d {
		m[x.Field] = true
	}
	return m
}

func TestDiffIdentical(t *testing.T) {
	if d := Diff(sampleExec(), sampleExec()); len(d) != 0 {
		t.Fatalf("identical executions diverge: %+v", d)
	}
}

func TestDiffScalarMismatches(t *testing.T) {
	a := sampleExec()
	b := sampleExec()
	b.Result = "REVERT"
	b.Return = "00"
	b.EnergyUsed = 200
	b.EnergyFee = 20000
	got := fieldsOf(Diff(a, b))
	for _, f := range []string{"result", "return", "energyUsed", "energyFee"} {
		if !got[f] {
			t.Fatalf("expected divergence on %q, got %v", f, got)
		}
	}
}

func TestDiffStorageAndLogs(t *testing.T) {
	a := sampleExec()

	// Different storage value.
	b := sampleExec()
	b.StorageWrites = map[string]map[string]string{"41c2": {"00": "2b"}}
	if !fieldsOf(Diff(a, b))["storage[41c2][00]"] {
		t.Fatal("expected storage-slot divergence")
	}

	// Extra log on one side.
	c := sampleExec()
	c.Logs = append(c.Logs, LogEntry{Address: "41c2"})
	if !fieldsOf(Diff(a, c))["logs.length"] {
		t.Fatal("expected logs.length divergence")
	}

	// Same length, different topic.
	e := sampleExec()
	e.Logs = []LogEntry{{Address: "41c2", Topics: []string{"dd"}, Data: "2a"}}
	if !fieldsOf(Diff(a, e))["logs[0].topics[0]"] {
		t.Fatal("expected log-topic divergence")
	}
}
