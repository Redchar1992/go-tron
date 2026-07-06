package tvm

import "errors"

// TRON shielded-TRC-20 precompiles (TIP-135): verifymintproof (0x1000001),
// verifytransferproof (0x1000002), verifyburnproof (0x1000003), and merklehash (0x1000004).
//
// Availability-gated on allowShieldedTRC20Transaction (chain-parameter #39), which is
// DISABLED by default (CommonParameter default 0) and off on TRON mainnet and on any
// from-genesis chain. With the gate off — go-tron's replay target — java-tron's
// getContractForAddr returns null for these addresses, so a CALL to them is an ordinary
// account call, NOT a precompile. lookupPrecompile reflects exactly that: with the gate off
// (the default) it returns nil for 0x1000001..4, which is bit-for-bit faithful. This is the
// reachable behavior and it is what TestShieldedGating locks in.
//
// DEFERRED (external zk crypto): java-tron's actual proof verification is JLibrustzcash — the
// JNI binding to Rust librustzcash (zcash Sapling: Groth16 over BLS12-381, JubJub Pedersen
// hashes, RedJubjub binding signatures, nullifier derivation). A byte-identical Go
// implementation is a large standalone milestone (a Sapling verifier port), out of scope for
// M3.5d and unreachable while the gate is off. Until it lands, if a chain config explicitly
// turns the gate ON, these precompiles fail CLOSED (Run returns an error → the CALL fails and
// the forwarded energy is consumed) rather than silently accept or reject an unverified
// proof. The fixed java-tron energy costs are recorded so the deferred path charges the same
// price and so the eventual real implementation slots in without a cost change.
var errShieldedUnsupported = errors.New(
	"tvm: shielded TRC-20 proof verification not implemented (needs a Sapling/Groth16 zk verifier)")

// Fixed per-call energy of each shielded precompile (java-tron getEnergyForData constants).
const (
	energyVerifyMintProof     = 150_000 // 0x1000001
	energyVerifyTransferProof = 200_000 // 0x1000002
	energyVerifyBurnProof     = 150_000 // 0x1000003
	energyMerkleHash          = 500     // 0x1000004
)

// shieldedDeferred is a placeholder for a shielded-TRC-20 precompile whose zk verification is
// not yet implemented. It carries java-tron's fixed energy cost and fails closed. It is only
// reachable when AllowShieldedTRC20Transaction is explicitly enabled (never on the default /
// mainnet / from-genesis path).
type shieldedDeferred struct{ energy uint64 }

func (s shieldedDeferred) RequiredEnergy([]byte) uint64 { return s.energy }
func (shieldedDeferred) Run([]byte) ([]byte, error)     { return nil, errShieldedUnsupported }
