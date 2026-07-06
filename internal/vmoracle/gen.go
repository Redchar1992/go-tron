package vmoracle

import (
	"encoding/hex"
	"hash/fnv"
	"math/rand"
)

// The fuzz-case generator (M3.5e plan §3). It turns a fuzzer seed into a fully-specified
// (World, Tx) deterministically, so the same seed always reproduces the same case — a
// prerequisite for shrinking and for a stable corpus. Until the java-tron oracle exists the
// generated cases drive go-vs-go invariants (determinism, no-panic); once it does, the SAME
// GenCase feeds both VMs (§3.2 world-synthesis).

// genAddr builds a 21-byte 0x41 TRON address ending in b, as lowercase hex. The 0xC0 second
// byte keeps generated addresses clear of the low precompile addresses (0x01..0x0a).
func genAddr(b byte) string {
	a := make([]byte, 21)
	a[0] = 0x41
	a[1] = 0xC0
	a[20] = b
	return hex.EncodeToString(a)
}

// genOps is the curated opcode alphabet the generator draws from: consensus-sensitive,
// mostly-ungated ops that exercise state/energy/logs/call-frames. PUSH immediates are handled
// separately; unlisted bytes never appear (so a program is meaningful, not noise).
var genOps = []byte{
	0x01, 0x02, 0x03, 0x04, 0x06, 0x0a, // ADD MUL SUB DIV MOD EXP
	0x10, 0x14, 0x15, 0x16, 0x17, 0x19, // LT EQ ISZERO AND OR NOT
	0x20,             // KECCAK256
	0x50,             // POP
	0x51, 0x52, 0x53, // MLOAD MSTORE MSTORE8
	0x54, 0x55, // SLOAD SSTORE
	0x58, 0x5a, 0x5b, // PC GAS JUMPDEST
	0x80, 0x81, 0x90, 0x91, // DUP1 DUP2 SWAP1 SWAP2
	0xa0, 0xa1, 0xa2, // LOG0 LOG1 LOG2
	0xf1, 0xfa, // CALL STATICCALL
}

// genProgram builds a structured pseudo-random bytecode from rng: a run of opcodes drawn from
// genOps, interleaved with PUSH1/PUSH32 immediates, ended by STOP/RETURN/REVERT. Stack
// underflows just fault (deterministically), which is a valid thing to fuzz.
func genProgram(rng *rand.Rand) []byte {
	n := 8 + rng.Intn(48)
	code := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		switch {
		case rng.Intn(3) == 0:
			code = append(code, 0x60, byte(rng.Intn(256))) // PUSH1 <b>
		case rng.Intn(16) == 0:
			code = append(code, 0x7f) // PUSH32
			for j := 0; j < 32; j++ {
				code = append(code, byte(rng.Intn(256)))
			}
		default:
			code = append(code, genOps[rng.Intn(len(genOps))])
		}
	}
	switch rng.Intn(3) {
	case 0:
		code = append(code, 0x00) // STOP
	case 1:
		code = append(code, 0x60, 0x20, 0x60, 0x00, 0xf3) // RETURN mem[0:32]
	default:
		code = append(code, 0x60, 0x00, 0x60, 0x00, 0xfd) // REVERT
	}
	return code
}

// GenCase turns a fuzzer seed into a deterministic (World, Tx): a generated contract driven by
// a Trigger, with a randomized-but-reproducible energy/staking config and fork version.
func GenCase(seed []byte) (World, Tx) {
	h := fnv.New64a()
	h.Write(seed)
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	code := genProgram(rng)
	owner, contract := genAddr(0x01), genAddr(0x02)

	// Half the cases have staked energy (weight > 0) so the receipt split is exercised;
	// half burn TRX (weight 0). Fork version drawn from a few representative gates.
	var stake, weight int64
	if rng.Intn(2) == 0 {
		stake = int64(rng.Intn(10)) * 1_000_000
		weight = 1 + int64(rng.Intn(100))
	}
	version := []int32{8, 19, 23}[rng.Intn(3)]
	fee := []int64{100, 140, 280, 420}[rng.Intn(4)]

	w := World{
		Version: version,
		DynamicProps: DynamicProps{
			TotalEnergyWeight:       weight,
			TotalEnergyCurrentLimit: 1_000_000_000,
			EnergyFee:               fee,
		},
		Block: Block{Number: 5_000_000, Timestamp: 1_600_000_000_000, Witness: genAddr(0x09)},
		Accounts: map[string]Account{
			owner:    {Balance: 1_000_000_000, EnergyStake: stake},
			contract: {Code: hex.EncodeToString(code)},
		},
	}
	tx := Tx{Type: "TriggerSmartContract", Owner: owner, Contract: contract, FeeLimit: 1_000_000_000}
	return w, tx
}
