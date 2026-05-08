package fleetsec

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestGenerateX25519Keypair_LengthsAndDistinctness(t *testing.T) {
	priv1, pub1, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	if len(priv1) != X25519KeySize || len(pub1) != X25519KeySize {
		t.Errorf("len(priv)=%d len(pub)=%d, want both %d",
			len(priv1), len(pub1), X25519KeySize)
	}

	// Bit-clamping per RFC 7748: priv[0] & 0x07 must be 0; priv[31] & 0x80
	// must be 0; priv[31] & 0x40 must be set.
	if priv1[0]&0x07 != 0 {
		t.Errorf("priv[0] = %#x, low 3 bits should be cleared", priv1[0])
	}
	if priv1[31]&0x80 != 0 {
		t.Errorf("priv[31] = %#x, high bit should be cleared", priv1[31])
	}
	if priv1[31]&0x40 == 0 {
		t.Errorf("priv[31] = %#x, second-highest bit should be set", priv1[31])
	}

	priv2, pub2, _ := GenerateX25519Keypair()
	if bytes.Equal(priv1, priv2) || bytes.Equal(pub1, pub2) {
		t.Error("two GenerateX25519Keypair calls produced identical keys")
	}
}

func TestDerivePubkey_RoundTrip(t *testing.T) {
	priv, pubExpected, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pubGot, err := DerivePubkey(priv)
	if err != nil {
		t.Fatalf("DerivePubkey: %v", err)
	}
	if !bytes.Equal(pubGot, pubExpected) {
		t.Error("DerivePubkey doesn't round-trip with GenerateX25519Keypair")
	}
}

func TestValidate_AllZeroRejected(t *testing.T) {
	zero := make([]byte, X25519KeySize)
	if err := ValidateX25519PublicKey(zero); err == nil {
		t.Error("all-zero pubkey accepted")
	}
	if err := ValidateX25519PrivateKey(zero); err == nil {
		t.Error("all-zero privkey accepted")
	}
}

func TestValidate_WrongLengthRejected(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		if err := ValidateX25519PublicKey(make([]byte, n)); err == nil {
			t.Errorf("len=%d accepted", n)
		}
	}
}

func TestFingerprint_StableAndCorrect(t *testing.T) {
	key := []byte{0xab, 0xcd, 0xef, 0x12}

	// Compute expected manually: first 8 bytes of SHA-256, hex w/ colons.
	h := sha256.Sum256(key)
	want := ""
	for i := 0; i < FingerprintLen; i++ {
		if i > 0 {
			want += ":"
		}
		want += hexByte(h[i])
	}

	got := Fingerprint(key)
	if got != want {
		t.Errorf("Fingerprint = %q, want %q", got, want)
	}

	// Stable across calls.
	if Fingerprint(key) != got {
		t.Error("Fingerprint not stable across calls")
	}

	// Different key, different fingerprint.
	if Fingerprint([]byte{0xab, 0xcd, 0xef, 0x13}) == got {
		t.Error("different keys produced same fingerprint")
	}
}

func hexByte(b byte) string {
	const h = "0123456789abcdef"
	return string([]byte{h[b>>4], h[b&0xf]})
}

func TestFingerprintFormat_DisplayShape(t *testing.T) {
	key, _, _ := GenerateX25519Keypair()
	fp := Fingerprint(key)
	parts := strings.Split(fp, ":")
	if len(parts) != FingerprintLen {
		t.Errorf("fingerprint has %d parts, want %d", len(parts), FingerprintLen)
	}
	for i, p := range parts {
		if len(p) != 2 {
			t.Errorf("part %d = %q, want 2 hex chars", i, p)
		}
	}
}

func TestPubkeyB64_RoundTrip(t *testing.T) {
	_, pub, _ := GenerateX25519Keypair()
	enc := EncodePubkeyB64(pub)
	dec, err := DecodePubkeyB64(enc)
	if err != nil {
		t.Fatalf("DecodePubkeyB64: %v", err)
	}
	if !bytes.Equal(dec, pub) {
		t.Error("base64 round-trip mismatch")
	}

	// Whitespace should be tolerated.
	if _, err := DecodePubkeyB64("  " + enc + "\n"); err != nil {
		t.Errorf("DecodePubkeyB64 with whitespace: %v", err)
	}
}

func TestRandomPSK_AcceptedSizesOnly(t *testing.T) {
	for _, n := range []int{0, 16, 32} {
		got, err := RandomPSK(n)
		if err != nil {
			t.Errorf("RandomPSK(%d): %v", n, err)
			continue
		}
		if len(got) != n {
			t.Errorf("RandomPSK(%d) returned %d bytes", n, len(got))
		}
	}
	for _, n := range []int{1, 8, 17, 31, 33, 64} {
		if _, err := RandomPSK(n); err == nil {
			t.Errorf("RandomPSK(%d) accepted", n)
		}
	}
}

func TestRandomPSK_Distinct(t *testing.T) {
	a, _ := RandomPSK(16)
	b, _ := RandomPSK(16)
	if bytes.Equal(a, b) {
		t.Error("two RandomPSK(16) calls returned identical bytes")
	}
}

func TestSecretBytes_ClearZeros(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5}
	s := NewSecret(b)
	s.Clear()
	for i, v := range b {
		if v != 0 {
			t.Errorf("byte[%d] = %d, want 0 after Clear", i, v)
		}
	}
	if s.Bytes() != nil {
		t.Error("Bytes() should be nil after Clear")
	}
}
