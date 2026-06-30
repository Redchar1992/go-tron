package crypto

import "testing"

func TestKeccak256KnownVectors(t *testing.T) {
	// Legacy Keccak-256 (not NIST SHA3-256) reference vectors.
	cases := []struct{ in, want string }{
		{"", "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"abc", "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
	}
	for _, c := range cases {
		got := toHex(Keccak256([]byte(c.in)))
		if got != c.want {
			t.Errorf("Keccak256(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestKeccak256ConcatEqualsJoined(t *testing.T) {
	a := Keccak256([]byte("foo"), []byte("bar"))
	b := Keccak256([]byte("foobar"))
	if toHex(a) != toHex(b) {
		t.Errorf("variadic concat mismatch: %s vs %s", toHex(a), toHex(b))
	}
}

func TestSha256Vector(t *testing.T) {
	// sha256("abc")
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := toHex(Sha256([]byte("abc"))); got != want {
		t.Errorf("Sha256(abc) = %s, want %s", got, want)
	}
}

func toHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
