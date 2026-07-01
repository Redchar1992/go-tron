package replay

import (
	"encoding/json"
	"fmt"
	"os"
)

// Receipt is the on-chain transaction receipt captured from gettransactioninfobyid — the
// oracle values the differential harness diffs go-tron's execution against. The bandwidth
// fields (fee / net_usage / net_fee) drive the M2.5 bandwidth oracle; the energy fields
// drive the M3.5 energy-receipt oracle for contract (Create/Trigger) transactions.
type Receipt struct {
	Fee               int64 `json:"fee"`
	NetUsage          int64 `json:"netUsage"`
	NetFee            int64 `json:"netFee"`
	EnergyUsage       int64 `json:"energyUsage"`
	EnergyFee         int64 `json:"energyFee"`
	OriginEnergyUsage int64 `json:"originEnergyUsage"`
	EnergyUsageTotal  int64 `json:"energyUsageTotal"`
}

// LoadReceipts reads a txID -> Receipt map (receipts.json).
func LoadReceipts(path string) (map[string]Receipt, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]Receipt
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("replay: parse %s: %w", path, err)
	}
	return m, nil
}
