package fleetsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// RotationProgressEvent is the payload of WS events emitted as a
// rotation walks its target list. The frontend's RotationProgressDrawer
// subscribes to fleet-security.rotation.progress and updates the per-
// target status pills in real time.
type RotationProgressEvent struct {
	RotationID string           `json:"rotationId"`
	Kind       RotationKind     `json:"kind"`
	Targets    []RotationTarget `json:"targets"`
	Done       bool             `json:"done"`
	NewPSKFP   string           `json:"newPskFingerprint,omitempty"`
}

// EventFleetSecRotation is the WS event type fleetsec emits for
// rotation progress. Registered in the existing ws.EventType enum
// surface so the frontend's bridge can route on it.
const EventFleetSecRotation ws.EventType = "fleet-security.rotation.progress"

// hub is an optional reference for broadcasting rotation events.
// Service.WireHub plugs it in from main.go after construction so the
// ws.Hub can stay an injection rather than a dependency.
type hubRef struct {
	mu  sync.Mutex
	hub *ws.Hub
}

func (h *hubRef) set(hub *ws.Hub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hub = hub
}

func (h *hubRef) broadcast(evt ws.Event) {
	h.mu.Lock()
	hub := h.hub
	h.mu.Unlock()
	if hub != nil {
		hub.Broadcast(evt)
	}
}

// WireHub injects the WebSocket hub for live rotation progress events.
// May be called once after NewService; nil-safe (no broadcast happens
// until a real hub is supplied).
func (s *Service) WireHub(hub *ws.Hub) {
	s.hubRef.set(hub)
}

// ListChannels returns the channel snapshot for the Channels card.
// Coverage data (X/Y nodes on the current PSK) is deferred -- per-node
// channel-PSK tracking would require extending GetTrust to read each
// node's GetChannel(0) reply too, which is out of scope for v1.
func (s *Service) ListChannels(ctx context.Context) ([]ChannelRecord, error) {
	return s.store.ListChannels(ctx)
}

// RotatePSKOpts modifies RotatePSK behaviour.
type RotatePSKOpts struct {
	// Ack must be the exact string "ROTATE" -- the typed-confirmation
	// gate the UI surfaces in RotatePSKModal.
	Ack string
	// Notes is a free-form description that lands in the audit log
	// and the fleet_rotations.notes column.
	Notes string
	// InterTargetDelay is the gap between target sends, used to honour
	// EU868 / similar duty-cycle limits. 0 = no extra delay (use the
	// transaction tracker timeout cadence as the natural pace).
	InterTargetDelay time.Duration
}

// RotatePSK initiates a fleet-wide PSK rotation. Returns immediately
// with the RotationID; the actual rotation runs in a background
// goroutine that walks the targets sequentially, updating
// fleet_rotations and broadcasting WS progress events as each target
// acks (or fails after retry exhaustion). The local Heltec is rotated
// last so we don't lose the ability to reach remaining targets
// mid-rotation.
//
// channelIndex 0 is the primary channel by Meshtastic convention.
//
// newPSK length must be 0/16/32 per ValidatePSK (1 is reserved by
// firmware for default-channel-index semantics).
//
// targets is the list of remote node numbers. The local Heltec is
// always appended at the end (caller doesn't need to include it).
//
// userID is the JWT-context user driving the request; threaded through
// for audit and the started_by column.
func (s *Service) RotatePSK(
	ctx context.Context,
	userID string,
	channelIndex int32,
	newPSK []byte,
	targets []uint32,
	opts RotatePSKOpts,
) (string, error) {
	if opts.Ack != "ROTATE" {
		return "", fmt.Errorf("PSK rotation requires Ack=\"ROTATE\": %w", ErrInvalidAck)
	}
	if err := ValidatePSK(newPSK); err != nil {
		return "", err
	}
	if channelIndex < 0 {
		return "", errors.New("channelIndex must be >= 0")
	}

	// Build the initial targets slice. Service operations need an
	// isolated copy of newPSK -- the caller's slice could outlive this
	// call's scope and we want to clear it on completion.
	pskCopy := append([]byte(nil), newPSK...)
	rotTargets := make([]RotationTarget, 0, len(targets))
	seen := map[uint32]bool{}
	for _, t := range targets {
		if t == 0 || seen[t] {
			continue
		}
		seen[t] = true
		rotTargets = append(rotTargets, RotationTarget{
			NodeNum: t,
			Status:  TargetStatusPending,
		})
	}
	// Append local last (if known and not already in the list).
	if local := s.localNode.LocalNodeNum(); local != 0 && !seen[local] {
		rotTargets = append(rotTargets, RotationTarget{
			NodeNum: local,
			Status:  TargetStatusPending,
		})
	}
	if len(rotTargets) == 0 {
		return "", errors.New("no valid targets")
	}

	channelIdxCopy := channelIndex
	rotID, err := s.store.InsertRotation(ctx, RotationRecord{
		Kind:         RotationKindPSK,
		ChannelIndex: &channelIdxCopy,
		StartedBy:    userID,
		Targets:      rotTargets,
		NewPSKFP:     Fingerprint(pskCopy),
		Notes:        opts.Notes,
	}, pskCopy)
	if err != nil {
		return "", fmt.Errorf("create rotation: %w", err)
	}

	s.auditFleet(ctx, userID, "rotate_psk_start", "channel", fmt.Sprintf("%d", channelIndex), map[string]any{
		"rotation_id":      rotID,
		"target_count":     len(rotTargets),
		"new_psk_fp":       Fingerprint(pskCopy),
		"psk_length":       len(pskCopy),
		"notes":            opts.Notes,
		// raw psk intentionally omitted (redactSecrets would catch it
		// even if a future caller leaked it).
	})

	// Detached context for the background runner -- the caller's
	// HTTP handler context expires once the response is written.
	go s.runPSKRotation(context.Background(), userID, rotID, channelIndex, pskCopy, rotTargets, opts)

	return rotID, nil
}

// runPSKRotation is the long-running background loop. Walks targets
// sequentially; updates each target's status in fleet_rotations as it
// goes; broadcasts a RotationProgressEvent after every status change so
// the UI's progress drawer stays current.
//
// The local Heltec is the LAST target so we don't risk a self-rotation
// that locks us out before remaining remotes are done. Failed remote
// targets retain the old PSK (not the new one); the operator can retry
// via the failed tray.
func (s *Service) runPSKRotation(
	ctx context.Context,
	userID, rotID string,
	channelIndex int32,
	newPSK []byte,
	targets []RotationTarget,
	opts RotatePSKOpts,
) {
	defer func() {
		// Best-effort zero of the PSK after the rotation runner exits.
		for i := range newPSK {
			newPSK[i] = 0
		}
	}()

	current := append([]RotationTarget(nil), targets...)
	localNum := s.localNode.LocalNodeNum()

	// Four-phase rotation:
	//  1. PUSH    -- Get-establish-session + send SetChannel to each remote.
	//                Don't wait for the SetChannel routing ack: the remote
	//                applies SetChannel and immediately switches to the new
	//                channel PSK; its ack rides the new channel which our
	//                local Heltec can't decrypt yet (we rotate last). The
	//                ack would arrive as port=UNKNOWN payloadLen=0 on our
	//                side. Treat the optimistic push as succeeded if the
	//                Get-session step succeeded (proves the remote was
	//                reachable on the OLD PSK at push time).
	//  2. WAIT    -- brief sleep so remotes have time to commit the new PSK
	//                before the local Heltec rotates and the channel layer
	//                breaks for any remote that didn't apply.
	//  3. LOCAL   -- self-rotate the local Heltec via runLocalAdmin (no PSK
	//                gap on the local side; admin uses the same in-process
	//                serial path).
	//  4. VERIFY  -- after local moved to the new PSK, GetTrust each remote
	//                via PKC. If GetConfig SECURITY round-trips on the new
	//                PSK channel, the remote successfully applied the
	//                rotation. Failure here = remote did not apply (still
	//                stranded on old PSK) or was unreachable.
	//
	// Why we don't trust the SetChannel routing ack: tested 2026-05-08, a
	// remote running through SetChannel applies new PSK before its ack
	// frame leaves the radio. The ack is encrypted with the new PSK; the
	// sender (still on old PSK) sees only undecryptable bytes. So per-
	// target ack inspection has zero correlation with rotation success.
	// Verify-after-rotation tests the only signal that matters: can we
	// PKC-admin the remote on the post-rotation channel.

	// Split target list: remotes get phases 1 + 4; local gets phase 3.
	var localTarget *RotationTarget
	remoteTargets := make([]*RotationTarget, 0, len(current))
	for i := range current {
		t := &current[i]
		if t.NodeNum == localNum {
			localTarget = t
		} else {
			remoteTargets = append(remoteTargets, t)
		}
	}

	// ---- Phase 1: optimistic push to each remote ----
	for i, t := range remoteTargets {
		t.Status = TargetStatusInFlight
		t.Attempts++
		s.persistAndBroadcast(ctx, rotID, channelIndex, current, false, Fingerprint(newPSK))

		err := s.pushRemoteRotation(ctx, t.NodeNum, channelIndex, newPSK)
		if err != nil {
			t.Status = TargetStatusFailed
			t.LastError = err.Error()
			slog.Warn("PSK rotation push failed",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
			s.persistAndBroadcast(ctx, rotID, channelIndex, current, false, Fingerprint(newPSK))
		}
		// Status remains in-flight on push success; phase 4 promotes to
		// acked or marks failed based on post-rotation reachability.

		if opts.InterTargetDelay > 0 && i+1 < len(remoteTargets) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(opts.InterTargetDelay):
			}
		}
	}

	// ---- Phase 2: brief settle so remotes commit before local rotates ----
	// 5s is plenty for ESP32 to apply SetChannel + persist to NVS. Any
	// shorter and we risk rotating local before slow remotes finished;
	// then we'd verify them on the new channel before they're listening.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	// ---- Phase 3: local self-rotate ----
	if localTarget != nil {
		localTarget.Status = TargetStatusInFlight
		localTarget.Attempts++
		s.persistAndBroadcast(ctx, rotID, channelIndex, current, false, Fingerprint(newPSK))
		err := s.rotateOneTarget(ctx, localNum, channelIndex, newPSK, true)
		if err != nil {
			localTarget.Status = TargetStatusFailed
			localTarget.LastError = err.Error()
			slog.Warn("PSK rotation local failed",
				"rotation_id", rotID, "node_num", localNum, "error", err)
		} else {
			localTarget.Status = TargetStatusAcked
			localTarget.LastError = ""
			if mErr := s.store.MarkTrustVerifiedNow(ctx, localNum, VerifyMethodLocalUSB); mErr != nil {
				slog.Warn("mark local trust verified", "rotation_id", rotID, "error", mErr)
			}
			slog.Info("PSK rotation local acked", "rotation_id", rotID)
		}
		s.persistAndBroadcast(ctx, rotID, channelIndex, current, false, Fingerprint(newPSK))
	}

	// ---- Phase 4: verify each remote on the post-rotation channel ----
	for _, t := range remoteTargets {
		// Don't re-test entries we already failed in push; their session
		// or transport was demonstrably broken before we even rotated.
		if t.Status == TargetStatusFailed {
			continue
		}
		// GetTrust does a fresh PKC GetConfig SECURITY round-trip and
		// stamps last_verified_at on success. Round-tripping post-
		// rotation proves the remote is on the new PSK == it applied
		// our SetChannel. Failure means stranded on old PSK or
		// otherwise unreachable.
		_, err := s.GetTrust(ctx, t.NodeNum)
		if err != nil {
			t.Status = TargetStatusFailed
			t.LastError = fmt.Sprintf("post-rotation verify: %v", err)
			slog.Warn("PSK rotation verify failed",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
		} else {
			t.Status = TargetStatusAcked
			t.LastError = ""
			slog.Info("PSK rotation verified",
				"rotation_id", rotID, "node_num", t.NodeNum)
		}
		s.persistAndBroadcast(ctx, rotID, channelIndex, current, false, Fingerprint(newPSK))
	}

	// Update the channel snapshot with the new PSK fingerprint.
	if err := s.store.UpsertChannel(ctx, ChannelRecord{
		Index:           channelIndex,
		Name:            "", // preserved via COALESCE in store
		Role:            "",
		PSKFingerprint:  Fingerprint(newPSK),
		PSKLength:       len(newPSK),
		LastRotatedAt:   timeNowPtr(),
		LastRotatedBy:   userID,
		LastRotationID:  rotID,
	}); err != nil {
		slog.Error("update channel after rotation",
			"rotation_id", rotID, "error", err)
	}

	// Final broadcast: done flag set; UI's progress drawer transitions
	// to its "complete" state.
	s.persistAndBroadcast(ctx, rotID, channelIndex, current, true, Fingerprint(newPSK))

	// Audit summary.
	successes := 0
	failures := 0
	for _, t := range current {
		if t.Status == TargetStatusAcked {
			successes++
		} else {
			failures++
		}
	}
	s.auditFleet(ctx, userID, "rotate_psk_complete", "channel",
		fmt.Sprintf("%d", channelIndex), map[string]any{
			"rotation_id": rotID,
			"successes":   successes,
			"failures":    failures,
			"new_psk_fp":  Fingerprint(newPSK),
		})

	// Drop the stashed raw PSK once every target reached acked. A failed
	// target keeps the row populated so RetryRotation can resume the same
	// PSK without the operator re-supplying it. The deferred zero of
	// newPSK above clears the in-memory copy regardless.
	if failures == 0 {
		if err := s.store.ClearRotationPSK(ctx, rotID); err != nil {
			slog.Warn("clear rotation psk", "rotation_id", rotID, "error", err)
		}
	}
}

// pushRemoteRotation runs the phase-1 push for one remote target.
// First does GetConfig SECURITY to (a) confirm the remote is reachable
// on the current channel and (b) install the firmware-emitted session
// passkey in our cache. Then sends SetChannel optimistically -- doesn't
// wait for the ack because the remote applies the new PSK before its
// ack frame leaves the radio, and the ack rides the new channel which
// the local Heltec won't decrypt until phase 3. Phase 4's GetTrust is
// the actual confirmation.
//
// Returns nil on push success (Get round-tripped, Set frame queued).
// Returns an error only if the Get establish-session step itself
// failed (remote unreachable on current channel) or the SendToRadio
// call failed (local serial broken).
func (s *Service) pushRemoteRotation(ctx context.Context, nodeNum uint32, channelIndex int32, newPSK []byte) error {
	if channelIndex != 0 {
		return fmt.Errorf("non-primary channel rotation (index %d) not supported", channelIndex)
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	// Phase 1a: establish session. The Get reply populates our session
	// passkey cache via send() -> cacheSessionPasskey. Required so the
	// SetChannel below carries a valid passkey on the wire (firmware
	// rejects Set without it after the post-reboot grace window).
	if _, err := s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-establish-session"); err != nil {
		return fmt.Errorf("session establish: %w", err)
	}

	// Phase 1b: optimistic SetChannel push. Inject the cached passkey,
	// build the PKC frame, hand it to the serial layer. We don't track
	// or wait for the response: the ack would be undecryptable across
	// the about-to-open PSK gap (we rotate local last). Phase 4 verifies
	// via a fresh GetConfig round-trip on the post-rotation channel.
	setMsg := AdminSetChannel(channelIndex, "", pb.Channel_PRIMARY, newPSK)
	if pk := s.getSessionPasskey(nodeNum); len(pk) > 0 {
		setMsg.SessionPasskey = pk
	}
	frame, _, err := serial.BuildAdminPacketPKC(nodeNum, setMsg)
	if err != nil {
		return fmt.Errorf("build set_channel: %w", err)
	}
	if err := s.serial.SendToRadio(frame); err != nil {
		return fmt.Errorf("push set_channel: %w", err)
	}
	return nil
}

// rotateOneTarget runs the get_channel + set_channel pair against one
// node. Reads the existing Channel proto (so we don't clobber name or
// role), patches PSK, sends back, awaits Routing ack. Up to 3 attempts
// with linear backoff (1s, 2s, 4s).
func (s *Service) rotateOneTarget(
	ctx context.Context,
	nodeNum uint32,
	channelIndex int32,
	newPSK []byte,
	isLocal bool,
) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := s.tryRotateOnce(ctx, nodeNum, channelIndex, newPSK, isLocal)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < maxAttempts {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
}

func (s *Service) tryRotateOnce(
	ctx context.Context,
	nodeNum uint32,
	channelIndex int32,
	newPSK []byte,
	isLocal bool,
) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	// We previously read the existing channel via AdminGetChannel to
	// preserve name + role across the rotation, but Meshtastic firmware
	// (verified through 2.7.23) does not reply to PKC get_channel_request
	// -- the local Heltec acks transport, the remote node receives the
	// packet, but no get_channel_response payload is ever emitted. Local
	// AdminGetChannel against the host firmware also fails with
	// routing-error BAD_REQUEST. The Meshtastic Python CLI works around
	// this by silently swallowing those timeouts and sending SetChannel
	// with defaults; we adopt the same approach.
	//
	// For PRIMARY channel rotations (channelIndex 0) the role is always
	// PRIMARY; the firmware preserves the existing name when SetChannel
	// is sent with name="" because the protobuf field stays unset on the
	// wire. Non-primary indices would need either a working GetChannel
	// or operator-supplied name+role -- callers currently only rotate
	// channel 0 so we error out for >0 to surface the limitation rather
	// than silently re-default a secondary channel.
	if channelIndex != 0 {
		return fmt.Errorf("non-primary channel rotation (index %d) not supported: firmware get_channel admin path is unresponsive; supply name+role explicitly when this is wired up", channelIndex)
	}
	role := pb.Channel_PRIMARY
	name := ""

	// For remote targets, ensure we have a fresh admin session passkey
	// before sending SetChannel. Meshtastic firmware requires the passkey
	// on every set_* admin and rejects with ADMIN_BAD_SESSION_KEY
	// otherwise (TTL 300s, reset on remote reboot). GetConfig SECURITY
	// is the cheapest Get that gives us a passkey -- the firmware
	// includes it in the response AdminMessage and runRemoteAdmin caches
	// it for the SetChannel that follows. Local admin doesn't enforce
	// session passkeys (different firmware code path) so we skip the
	// pre-step there.
	if !isLocal {
		_, gErr := s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-establish-session")
		if gErr != nil {
			return fmt.Errorf("session establish: %w", gErr)
		}
	}

	setMsg := AdminSetChannel(channelIndex, name, role, newPSK)
	var err error
	if isLocal {
		_, err = s.runLocalAdmin(ctx, setMsg, "local-set-channel")
	} else {
		_, err = s.runRemoteAdmin(ctx, nodeNum, setMsg, "remote-set-channel")
	}
	if err != nil {
		return fmt.Errorf("set_channel: %w", err)
	}
	return nil
}

// persistAndBroadcast writes the current rotation target snapshot back
// to fleet_rotations and pushes a WS event so the UI updates in real
// time. completedAt is non-nil only for the final broadcast (done=true).
func (s *Service) persistAndBroadcast(
	ctx context.Context,
	rotID string,
	channelIndex int32,
	targets []RotationTarget,
	done bool,
	pskFP string,
) {
	var completedAt *time.Time
	if done {
		t := time.Now().UTC()
		completedAt = &t
	}
	if err := s.store.UpdateRotationTargets(ctx, rotID, targets, completedAt); err != nil {
		slog.Error("persist rotation progress",
			"rotation_id", rotID, "error", err)
	}
	s.hubRef.broadcast(ws.Event{
		Type: EventFleetSecRotation,
		Payload: RotationProgressEvent{
			RotationID: rotID,
			Kind:       RotationKindPSK,
			Targets:    targets,
			Done:       done,
			NewPSKFP:   pskFP,
		},
	})
}

// GetRotation returns the rotation row by id. The UI's progress drawer
// uses this on initial open (the WS feed only carries delta events).
func (s *Service) GetRotation(ctx context.Context, id string) (*RotationRecord, error) {
	return s.store.GetRotation(ctx, id)
}

// RetryRotation resends the PSK to the failed targets. The new PSK is
// the one recorded on fleet_rotations.new_psk_fp -- we DO NOT re-take
// PSK input here because storing the PSK bytes would defeat the
// fingerprint-only invariant.
//
// Limitation: retry only works while the operator still has the PSK
// plaintext available (i.e. immediately after the original push).
// Because we don't persist the PSK, retries past a process restart
// require a fresh RotatePSK call. The handler enforces this by
// requiring the caller to supply newPSK on retry too -- the rotation
// id is just for status correlation.
func (s *Service) RetryRotation(
	ctx context.Context,
	userID, id string,
	newPSK []byte,
	targetNodeNums []uint32,
) error {
	rec, err := s.store.GetRotation(ctx, id)
	if err != nil {
		return err
	}
	// If the caller didn't supply a PSK, try to pick up the one we
	// stashed when the rotation was originally launched. The column is
	// NULLed only after every target reached acked, so any rotation
	// with failed targets still has its PSK on hand.
	if len(newPSK) == 0 {
		stored, err := s.store.GetRotationPSK(ctx, id)
		if err != nil {
			return fmt.Errorf("fetch stored psk: %w", err)
		}
		if len(stored) == 0 {
			return errors.New("no PSK supplied and none stored for this rotation (already fully acked, or pre-dates migration 000026)")
		}
		newPSK = stored
		defer NewSecret(newPSK).Clear()
	}
	if err := ValidatePSK(newPSK); err != nil {
		return err
	}
	if Fingerprint(newPSK) != rec.NewPSKFP {
		return errors.New("supplied PSK does not match this rotation's recorded fingerprint")
	}
	if rec.ChannelIndex == nil {
		return errors.New("rotation has no channel index (not a PSK rotation?)")
	}

	// Mark targeted entries pending again for the runner.
	want := map[uint32]bool{}
	for _, t := range targetNodeNums {
		want[t] = true
	}
	current := append([]RotationTarget(nil), rec.Targets...)
	any := false
	for i := range current {
		if want[current[i].NodeNum] && current[i].Status == TargetStatusFailed {
			current[i].Status = TargetStatusPending
			current[i].LastError = ""
			any = true
		}
	}
	if !any {
		return errors.New("no failed targets matched the retry list")
	}

	pskCopy := append([]byte(nil), newPSK...)
	go s.runPSKRotation(context.Background(), userID, id, *rec.ChannelIndex, pskCopy, current, RotatePSKOpts{Ack: "ROTATE"})
	return nil
}

// hubRef field accessor -- service struct embedding means this is
// available via s.hubRef. Embedding decision: kept as a field rather
// than embedding because the helper struct is package-private.

// timeNowPtr is a small allocation helper; saves repeating the pattern.
func timeNowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}

// json import shield -- fleet_rotations.targets is JSONB and gets
// marshalled in the store layer; this var keeps the import live so
// future additions here don't need to remember to add it.
var _ = json.Marshal
