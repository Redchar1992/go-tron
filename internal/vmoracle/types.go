// Package vmoracle is the go-tron half of the M3.5e differential VM oracle
// (docs/m3.5e-fuzzer-and-signoff-plan.md). It defines the normalized cross-VM execution
// schema that BOTH go-tron and the java-tron `jtron-oracle` harness emit, a local executor
// that runs a fully-specified World+Tx through internal/tvm, and a Diff that classifies the
// two outcomes. Fixing this schema now pins the JSON wire protocol the Java side must match,
// and lets the fuzzer (and go-vs-go tests) run before the Java oracle exists.
//
// Addresses, code, slots, values, and payloads are lowercase hex (no 0x); TRON addresses are
// 21-byte 0x41-prefixed, i.e. "41…". Hex at the boundary keeps the schema JSON-clean and
// makes Diff a string comparison.
package vmoracle

// World is the fully-specified pre-state + config one execution runs against — the go-tron
// counterpart of a jtron-oracle request. Both VMs execute an identical World, so the fuzzer
// authors the world (incl. staked-energy globals) and sidesteps the historical-state oracle.
type World struct {
	// Version is the block header version; the executor resolves the TVM fork gates from it
	// (tvm.VMConfigForVersion). Proposal-gated flags default off (see M3.5d).
	Version      int32              `json:"version"`
	DynamicProps DynamicProps       `json:"dynamicProps"`
	Block        Block              `json:"block"`
	Accounts     map[string]Account `json:"accounts"` // hex 41-address -> account
}

// DynamicProps carries the network-global resource params the energy path reads (java-tron
// DynamicPropertiesStore). The fuzzer sets these directly so the staked-energy derivation is
// exercised with a known, identical world on both sides.
type DynamicProps struct {
	TotalEnergyWeight       int64 `json:"totalEnergyWeight"`
	TotalEnergyCurrentLimit int64 `json:"totalEnergyCurrentLimit"`
	SupportUnfreezeDelay    bool  `json:"supportUnfreezeDelay"`
	AllowNewReward          bool  `json:"allowNewReward"`
	EnergyFee               int64 `json:"energyFee"` // sun per energy (0 -> 100-sun floor)
}

// Block is the block context the TVM reads.
type Block struct {
	Number    int64  `json:"number"`
	Timestamp int64  `json:"timestamp"`
	Witness   string `json:"witness"` // hex 41-address (COINBASE)
}

// Account is one account's pre-state.
type Account struct {
	Balance     int64             `json:"balance"`     // sun
	Code        string            `json:"code"`        // hex runtime code ("" for EOAs)
	Storage     map[string]string `json:"storage"`     // hex 32-byte slot -> hex 32-byte value
	EnergyStake int64             `json:"energyStake"` // frozen-for-energy (sun) -> staked energy
}

// Tx is the contract call/creation to execute.
type Tx struct {
	Type      string `json:"type"`     // "TriggerSmartContract" | "CreateSmartContract"
	Owner     string `json:"owner"`    // hex 41-address
	Contract  string `json:"contract"` // hex 41-address (Trigger: callee)
	Data      string `json:"data"`     // hex calldata (Trigger)
	Bytecode  string `json:"bytecode"` // hex init/runtime code (Create)
	CallValue int64  `json:"callValue"`
	FeeLimit  int64  `json:"feeLimit"`
	TxID      string `json:"txID"` // hex 32-byte root tx id (Create address derivation)
}

// Execution is the normalized cross-VM outcome — the schema go-tron and jtron-oracle both
// return.
type Execution struct {
	// Result is a coarse normalized outcome: SUCCESS | REVERT | OUT_OF_ENERGY |
	// ILLEGAL_OPERATION | BAD_JUMP_DESTINATION | STACK_TOO_SMALL | STACK_TOO_LARGE |
	// STATE_CHANGE_IN_STATIC | FAULT. The exact code set will be reconciled to java-tron's
	// program-result enum when the Java oracle is wired; VMError carries the raw go-tron error.
	Result            string                       `json:"result"`
	VMError           string                       `json:"vmError,omitempty"`
	Return            string                       `json:"return"` // hex
	EnergyUsed        int64                        `json:"energyUsed"`
	EnergyFee         int64                        `json:"energyFee"`
	OriginEnergyUsage int64                        `json:"originEnergyUsage"`
	StorageWrites     map[string]map[string]string `json:"storageWrites"` // 41-addr -> slot -> value (net delta)
	Logs              []LogEntry                   `json:"logs"`
	CreatedAddress    string                       `json:"createdAddress,omitempty"` // hex 41-address (Create success)
}

// LogEntry is one emitted event.
type LogEntry struct {
	Address string   `json:"address"` // hex 41-address
	Topics  []string `json:"topics"`  // hex 32-byte topics
	Data    string   `json:"data"`    // hex
}

// Divergence is one field on which two Executions disagree.
type Divergence struct {
	Field string `json:"field"`
	A     string `json:"a"`
	B     string `json:"b"`
	Kind  string `json:"kind"` // "mismatch" (P0) — "out-of-scope" is used by higher layers
}
