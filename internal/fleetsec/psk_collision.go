package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrChannelHashCollision is returned when a candidate PSK's 1-byte
// channel hash collides with another active channel on Pi-Heltec.
// The firmware decrypts inbound packets against the lowest-slot
// channel that matches the wire hash byte; a collision silently
// drops traffic on the higher-slot channel until the next rotation.
// We refuse to stage such a PSK and force the caller to regenerate
// (random source) or pick a different PSK (explicit source).
var ErrChannelHashCollision = errors.New("PSK channel hash collides with an active channel slot")

// CheckPSKHashCollision returns ErrChannelHashCollision if the
// candidate PSK's hash matches the hash of any currently-active
// Pi-Heltec channel: PRIMARY (slot 0 OR 1) or any recovery cache
// slot (2..7). Hash includes the channel name (we always pass "" so
// it collapses to xor(psk) but the helper accepts both for forward-
// compat).
//
// Used as a pre-flight gate by RotatePSK before staging.
func (s *Service) CheckPSKHashCollision(ctx context.Context, candidate []byte) error {
	if len(candidate) == 0 {
		return nil // empty PSK never collides — always-zero hash
	}
	candHash := ChannelHash("", candidate)

	// Walk Pi's slots 0/1 (active rotation pingpong slots).
	for idx := int32(0); idx < 2; idx++ {
		ch, err := s.readLocalChannel(ctx, idx)
		if err != nil {
			continue // unreachable slot is treated as "not in use"
		}
		var psk []byte
		if ch.GetSettings() != nil {
			psk = ch.GetSettings().GetPsk()
		}
		if len(psk) == 0 {
			continue
		}
		if ChannelHash("", psk) == candHash {
			return fmt.Errorf("%w (slot %d active PSK)", ErrChannelHashCollision, idx)
		}
	}

	// Walk recovery cache slots (2..7).
	recs, err := s.store.ListRecoveryPSKs(ctx)
	if err != nil {
		// Don't block staging on a transient store hiccup; the
		// startup reconcile will catch any drift later. Log + allow.
		slog.Warn("psk collision check: list recovery psks failed",
			"error", err)
		return nil
	}
	for _, r := range recs {
		if byte(r.PSKHash) == candHash {
			return fmt.Errorf("%w (recovery slot %d)", ErrChannelHashCollision, r.Slot)
		}
	}
	return nil
}

// GenerateRandomPSKAvoidCollision tries up to maxAttempts times to
// pick a non-colliding random PSK of the given length. With ~7
// active channels and 256 hash buckets, expected attempts < 2.
// Returns the last error if every attempt collided.
func (s *Service) GenerateRandomPSKAvoidCollision(ctx context.Context, size, maxAttempts int) ([]byte, error) {
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		psk, err := RandomPSK(size)
		if err != nil {
			return nil, err
		}
		if cErr := s.CheckPSKHashCollision(ctx, psk); cErr == nil {
			return psk, nil
		} else {
			lastErr = cErr
			NewSecret(psk).Clear()
		}
	}
	return nil, fmt.Errorf("could not generate non-colliding PSK in %d attempts: %w", maxAttempts, lastErr)
}
