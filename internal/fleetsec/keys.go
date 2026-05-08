package fleetsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// X25519KeySize is the byte length of both X25519 private and public keys.
const X25519KeySize = 32

// FingerprintLen is the number of SHA-256(pubkey) bytes we hex-encode for
// display fingerprints. 8 bytes (64 bits) is enough collision resistance
// for a small fleet's pubkey set; the colon-separated hex form is
// "ab:cd:ef:01:02:03:04:05" -- compact enough for a UI chip, mnemonic
// enough for an operator to spot-check.
const FingerprintLen = 8

// PSK length validation: Meshtastic accepts 0 / 1 / 16 / 32 byte PSKs.
// We never *generate* length 1 (that's reserved for "default channel
// index" semantics). 0 means "no encryption". RandomPSK only produces
// 16 or 32.
var validPSKLengths = map[int]bool{0: true, 16: true, 32: true}

// GenerateX25519Keypair returns a fresh Curve25519 (X25519) keypair. The
// private key is 32 random bytes with the standard bit-clamping that the
// underlying curve25519 package expects. The public key is derived via
// scalar multiplication of the basepoint.
//
// Used by RotateIdentity (mints a new control-center keypair on demand)
// and by ImportIdentity (validation only -- the caller supplies the key
// bytes; this is just sanity-checking the format).
func GenerateX25519Keypair() (priv, pub []byte, err error) {
	priv = make([]byte, X25519KeySize)
	if _, err := rand.Read(priv); err != nil {
		return nil, nil, fmt.Errorf("read random for privkey: %w", err)
	}
	// Bit-clamping per RFC 7748 §5: clear bits 0,1,2 of priv[0]; clear bit 7
	// and set bit 6 of priv[31]. The curve25519 package does this internally
	// on use, but doing it here too means anyone reading priv[31] back from
	// storage sees the canonical form.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("derive pubkey: %w", err)
	}
	return priv, pub, nil
}

// ValidateX25519PublicKey returns nil if the bytes are a valid 32-byte
// X25519 public key. Used by ImportIdentity / RegisterIdentity to reject
// obvious garbage before it lands in the DB.
func ValidateX25519PublicKey(pub []byte) error {
	if len(pub) != X25519KeySize {
		return fmt.Errorf("public key must be %d bytes, got %d", X25519KeySize, len(pub))
	}
	// X25519 has no concept of "low-order points" rejection that's
	// universally agreed upon, but the all-zero pubkey is the most common
	// sign of an uninitialised buffer. Reject it.
	var zero [X25519KeySize]byte
	if subtle.ConstantTimeCompare(pub, zero[:]) == 1 {
		return errors.New("public key is all zero")
	}
	return nil
}

// ValidateX25519PrivateKey is the equivalent for a 32-byte privkey. Same
// caveat about low-order points; the bit-clamping is enforced by callers
// because the underlying curve25519 package does it internally on use.
func ValidateX25519PrivateKey(priv []byte) error {
	if len(priv) != X25519KeySize {
		return fmt.Errorf("private key must be %d bytes, got %d", X25519KeySize, len(priv))
	}
	var zero [X25519KeySize]byte
	if subtle.ConstantTimeCompare(priv, zero[:]) == 1 {
		return errors.New("private key is all zero")
	}
	return nil
}

// DerivePubkey computes the X25519 public key for a given private key.
// Used to verify a (priv, pub) pair the operator imports actually
// matches -- catches paste errors before the keys are pushed to the
// Heltec.
func DerivePubkey(priv []byte) ([]byte, error) {
	if err := ValidateX25519PrivateKey(priv); err != nil {
		return nil, err
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive pubkey: %w", err)
	}
	return pub, nil
}

// Fingerprint computes the canonical fleet-security fingerprint of a key.
// Format: first 8 bytes of SHA-256(key), hex-encoded with colon
// separators ("ab:cd:ef:12:34:56:78:9a"). Same format used for both
// pubkey fingerprints and PSK fingerprints, so a single component
// in the UI can display either.
func Fingerprint(key []byte) string {
	h := sha256.Sum256(key)
	parts := make([]string, FingerprintLen)
	for i := 0; i < FingerprintLen; i++ {
		parts[i] = hex.EncodeToString(h[i : i+1])
	}
	return strings.Join(parts, ":")
}

// FingerprintEqual is a constant-time fingerprint comparison. Probably
// overkill given fingerprints aren't secret, but it's cheap.
func FingerprintEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// EncodePubkeyB64 returns the standard-base64 (with padding) form of a
// pubkey, matching what `meshtastic --info` prints. Used by the UI's
// "Export pubkey" action.
func EncodePubkeyB64(pub []byte) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePubkeyB64 parses a pubkey from base64 (standard alphabet, with or
// without padding). Returns ValidateX25519PublicKey-validated bytes.
func DecodePubkeyB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Try with padding first; fall back to raw if that fails.
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("decode pubkey: %w", err)
		}
	}
	if err := ValidateX25519PublicKey(dec); err != nil {
		return nil, err
	}
	return dec, nil
}

// DecodePSKB64 parses a Meshtastic channel PSK from base64. Accepts the
// PSK lengths Meshtastic supports for an encrypted channel (16 or 32
// bytes) and rejects anything else with a length error. Use this rather
// than DecodePubkeyB64 on PSK paths -- the latter requires exactly 32
// bytes and rejects the 16-byte AES-128 case the rotation worker uses
// by default ("Random 16 bytes").
func DecodePSKB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("decode PSK: %w", err)
		}
	}
	if err := ValidatePSK(dec); err != nil {
		return nil, err
	}
	return dec, nil
}

// DecodePrivkeyB64 is the privkey equivalent of DecodePubkeyB64. Used
// only on the inbound edge of ImportIdentity / RecoveryStart; the bytes
// are then handed to the Heltec via SetSecurityConfig and zeroed.
func DecodePrivkeyB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("decode privkey: %w", err)
		}
	}
	if err := ValidateX25519PrivateKey(dec); err != nil {
		return nil, err
	}
	return dec, nil
}

// RandomPSK returns size random bytes suitable for a Meshtastic channel
// PSK. Sizes 0, 16, 32 are accepted; 1 is rejected because Meshtastic
// reserves length-1 PSKs for "default channel index" semantics that we
// don't drive from this code path. Other sizes are also rejected --
// callers should use the constants 0/16/32.
func RandomPSK(size int) ([]byte, error) {
	if !validPSKLengths[size] {
		return nil, fmt.Errorf("invalid PSK size %d (allowed: 0, 16, 32)", size)
	}
	if size == 0 {
		return nil, nil
	}
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}
	return out, nil
}

// ValidatePSK checks that a provided PSK has an accepted length. Empty
// (length 0) is allowed and means "no channel encryption".
func ValidatePSK(psk []byte) error {
	if !validPSKLengths[len(psk)] {
		return fmt.Errorf("invalid PSK length %d (allowed: 0, 16, 32)", len(psk))
	}
	return nil
}

// SecretBytes is a defensive wrapper around byte slices that hold private
// keys or PSKs. Calling Clear() zeros the underlying memory; the GC will
// then drop the buffer. The pattern is:
//
//	priv := fleetsec.NewSecret(privBytes)
//	defer priv.Clear()
//	if err := pushToHeltec(priv.Bytes()); err != nil { ... }
//
// This isn't perfect -- the Go runtime can copy slices, and there's no
// guarantee the original bytes weren't already aliased by the JSON
// unmarshaller -- but it makes the intent explicit and reduces window
// time. Pair with: never log priv bytes; never persist them; only ever
// receive them in request scope.
type SecretBytes struct {
	b []byte
}

// NewSecret wraps b. Does not copy -- the caller must not retain the
// original slice or pass it elsewhere.
func NewSecret(b []byte) *SecretBytes { return &SecretBytes{b: b} }

// Bytes returns the underlying slice. Callers should not retain it past
// the request scope.
func (s *SecretBytes) Bytes() []byte { return s.b }

// Clear zeros the underlying bytes. Idempotent.
func (s *SecretBytes) Clear() {
	for i := range s.b {
		s.b[i] = 0
	}
	s.b = nil
}
