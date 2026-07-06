package tvm

import "encoding/binary"

// blake2F is the 0x20009 precompile: the BLAKE2b `F` compression function per EIP-152.
// It is availability-gated on allowTvmCompatibleEvm (VMConfig.Forward6364) — see
// lookupPrecompile — matching java-tron's getContractForAddr guard.
//
// Input is exactly 213 bytes: rounds(4, big-endian) ‖ h(64) ‖ m(128) ‖ t(16) ‖ f(1).
// h/m/t are little-endian 64-bit words. f (final flag) must be 0 or 1. Output is the
// 64-byte updated state h. Energy = rounds. CONSENSUS-CRITICAL; matches java-tron Blake2F.
type blake2F struct{}

const blake2FInputLen = 213

// RequiredEnergy returns `rounds` when the input is well-formed, else 0 — byte-faithful to
// PrecompiledContracts.Blake2F.getEnergyForData (len==213 && (data[212]&0xFE)==0).
func (blake2F) RequiredEnergy(in []byte) uint64 {
	if len(in) != blake2FInputLen || in[212]&0xFE != 0 {
		return 0
	}
	return uint64(binary.BigEndian.Uint32(in[0:4]))
}

func (blake2F) Run(in []byte) ([]byte, error) {
	// A malformed input is a hard failure (fails the CALL), matching java-tron returning
	// (false, …): wrong length, or a final flag that is neither 0 nor 1.
	if len(in) != blake2FInputLen || (in[212] != 0 && in[212] != 1) {
		return nil, errPrecompileFailure
	}
	rounds := binary.BigEndian.Uint32(in[0:4])

	var h [8]uint64
	for i := 0; i < 8; i++ {
		h[i] = binary.LittleEndian.Uint64(in[4+i*8:])
	}
	var m [16]uint64
	for i := 0; i < 16; i++ {
		m[i] = binary.LittleEndian.Uint64(in[68+i*8:])
	}
	t0 := binary.LittleEndian.Uint64(in[196:])
	t1 := binary.LittleEndian.Uint64(in[204:])
	final := in[212] == 1

	blake2bF(&h, m, t0, t1, final, rounds)

	out := make([]byte, 64)
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], h[i])
	}
	return out, nil
}

// blake2bIV is the BLAKE2b initialization vector (fractional parts of sqrt of the first 8
// primes) — the same constants used to seed the working vector v[8..15].
var blake2bIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

// blake2bSigma is the message word permutation schedule. EIP-152 allows an arbitrary round
// count, so round r selects blake2bSigma[r % 10] (the schedule has period 10).
var blake2bSigma = [10][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
}

func rotr64(x uint64, n uint) uint64 { return (x >> n) | (x << (64 - n)) }

// blake2bF runs `rounds` rounds of the BLAKE2b compression on state h with message block m,
// offset counters t0/t1 and final flag, writing the mixed state back into h (EIP-152 F).
func blake2bF(h *[8]uint64, m [16]uint64, t0, t1 uint64, final bool, rounds uint32) {
	var v [16]uint64
	copy(v[0:8], h[:])
	copy(v[8:16], blake2bIV[:])
	v[12] ^= t0
	v[13] ^= t1
	if final {
		v[14] = ^v[14]
	}

	g := func(a, b, c, d int, x, y uint64) {
		v[a] = v[a] + v[b] + x
		v[d] = rotr64(v[d]^v[a], 32)
		v[c] = v[c] + v[d]
		v[b] = rotr64(v[b]^v[c], 24)
		v[a] = v[a] + v[b] + y
		v[d] = rotr64(v[d]^v[a], 16)
		v[c] = v[c] + v[d]
		v[b] = rotr64(v[b]^v[c], 63)
	}

	for r := uint32(0); r < rounds; r++ {
		s := blake2bSigma[r%10]
		g(0, 4, 8, 12, m[s[0]], m[s[1]])
		g(1, 5, 9, 13, m[s[2]], m[s[3]])
		g(2, 6, 10, 14, m[s[4]], m[s[5]])
		g(3, 7, 11, 15, m[s[6]], m[s[7]])
		g(0, 5, 10, 15, m[s[8]], m[s[9]])
		g(1, 6, 11, 12, m[s[10]], m[s[11]])
		g(2, 7, 8, 13, m[s[12]], m[s[13]])
		g(3, 4, 9, 14, m[s[14]], m[s[15]])
	}

	for i := 0; i < 8; i++ {
		h[i] ^= v[i] ^ v[i+8]
	}
}
