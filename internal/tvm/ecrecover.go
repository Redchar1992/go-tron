package tvm

import (
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/Redchar1992/go-tron/internal/crypto"
)

// ecrecoverRun implements the 0x01 ecrecover precompile: recover the signer's public key
// from (hash, v, r, s) and return its 20-byte Ethereum-style address, left-padded into a
// 32-byte word. Faithful to java-tron PrecompiledContracts.ECRecover:
//
//   - input is hash(32) || v(32) || r(32) || s(32), right-padded with zeros;
//   - v must be 27 or 28 and bytes [32:63] must be zero;
//   - on any validation/recovery failure it returns an EMPTY result (the call still
//     "succeeds" — the EVM ecrecover contract reports failure as empty output, not a
//     revert);
//   - TRON deviation from its own address format: the OUTPUT is the Ethereum 20-byte
//     address (keccak(pubXY)[12:32]), NOT a 0x41-prefixed TRON address.
func ecrecoverRun(input []byte) ([]byte, error) {
	in := rightPad(input, 0, 128)
	hash := in[0:32]
	// bytes [32:63] of v must be zero; v itself is in[63] and must be 27 or 28.
	for _, b := range in[32:63] {
		if b != 0 {
			return nil, nil
		}
	}
	v := in[63]
	if v != 27 && v != 28 {
		return nil, nil
	}

	// decred RecoverCompact expects a 65-byte [recoveryCode || R || S] signature, where
	// recoveryCode == 27 + recoveryID for an uncompressed key — exactly Ethereum's v.
	compact := make([]byte, 65)
	compact[0] = v
	copy(compact[1:33], in[64:96])   // r
	copy(compact[33:65], in[96:128]) // s

	pub, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil || pub == nil {
		return nil, nil
	}
	// Uncompressed serialization is 0x04 || X(32) || Y(32); hash the 64-byte X||Y.
	uncompressed := pub.SerializeUncompressed()
	digest := crypto.Keccak256(uncompressed[1:])
	out := make([]byte, 32)
	copy(out[12:], digest[12:32])
	return out, nil
}
