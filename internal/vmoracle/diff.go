package vmoracle

import (
	"fmt"
	"sort"
)

// Diff compares two Executions (conventionally a = go-tron, b = java-tron oracle) and returns
// the fields on which they disagree. An empty result means byte-for-byte agreement. Every
// divergence is a "mismatch" (P0); higher layers may reclassify specific fields as
// "out-of-scope" for a deferred feature under a given config (see the M3.5e plan §3.3).
func Diff(a, b Execution) []Divergence {
	var d []Divergence
	add := func(field, av, bv string) {
		if av != bv {
			d = append(d, Divergence{Field: field, A: av, B: bv, Kind: "mismatch"})
		}
	}

	add("result", a.Result, b.Result)
	add("return", a.Return, b.Return)
	add("energyUsed", itoa(a.EnergyUsed), itoa(b.EnergyUsed))
	add("energyFee", itoa(a.EnergyFee), itoa(b.EnergyFee))
	add("originEnergyUsage", itoa(a.OriginEnergyUsage), itoa(b.OriginEnergyUsage))
	add("createdAddress", a.CreatedAddress, b.CreatedAddress)

	diffStorage(&d, a.StorageWrites, b.StorageWrites)
	diffLogs(&d, a.Logs, b.Logs)
	return d
}

// diffStorage compares the net storage deltas, emitting one divergence per differing slot
// (absent on one side reads as "").
func diffStorage(d *[]Divergence, a, b map[string]map[string]string) {
	for _, addr := range sortedUnion(keys(a), keys(b)) {
		am, bm := a[addr], b[addr]
		for _, slot := range sortedUnion(keys(am), keys(bm)) {
			av, bv := am[slot], bm[slot]
			if av != bv {
				*d = append(*d, Divergence{
					Field: fmt.Sprintf("storage[%s][%s]", addr, slot), A: av, B: bv, Kind: "mismatch",
				})
			}
		}
	}
}

// diffLogs compares the log lists positionally (order is consensus-relevant).
func diffLogs(d *[]Divergence, a, b []LogEntry) {
	if len(a) != len(b) {
		*d = append(*d, Divergence{Field: "logs.length", A: itoa(int64(len(a))), B: itoa(int64(len(b))), Kind: "mismatch"})
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Address != b[i].Address {
			*d = append(*d, Divergence{Field: fmt.Sprintf("logs[%d].address", i), A: a[i].Address, B: b[i].Address, Kind: "mismatch"})
		}
		if a[i].Data != b[i].Data {
			*d = append(*d, Divergence{Field: fmt.Sprintf("logs[%d].data", i), A: a[i].Data, B: b[i].Data, Kind: "mismatch"})
		}
		if len(a[i].Topics) != len(b[i].Topics) {
			*d = append(*d, Divergence{Field: fmt.Sprintf("logs[%d].topics.length", i), A: itoa(int64(len(a[i].Topics))), B: itoa(int64(len(b[i].Topics))), Kind: "mismatch"})
			continue
		}
		for j := range a[i].Topics {
			if a[i].Topics[j] != b[i].Topics[j] {
				*d = append(*d, Divergence{Field: fmt.Sprintf("logs[%d].topics[%d]", i, j), A: a[i].Topics[j], B: b[i].Topics[j], Kind: "mismatch"})
			}
		}
	}
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedUnion(a, b []string) []string {
	seen := map[string]struct{}{}
	for _, s := range append(append([]string{}, a...), b...) {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
