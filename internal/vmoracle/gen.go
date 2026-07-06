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

// genBody builds a structured pseudo-random opcode run (no terminator) from rng: opcodes
// drawn from genOps, interleaved with PUSH1/PUSH32 immediates. Stack underflows just fault
// (deterministically), which is a valid thing to fuzz.
func genBody(rng *rand.Rand) []byte {
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
	return code
}

// terminal picks a random halting sequence.
func terminal(rng *rand.Rand) []byte {
	switch rng.Intn(3) {
	case 0:
		return []byte{0x00} // STOP
	case 1:
		return []byte{0x60, 0x20, 0x60, 0x00, 0xf3} // RETURN mem[0:32]
	default:
		return []byte{0x60, 0x00, 0x60, 0x00, 0xfd} // REVERT
	}
}

// genProgram is a body + terminal — a self-contained contract.
func genProgram(rng *rand.Rand) []byte {
	return append(genBody(rng), terminal(rng)...)
}

// callSeq emits bytecode that CALLs the 21-byte 0x41 address to21 with empty in/out and a
// large gas cap, then POPs the success flag. Exercises call frames, cross-frame log
// journaling, and nested-revert rollback.
func callSeq(to21 []byte) []byte {
	seq := []byte{
		0x60, 0x00, // outSize
		0x60, 0x00, // outOff
		0x60, 0x00, // inSize
		0x60, 0x00, // inOff
		0x60, 0x00, // value
	}
	seq = append(seq, 0x73)                   // PUSH20
	seq = append(seq, to21[1:21]...)          // callee 20-byte body
	seq = append(seq, 0x62, 0xff, 0xff, 0xff) // PUSH3 gas
	seq = append(seq, 0xf1, 0x50)             // CALL; POP
	return seq
}

// GenCase turns a fuzzer seed into a deterministic (World, Tx): a generated contract driven by
// a Trigger, with a randomized-but-reproducible energy/staking config and fork version.
func GenCase(seed []byte) (World, Tx) {
	h := fnv.New64a()
	h.Write(seed)
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	owner, aHex, bHex := genAddr(0x01), genAddr(0x02), genAddr(0x03)
	bAddr, _ := hex.DecodeString(bHex)

	// Contract A CALLs a co-generated callee B, then runs its own body — exercising call
	// frames, cross-frame log journaling, and nested-revert rollback. B is self-contained.
	codeA := append(callSeq(bAddr), genProgram(rng)...)
	codeB := genProgram(rng)

	// Half the cases have staked energy (weight > 0) so the receipt split is exercised;
	// half burn TRX (weight 0). Fork version drawn from a few representative gates.
	var stake, weight int64
	if rng.Intn(2) == 0 {
		stake = int64(rng.Intn(10)) * 1_000_000
		weight = 1 + int64(rng.Intn(100))
	}
	version := []int32{8, 19, 23}[rng.Intn(3)]
	fee := []int64{100, 140, 280, 420}[rng.Intn(4)]

	accB := Account{Code: hex.EncodeToString(codeB)}
	if rng.Intn(2) == 0 { // sometimes pre-seed a storage slot so SLOAD reads non-zero
		accB.Storage = map[string]string{
			hex.EncodeToString([]byte{byte(rng.Intn(4))}): hex.EncodeToString([]byte{byte(1 + rng.Intn(255))}),
		}
	}

	w := World{
		Version: version,
		DynamicProps: DynamicProps{
			TotalEnergyWeight:       weight,
			TotalEnergyCurrentLimit: 1_000_000_000,
			EnergyFee:               fee,
		},
		Block: Block{Number: 5_000_000, Timestamp: 1_600_000_000_000, Witness: genAddr(0x09)},
		Accounts: map[string]Account{
			owner: {Balance: 1_000_000_000, EnergyStake: stake},
			aHex:  {Code: hex.EncodeToString(codeA)},
			bHex:  accB,
		},
	}
	tx := Tx{Type: "TriggerSmartContract", Owner: owner, Contract: aHex, FeeLimit: 1_000_000_000}
	return w, tx
}
