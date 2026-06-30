// Package bandwidth models TRON's "net" (bandwidth) accounting for a transaction — the
// consensus quantity that determines whether a tx consumes free/staked bandwidth points
// or burns TRX.
//
// Verified against java-tron (consumer/BandwidthProcessor) and reconciled with on-chain
// receipts (gettransactioninfobyid) at mainnet block ~3,000,000:
//
//   - The charged size is the FULL serialized Transaction size in bytes — the same bytes
//     hashed for the Merkle leaf (block.TxMerkleHash). No constant overhead is added on
//     this path (an empirically-220-byte tx is charged exactly 220).
//   - If the owner has enough free/staked bandwidth, the receipt reports
//     net_usage == Size and net_fee == 0 (covered).
//   - Otherwise the tx burns net_fee == Size * Rate sun, with Rate = TRANSACTION_FEE =
//     10 sun/byte (DynamicProperties default), and net_usage == 0 (burned).
//
// Hence the state-independent invariant the replay harness checks against the chain:
// Size == net_usage  (covered)  OR  Size*Rate == net_fee  (burned).
//
// NOT modeled here (deferred): account-creation fee, multi-sig fee, frozen-bandwidth
// limits, the 24h free-limit window, energy. Those need per-account resource state and
// DynamicProperties, which arrive with the broader fee milestone.
package bandwidth

import (
	"google.golang.org/protobuf/proto"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

// Rate is TRON's TRANSACTION_FEE: sun burned per bandwidth byte when bandwidth is not
// covered by free/staked points (DynamicPropertiesStore default).
const Rate int64 = 10

// Size returns the full serialized size of a transaction in bytes — the bandwidth the
// network charges for it.
func Size(tx *core.Transaction) (int, error) {
	b, err := proto.Marshal(tx)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// BurnFee returns the TRX (sun) burned for a transaction of the given byte size when its
// bandwidth is not covered by free/staked points.
func BurnFee(size int) int64 {
	return int64(size) * Rate
}
