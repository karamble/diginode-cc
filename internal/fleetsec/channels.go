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
			Phase:   PhasePending,
			Status:  TargetStatusPending,
		})
	}
	// Append local last (if known and not already in the list).
	if local := s.localNode.LocalNodeNum(); local != 0 && !seen[local] {
		rotTargets = append(rotTargets, RotationTarget{
			NodeNum: local,
			Phase:   PhasePending,
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

// chooseStagingSlot picks the channel index where the new PSK lives
// during the rotation. For now: always use slot 1 unless the operator
// has rotated PRIMARY there for some reason. Future: scan
// fleet_channels for the lowest unused slot in [1..7] and avoid
// PRIMARY/SECONDARY collisions. The chosen slot is pinned on the
// rotation row so retries find the same definition on the remotes.
func (s *Service) chooseStagingSlot(currentPrimary int32) (int32, error) {
	const defaultStaging int32 = 1
	if currentPrimary == defaultStaging {
		// Edge case: PRIMARY is on slot 1 already; punt to slot 2 so we
		// don't overwrite the active channel. A more sophisticated picker
		// would scan all 8 slots; for the documented "rotate channel 0"
		// flow this branch never fires.
		return 2, nil
	}
	return defaultStaging, nil
}

// applyLocalStagingChannel runs Phase A: write the new PSK into a
// SECONDARY-role channel slot on the local Heltec. After this returns
// the Pi can decrypt traffic on EITHER the existing PRIMARY (old PSK)
// or the staging slot (new PSK), so per-target acks during Phase B
// land on the unchanged PRIMARY and decode cleanly.
//
// Local admin path -- no PSK gap, no session_passkey enforcement, just
// a SetChannel that the firmware applies live (no reboot, per the
// channel admin exception in feedback_meshtastic_pacing.md).
func (s *Service) applyLocalStagingChannel(ctx context.Context, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	msg := AdminSetChannel(stagingIdx, "", pb.Channel_SECONDARY, newPSK)
	if _, err := s.runLocalAdmin(ctx, msg, "local-stage-secondary"); err != nil {
		return fmt.Errorf("apply staging channel locally: %w", err)
	}
	return nil
}

// pushStagingToRemote runs Phase B against one remote: send
// SetChannel(stagingIdx, role=SECONDARY, psk=newPSK) over PKC. Acks
// ride the unchanged PRIMARY (old PSK) so the routing-layer round-trip
// is fully decryptable. The worker wraps this in the per-target retry
// loop and broadcasts phase transitions via transitionTarget.
func (s *Service) pushStagingToRemote(ctx context.Context, nodeNum uint32, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	// Establish session_passkey via GetConfig SECURITY first; the
	// SetChannel that follows requires the firmware-emitted passkey.
	if _, err := s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-establish-session"); err != nil {
		return fmt.Errorf("session establish: %w", err)
	}
	setMsg := AdminSetChannel(stagingIdx, "", pb.Channel_SECONDARY, newPSK)
	if _, err := s.runRemoteAdmin(ctx, nodeNum, setMsg, "remote-stage-secondary"); err != nil {
		return fmt.Errorf("push staging: %w", err)
	}
	return nil
}

// promoteRemoteToNewPrimary runs Phase C against one remote: send
// SetChannel(stagingIdx, role=PRIMARY, psk=newPSK). Meshtastic firmware
// auto-demotes the previous PRIMARY (oldChannelIndex) to SECONDARY when
// a new slot is marked PRIMARY -- per the proto comment on
// AdminMessage.SetChannel. So one admin per remote does both moves
// atomically; old PSK stays alive as SECONDARY for graceful migration.
//
// Acks during this transition can ride either the old or new channel
// depending on firmware ordering, but both are alive on the remote
// AND on Pi (Phase A added staging on Pi already), so the ack is
// always decryptable. No PSK gap.
func (s *Service) promoteRemoteToNewPrimary(ctx context.Context, nodeNum uint32, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	// Refresh the session passkey -- the cached one from Phase B is
	// likely still valid (300s TTL) but cheap to refresh.
	if _, err := s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-promote-establish-session"); err != nil {
		return fmt.Errorf("session establish: %w", err)
	}
	setMsg := AdminSetChannel(stagingIdx, "", pb.Channel_PRIMARY, newPSK)
	if _, err := s.runRemoteAdmin(ctx, nodeNum, setMsg, "remote-promote-primary"); err != nil {
		return fmt.Errorf("promote primary: %w", err)
	}
	return nil
}

// promotePiToNewPrimary runs Phase D: local SetChannel(stagingIdx,
// role=PRIMARY) so the Pi-Heltec also moves to the new PRIMARY.
// Firmware auto-demotes the old PRIMARY to SECONDARY here too,
// keeping it alive for laggards still on the old PSK who haven't
// completed Phase C yet.
func (s *Service) promotePiToNewPrimary(ctx context.Context, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	msg := AdminSetChannel(stagingIdx, "", pb.Channel_PRIMARY, newPSK)
	if _, err := s.runLocalAdmin(ctx, msg, "local-promote-primary"); err != nil {
		return fmt.Errorf("promote primary locally: %w", err)
	}
	return nil
}

// transitionTarget is the single point of change for a target's phase.
// Persists the new phase + lastError; bumps the legacy Status field via
// statusForPhase; broadcasts the WS event so the UI updates live.
func (s *Service) transitionTarget(ctx context.Context, rotID string, channelIdx int32, current []RotationTarget, t *RotationTarget, phase RotationPhase, errMsg string, newPSKFP string) {
	t.Phase = phase
	t.Status = statusForPhase(phase)
	t.LastError = errMsg
	if err := s.store.UpdateTargetPhase(ctx, rotID, t.NodeNum, phase, errMsg); err != nil {
		slog.Warn("persist target phase",
			"rotation_id", rotID, "node_num", t.NodeNum, "phase", phase, "error", err)
	}
	s.persistAndBroadcast(ctx, rotID, channelIdx, current, false, newPSKFP)
}

// runPSKRotation is the long-running background loop. Implements the
// 5-phase staged rotation
// (project_psk_rotation_secondary_channel_staging.md):
//
//	A. Pi adds new PSK as a SECONDARY slot (so Pi can decrypt acks on
//	   either the old PRIMARY or the new SECONDARY going forward).
//	B. Each remote receives SetChannel(stagingIdx, SECONDARY, newPSK).
//	   Acks ride the still-shared old PRIMARY -- fully decryptable.
//	C. Each remote receives SetChannel(stagingIdx, PRIMARY, newPSK).
//	   Firmware auto-demotes the old PRIMARY to SECONDARY. Both
//	   channels remain alive on the remote; both are alive on Pi from
//	   Phase A; acks decode either way.
//	D. Pi local SetChannel(stagingIdx, PRIMARY) -- same auto-demotion
//	   gives Pi PRIMARY=newPSK + SECONDARY=oldPSK.
//	E. Operator-paced retirement (separate endpoint, not part of this
//	   worker): once every fleet member's current_psk_fp matches the
//	   new fp, disable the old SECONDARY slot on Pi and remotes.
//
// Failure modes are graceful: a target that fails Phase B stays on
// PSK_OLD only and is fully reachable; a target that fails Phase C has
// both channels and is reachable on either. Retry resumes from the
// last good resting state per target.
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
	pskFP := Fingerprint(newPSK)

	// Pick a staging slot and pin it on the rotation row so retries find
	// the same definition on the remotes. For the documented "rotate
	// channel 0" flow this picks slot 1; chooseStagingSlot handles the
	// edge case where slot 1 is the operator's PRIMARY.
	stagingIdx, sErr := s.chooseStagingSlot(channelIndex)
	if sErr != nil {
		slog.Error("choose staging slot",
			"rotation_id", rotID, "error", sErr)
		return
	}
	if err := s.store.SetStagingChannelIndex(ctx, rotID, stagingIdx); err != nil {
		slog.Warn("persist staging_channel_index",
			"rotation_id", rotID, "error", err)
	}

	// Split target list: remotes go through Phase B + C; local goes
	// through Phase D. Local is intentionally separate -- its admin path
	// is in-process and uses different firmware semantics (no session
	// passkey, no PSK gap).
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

	// ---- Phase A: Pi adds the new PSK as a SECONDARY channel slot ----
	if err := s.applyLocalStagingChannel(ctx, stagingIdx, newPSK); err != nil {
		// Hard abort -- without staging on Pi, Phase B acks would be
		// undecryptable in any subsequent channel transition. Mark every
		// target failed_b so the operator knows nothing happened.
		slog.Error("phase A staging failed",
			"rotation_id", rotID, "error", err)
		for _, t := range remoteTargets {
			s.transitionTarget(ctx, rotID, channelIndex, current, t, PhaseFailedB, "phase A staging failed: "+err.Error(), pskFP)
		}
		if localTarget != nil {
			s.transitionTarget(ctx, rotID, channelIndex, current, localTarget, PhaseFailedB, "phase A staging failed: "+err.Error(), pskFP)
		}
		s.persistAndBroadcast(ctx, rotID, channelIndex, current, true, pskFP)
		return
	}
	if err := s.store.UpsertPiLocalPhase(ctx, rotID, PiPhaseStagingAdded); err != nil {
		slog.Warn("persist pi_local_phase=staging_added",
			"rotation_id", rotID, "error", err)
	}

	// ---- Phase B: push staging slot to each remote ----
	for i, t := range remoteTargets {
		_ = s.store.IncrementTargetAttempts(ctx, rotID, t.NodeNum)
		s.transitionTarget(ctx, rotID, channelIndex, current, t, PhasePushingB, "", pskFP)

		err := s.pushStagingToRemote(ctx, t.NodeNum, stagingIdx, newPSK)
		if err != nil {
			s.transitionTarget(ctx, rotID, channelIndex, current, t, PhaseFailedB, err.Error(), pskFP)
			slog.Warn("PSK rotation phase B failed",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
		} else {
			s.transitionTarget(ctx, rotID, channelIndex, current, t, PhaseHasNewPSK, "", pskFP)
		}

		if opts.InterTargetDelay > 0 && i+1 < len(remoteTargets) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(opts.InterTargetDelay):
			}
		}
	}

	// ---- Phase C: promote staging to PRIMARY on each remote ----
	for _, t := range remoteTargets {
		// Skip remotes that failed Phase B; they don't have the new PSK
		// yet so promoting would just fail again. Operator's Retry will
		// re-run Phase B for them.
		if t.Phase == PhaseFailedB {
			continue
		}
		s.transitionTarget(ctx, rotID, channelIndex, current, t, PhasePromotingC, "", pskFP)

		err := s.promoteRemoteToNewPrimary(ctx, t.NodeNum, stagingIdx, newPSK)
		if err != nil {
			s.transitionTarget(ctx, rotID, channelIndex, current, t, PhaseFailedC, err.Error(), pskFP)
			slog.Warn("PSK rotation phase C failed",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
		} else {
			s.transitionTarget(ctx, rotID, channelIndex, current, t, PhaseOnNewPSK, "", pskFP)
			// MarkTrustVerifiedNow stamps last_verified_at; the remote's
			// current_psk_fp gets updated on the next GetTrust round-trip.
			if mErr := s.store.MarkTrustVerifiedNow(ctx, t.NodeNum, VerifyMethodRemotePKC); mErr != nil {
				slog.Warn("mark trust verified after promote",
					"rotation_id", rotID, "node_num", t.NodeNum, "error", mErr)
			}
		}
	}

	// ---- Phase D: Pi promotes locally ----
	if localTarget != nil {
		_ = s.store.IncrementTargetAttempts(ctx, rotID, localNum)
		s.transitionTarget(ctx, rotID, channelIndex, current, localTarget, PhasePromotingC, "", pskFP)

		err := s.promotePiToNewPrimary(ctx, stagingIdx, newPSK)
		if err != nil {
			s.transitionTarget(ctx, rotID, channelIndex, current, localTarget, PhaseFailedC, err.Error(), pskFP)
			slog.Error("PSK rotation phase D (local promote) failed",
				"rotation_id", rotID, "error", err)
		} else {
			s.transitionTarget(ctx, rotID, channelIndex, current, localTarget, PhaseOnNewPSK, "", pskFP)
			if mErr := s.store.MarkTrustVerifiedNow(ctx, localNum, VerifyMethodLocalUSB); mErr != nil {
				slog.Warn("mark local trust verified", "rotation_id", rotID, "error", mErr)
			}
		}
	}

	if err := s.store.UpsertPiLocalPhase(ctx, rotID, PiPhasePhaseDPromoted); err != nil {
		slog.Warn("persist pi_local_phase=phase_d_promoted",
			"rotation_id", rotID, "error", err)
	}

	// Refresh current_psk_fp for every member that landed on the new PSK
	// -- a successful Phase C / D doesn't itself prove channel-layer
	// alignment (the Pi could be on the new PRIMARY while a remote is
	// stalled on phase_c_promoting), but a follow-up GetTrust does.
	// Operator-driven retirement gates on this, so a fresh stamp here
	// makes "all migrated" detectable without needing each operator to
	// click Verify on every node post-rotation.
	for _, t := range remoteTargets {
		if t.Phase != PhaseOnNewPSK {
			continue
		}
		if _, err := s.GetTrust(ctx, t.NodeNum); err != nil {
			slog.Warn("post-rotation trust refresh",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
		}
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
