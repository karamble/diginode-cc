package fleetsec

import (
	"bytes"
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

// probeSlotsLocal scans slots 0..7 on the local Heltec via
// AdminGetChannel. Returns the set of DISABLED slot indices and the
// PRIMARY slot. Slots that fail to probe are treated as "unknown" --
// excluded from the empty set so the picker won't write to them.
func (s *Service) probeSlotsLocal(ctx context.Context) (empty map[int32]bool, primary int32, err error) {
	empty = make(map[int32]bool)
	primary = -1
	for idx := int32(0); idx < 8; idx++ {
		s.adminMu.Lock()
		reply, perr := s.runLocalAdmin(ctx, AdminGetChannel(uint32(idx)), "local-probe-channel")
		s.adminMu.Unlock()
		if perr != nil {
			continue
		}
		ch, cerr := extractChannel(reply)
		if cerr != nil {
			continue
		}
		switch ch.GetRole() {
		case pb.Channel_PRIMARY:
			primary = idx
		case pb.Channel_DISABLED:
			empty[idx] = true
		}
	}
	if primary < 0 {
		return empty, primary, fmt.Errorf("no PRIMARY channel found on local Heltec")
	}
	return empty, primary, nil
}

// remoteSlotIsDisabled probes a single slot on one remote via PKC and
// returns true iff the slot reports role=DISABLED. Used by the staging
// picker to verify a candidate slot per-remote without walking all 8
// slots (the bulk-probe was the dominant cost in early rotations,
// pushing pre-flight wall to ~80s for a 2-remote fleet). A probe error
// is treated as "remote unreachable" and reported via the bool/err
// pair so the picker can exclude that remote from consensus rather
// than abort.
func (s *Service) remoteSlotIsDisabled(ctx context.Context, nodeNum uint32, idx int32) (disabled bool, reachable bool, err error) {
	s.adminMu.Lock()
	reply, perr := s.runRemoteAdmin(ctx, nodeNum, AdminGetChannel(uint32(idx)), "remote-probe-channel")
	s.adminMu.Unlock()
	if perr != nil {
		return false, false, perr
	}
	ch, cerr := extractChannel(reply)
	if cerr != nil {
		return false, true, cerr
	}
	return ch.GetRole() == pb.Channel_DISABLED, true, nil
}

// chooseStagingSlot picks a slot index that's DISABLED on the local
// Heltec AND DISABLED on every reachable remote target.
//
// Cost-aware probing: walks candidates from the local probe (8 fast
// local admin calls) and only probes each REMOTE for the SPECIFIC
// candidate, not all 8 slots. On a healthy fleet that's been Phase-0
// reconciled, the first candidate (lowest empty != local PRIMARY) is
// usable and we issue exactly len(remotes) PKC admin calls. Worst
// case (every candidate collides with some remote) degrades to 8 *
// len(remotes) -- same as the old bulk-probe but only when actually
// needed.
//
// Reachable-remote skew detection: if a candidate slot is reported as
// non-DISABLED on a remote, that slot is rejected and we walk to the
// next candidate. Result: rotation lands on a slot that's safe
// everywhere; post-Phase-D the whole fleet sits on that slot.
//
// Unreachable remotes don't gate slot selection -- they're excluded
// from the per-candidate probe and will fail Phase B normally with a
// "remote unreachable" error, leaving the rest of the fleet to rotate
// successfully.
func (s *Service) chooseStagingSlot(ctx context.Context, channelIndex int32, remoteTargets []uint32) (int32, error) {
	localEmpty, localPrimary, err := s.probeSlotsLocal(ctx)
	if err != nil {
		return 0, fmt.Errorf("local slot probe: %w", err)
	}
	if len(localEmpty) == 0 {
		return 0, fmt.Errorf("all 8 channel slots in use on local Heltec; cannot stage new PSK -- retire an old slot first")
	}
	for idx := int32(0); idx < 8; idx++ {
		if idx == localPrimary {
			continue
		}
		if !localEmpty[idx] {
			continue
		}
		ok := true
		for _, n := range remoteTargets {
			disabled, reachable, perr := s.remoteSlotIsDisabled(ctx, n, idx)
			if !reachable {
				slog.Info("staging-slot probe: remote unreachable, excluded from consensus",
					"node_num", n, "candidate_slot", idx, "error", perr)
				continue
			}
			if !disabled {
				ok = false
				break
			}
		}
		if ok {
			return idx, nil
		}
	}
	return 0, fmt.Errorf("no slot is DISABLED across local Heltec + %d remote target(s); fleet slot layout is too skewed for automated picking -- compact remote slots via USB", len(remoteTargets))
}

// readLocalChannel reads slot idx on the local Heltec via local admin.
// Returns the full Channel proto (role + settings.psk + settings.name).
func (s *Service) readLocalChannel(ctx context.Context, idx int32) (*pb.Channel, error) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	reply, err := s.runLocalAdmin(ctx, AdminGetChannel(uint32(idx)), "local-read-channel")
	if err != nil {
		return nil, err
	}
	return extractChannel(reply)
}

// readRemoteChannel reads slot idx on the named remote via PKC.
func (s *Service) readRemoteChannel(ctx context.Context, nodeNum uint32, idx int32) (*pb.Channel, error) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	reply, err := s.runRemoteAdmin(ctx, nodeNum, AdminGetChannel(uint32(idx)), "remote-read-channel")
	if err != nil {
		return nil, err
	}
	return extractChannel(reply)
}

// channelStateMatches returns true when two Channel rows represent the
// same role + PSK material. Name + uplink/downlink flags are not
// reconciled by Phase 0 -- those are operator-owned cosmetics.
func channelStateMatches(a, b *pb.Channel) bool {
	if a.GetRole() != b.GetRole() {
		return false
	}
	var pskA, pskB []byte
	if a.GetSettings() != nil {
		pskA = a.GetSettings().GetPsk()
	}
	if b.GetSettings() != nil {
		pskB = b.GetSettings().GetPsk()
	}
	return bytes.Equal(pskA, pskB)
}

// reconcileRemoteSlots is Phase 0: it makes a remote's slot 0 and slot
// 1 match the Pi's slot 0 and slot 1. The convention "diginode-cc owns
// slots 0 and 1" means these slots ARE the staged-rotation working
// area; if a remote drifted out of lockstep (partial-failure carryover,
// USB-side intervention, missed retire), Phase 0 brings it back into
// canonical alignment before the rotation runner picks a staging slot.
//
// Slots 2-7 are operator-owned and NEVER touched -- the remote can run
// any ham/test/named channels there without interference.
//
// Order: write whichever slot Pi has as PRIMARY first. Firmware
// auto-demotes the previous PRIMARY when a new PRIMARY is set, so this
// avoids a transient no-PRIMARY window on the remote (which would
// brick its radio mid-reconcile). Then write the other slot to its
// canonical state (typically DISABLED post-retire).
//
// Returns nil if remote already matched (no writes issued). Returns
// error on unreachable remote -- caller logs and proceeds; that
// remote will surface as failed_b in the normal rotation flow.
func (s *Service) reconcileRemoteSlots(ctx context.Context, nodeNum uint32) error {
	piSlot0, err := s.readLocalChannel(ctx, 0)
	if err != nil {
		return fmt.Errorf("read local slot 0: %w", err)
	}
	piSlot1, err := s.readLocalChannel(ctx, 1)
	if err != nil {
		return fmt.Errorf("read local slot 1: %w", err)
	}
	remoteSlot0, err := s.readRemoteChannel(ctx, nodeNum, 0)
	if err != nil {
		return fmt.Errorf("read remote slot 0: %w", err)
	}
	remoteSlot1, err := s.readRemoteChannel(ctx, nodeNum, 1)
	if err != nil {
		return fmt.Errorf("read remote slot 1: %w", err)
	}

	piSlots := [2]*pb.Channel{piSlot0, piSlot1}
	remoteSlots := [2]*pb.Channel{remoteSlot0, remoteSlot1}

	// Order: PRIMARY-first when Pi has PRIMARY at slot 1, otherwise
	// natural slot-0-first order. This sequencing ensures the remote
	// always has at least one PRIMARY slot mid-reconcile -- never goes
	// dark.
	order := []int32{0, 1}
	if piSlots[1].GetRole() == pb.Channel_PRIMARY && piSlots[0].GetRole() != pb.Channel_PRIMARY {
		order = []int32{1, 0}
	}
	wrote := 0
	for _, idx := range order {
		pi := piSlots[idx]
		rem := remoteSlots[idx]
		if channelStateMatches(pi, rem) {
			continue
		}
		var psk []byte
		if pi.GetSettings() != nil {
			psk = pi.GetSettings().GetPsk()
		}
		msg := AdminSetChannel(idx, "", pi.GetRole(), psk)
		s.adminMu.Lock()
		_, err := s.runRemoteAdmin(ctx, nodeNum, msg, fmt.Sprintf("phase0-mirror-slot%d", idx))
		s.adminMu.Unlock()
		if err != nil {
			return fmt.Errorf("mirror slot %d (role=%s): %w", idx, pi.GetRole().String(), err)
		}
		wrote++
	}
	if wrote > 0 {
		slog.Info("phase 0: reconciled remote slots to pi state",
			"node_num", nodeNum, "writes", wrote)
	}
	return nil
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
//
// Cross-fleet safety: AdminGetChannel(stagingIdx) is issued FIRST. If
// the slot already holds a PRIMARY/SECONDARY role on the remote, abort
// without writing -- proceeding would overwrite an active channel and
// leave the remote with a broken slot layout (worst case: no PRIMARY =
// no radio = stranded). This guards against the slot-skew class of
// bug where the Pi-local picker chose a slot index that's empty on Pi
// but live on a remote (out-of-lockstep state from a prior partial
// rotation or manual USB intervention). The probe doubles as the
// session_passkey establishment that the SetChannel call below needs.
func (s *Service) pushStagingToRemote(ctx context.Context, nodeNum uint32, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	probeReply, err := s.runRemoteAdmin(ctx, nodeNum, AdminGetChannel(uint32(stagingIdx)), "remote-probe-staging-slot")
	if err != nil {
		return fmt.Errorf("session establish: %w", err)
	}
	if ch, perr := extractChannel(probeReply); perr == nil && ch.GetRole() != pb.Channel_DISABLED {
		return fmt.Errorf("staging slot %d already in use on remote (role=%s) -- refusing to overwrite; reset the remote slot via USB or pick a different staging index", stagingIdx, ch.GetRole().String())
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

	// Build remote-target list once for Phase 0 + Phase A.
	remoteNodeNums := make([]uint32, 0, len(targets))
	for _, t := range targets {
		if t.NodeNum != localNum {
			remoteNodeNums = append(remoteNodeNums, t.NodeNum)
		}
	}

	// Phase 0: reconcile each reachable remote's slots 0/1 to mirror
	// Pi's slots 0/1 (Pi is the source of truth). Brings out-of-lockstep
	// remotes back to canonical alignment so the cross-fleet picker
	// below has a clean fleet to choose from. Unreachable remotes are
	// logged + skipped -- they'll fail Phase B with a "remote
	// unreachable" error in the normal flow.
	for _, n := range remoteNodeNums {
		if rErr := s.reconcileRemoteSlots(ctx, n); rErr != nil {
			slog.Warn("phase 0: reconcile remote slots failed -- proceeding without alignment",
				"rotation_id", rotID, "node_num", n, "error", rErr)
		}
	}

	// Phase A: pick a staging slot. On retry the staging slot was
	// already pinned on the rotation row by the original run -- reuse
	// it so the failed target lands on the SAME physical slot as the
	// rest of the fleet (otherwise the cross-fleet picker would walk
	// past Pi's now-occupied previous staging slot and land the
	// retried target on a different slot, breaking fleet-wide slot
	// alignment).
	//
	// Fresh rotation: probe Pi-local + every reachable remote target.
	// Post-Phase-0 the fleet should be aligned so the picker picks
	// deterministically; the cross-probe is belt-and-suspenders
	// against any remote that didn't reconcile (e.g., admin-key drift,
	// PKC failure).
	var stagingIdx int32
	if rec, gErr := s.store.GetRotation(ctx, rotID); gErr == nil && rec.StagingChannelIndex != nil {
		stagingIdx = *rec.StagingChannelIndex
		slog.Info("reusing pinned staging slot from rotation row",
			"rotation_id", rotID, "staging_idx", stagingIdx)
	} else {
		var sErr error
		stagingIdx, sErr = s.chooseStagingSlot(ctx, channelIndex, remoteNodeNums)
		if sErr != nil {
			slog.Error("choose staging slot",
				"rotation_id", rotID, "error", sErr)
			return
		}
		if err := s.store.SetStagingChannelIndex(ctx, rotID, stagingIdx); err != nil {
			slog.Warn("persist staging_channel_index",
				"rotation_id", rotID, "error", err)
		}
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
	// A retry that resumes from has_new_psk (target was failed_c) skips
	// this loop -- staging is already on the remote, only the promote
	// step needs replay.
	for i, t := range remoteTargets {
		// Targets already past Phase B keep their phase. Phase C loop
		// picks them up.
		if t.Phase == PhaseHasNewPSK || t.Phase == PhasePromotingC ||
			t.Phase == PhaseOnNewPSK || t.Phase == PhaseFailedC ||
			t.Phase == PhaseRetired {
			continue
		}
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
		// Skip remotes that failed Phase B (no staging on them yet)
		// or that already finished Phase C in a previous run.
		if t.Phase == PhaseFailedB || t.Phase == PhaseOnNewPSK || t.Phase == PhaseRetired {
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

	// Update the channel snapshot with the new PSK fingerprint FIRST --
	// the trust-refresh loop below calls GetTrust, which stamps each
	// remote's current_psk_fp from localPrimaryPSKFP(), which reads from
	// this row. If we updated the snapshot after the refresh loop, every
	// node would get stamped with the OLD fingerprint and the retirement
	// gate would stay closed forever.
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

// RetireOldPSKResult is what the retirement endpoint returns. On a
// gate-failure (some node not yet on the new PSK) the API returns 409
// with this struct populated -- Laggards lists the node-nums still on a
// stale fingerprint so the operator can run Retry / Verify on each.
// On success Laggards is empty and OldChannelIndex is the slot we just
// disabled (typically the old PRIMARY index).
type RetireOldPSKResult struct {
	OK              bool     `json:"ok"`
	Laggards        []uint32 `json:"laggards,omitempty"`
	OldChannelIndex int32    `json:"oldChannelIndex,omitempty"`
	NewPSKFP        string   `json:"newPskFingerprint,omitempty"`
}

// RetireOldPSK runs Phase E for a completed staged rotation. Gated on
// every managed-trust row's current_psk_fp matching the rotation's
// new PSK fingerprint -- a node that hasn't been Verified on the new
// PSK keeps the gate closed even if it would have decrypted just fine,
// because the operator hasn't proven it.
//
// On success: sends SetChannel(idx=oldPrimary, role=DISABLED) locally
// AND to each fleet member that's reachable. Stamps the rotation as
// retired. The OLD slot's PSK is wiped at the firmware level.
//
// Why not auto-broadcast retirement to remotes? Each remote already
// has its old slot demoted to SECONDARY (firmware auto-demotion in
// Phase C); leaving it in place is harmless until we explicitly
// disable. Broadcasting retirement is a courtesy that frees the slot
// on remotes too -- but a remote that's offline at retirement time
// stays SECONDARY-mapped until it's individually retired later.
func (s *Service) RetireOldPSK(ctx context.Context, userID, rotID string) (*RetireOldPSKResult, error) {
	rec, err := s.store.GetRotation(ctx, rotID)
	if err != nil {
		return nil, err
	}
	if rec.Kind != RotationKindPSK {
		return nil, fmt.Errorf("rotation %s is not a PSK rotation", rotID)
	}

	// Detach from the caller's deadline so the per-remote PKC round
	// (DefaultRemoteAdminTimeout = 30s, GetConfig + SetChannel each)
	// can finish even when the inbound HTTP request times out. A
	// 2-remote fleet needs ~2*60s wall worst-case; without this
	// detach the second remote's first transaction starts on an
	// already-expired context and aborts immediately. Audit + DB row
	// state still record the outcome, so the UI can re-trigger Retire
	// if the operator gives up waiting.
	deadline := 2*time.Minute + time.Duration(2*len(rec.Targets))*DefaultRemoteAdminTimeout
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deadline)
	defer cancel()
	ctx = bgCtx
	if rec.PiLocalPhase != PiPhasePhaseDPromoted {
		return nil, fmt.Errorf("rotation not ready to retire (pi_local_phase=%s, want phase_d_promoted)", rec.PiLocalPhase)
	}
	if rec.RetiredAt != nil {
		return nil, errors.New("rotation already retired")
	}
	if rec.ChannelIndex == nil {
		return nil, errors.New("rotation has no channel_index")
	}
	newPSKFP := rec.NewPSKFP

	// Probe Pi-local for the SECONDARY slot that currently holds the
	// OLD PSK. After Phase D the firmware auto-demoted the previous
	// PRIMARY to SECONDARY at its prior slot index; that slot is what
	// Phase E targets, NOT the rotation row's logical channel_index.
	// If the rotation went through a Retry the staging slot (and thus
	// the new PRIMARY slot post-Phase-D) may differ from
	// rec.ChannelIndex, leaving rec.ChannelIndex pointing at the LIVE
	// PRIMARY slot. Disabling that would brick the local Heltec.
	oldChannelIdx := int32(-1)
	for idx := int32(0); idx < 8; idx++ {
		ch, perr := s.readLocalChannel(ctx, idx)
		if perr != nil {
			continue
		}
		if ch.GetRole() != pb.Channel_SECONDARY {
			continue
		}
		var psk []byte
		if ch.GetSettings() != nil {
			psk = ch.GetSettings().GetPsk()
		}
		if Fingerprint(psk) != newPSKFP {
			oldChannelIdx = idx
			break
		}
	}
	if oldChannelIdx < 0 {
		// Nothing matches the OLD PSK pattern. Fall back to the rotation
		// row's logical channel_index for backward compatibility, but
		// log the unexpected case.
		oldChannelIdx = *rec.ChannelIndex
		slog.Info("retire: no SECONDARY slot with non-new PSK found; falling back to rotation channel_index",
			"rotation_id", rotID, "channel_index", oldChannelIdx)
	}

	// Gate: every managed fleet member must show current_psk_fp == newPSKFP.
	allMigrated, laggards, err := s.store.AllManagedNodesOnPSK(ctx, newPSKFP)
	if err != nil {
		return nil, err
	}
	if !allMigrated {
		s.auditFleet(ctx, userID, "rotate_psk_retire_blocked", "channel",
			fmt.Sprintf("%d", oldChannelIdx), map[string]any{
				"rotation_id": rotID,
				"laggards":    laggards,
				"new_psk_fp":  newPSKFP,
			})
		return &RetireOldPSKResult{OK: false, Laggards: laggards, OldChannelIndex: oldChannelIdx, NewPSKFP: newPSKFP}, nil
	}

	// Locally disable the old slot. SetChannel role=DISABLED on the slot
	// that previously held the old PRIMARY -- the firmware wipes the
	// PSK material from the slot.
	s.adminMu.Lock()
	disableMsg := AdminSetChannel(oldChannelIdx, "", pb.Channel_DISABLED, nil)
	if _, err := s.runLocalAdmin(ctx, disableMsg, "local-retire-old-psk"); err != nil {
		s.adminMu.Unlock()
		return nil, fmt.Errorf("disable local old slot: %w", err)
	}
	s.adminMu.Unlock()

	// Best-effort broadcast to each remote that was managed by this
	// rotation. A remote that's offline now stays SECONDARY-mapped on
	// the old slot; harmless. Operator can re-trigger retirement later
	// to push to it once it's back -- the gate stays open as long as
	// every laggard is on the new PSK.
	for _, t := range rec.Targets {
		if t.NodeNum == s.localNode.LocalNodeNum() {
			continue
		}
		s.adminMu.Lock()
		// Refresh session passkey via Get; remote may have rebooted.
		if _, gErr := s.runRemoteAdmin(ctx, t.NodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-retire-establish-session"); gErr != nil {
			s.adminMu.Unlock()
			slog.Warn("retire: remote unreachable",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", gErr)
			continue
		}
		if _, dErr := s.runRemoteAdmin(ctx, t.NodeNum, AdminSetChannel(oldChannelIdx, "", pb.Channel_DISABLED, nil), "remote-retire-old-psk"); dErr != nil {
			slog.Warn("retire: remote disable failed",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", dErr)
		}
		s.adminMu.Unlock()
	}

	if err := s.store.MarkRotationRetired(ctx, rotID); err != nil {
		return nil, err
	}

	s.auditFleet(ctx, userID, "rotate_psk_retire", "channel",
		fmt.Sprintf("%d", oldChannelIdx), map[string]any{
			"rotation_id":       rotID,
			"old_channel_index": oldChannelIdx,
			"new_psk_fp":        newPSKFP,
		})

	return &RetireOldPSKResult{OK: true, OldChannelIndex: oldChannelIdx, NewPSKFP: newPSKFP}, nil
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

	// Phase-aware retry: resume each requested target from the right
	// resting state so the worker doesn't redo work that already
	// succeeded.
	//
	//   failed_b      -> reset to pending; worker runs Phase B + C.
	//   failed_c      -> reset to has_new_psk; worker skips Phase B
	//                    and runs only Phase C.
	//   phase_b_*     -> mid-flight from a crashed worker; reset to
	//                    pending.
	//   phase_c_*     -> mid-flight; staging already on the remote
	//                    from the prior Phase B success; reset to
	//                    has_new_psk so we don't re-push.
	//   on_new_psk /  -> already done; skip.
	//   pending       -> hadn't started; worker picks it up naturally.
	want := map[uint32]bool{}
	for _, t := range targetNodeNums {
		want[t] = true
	}
	current := append([]RotationTarget(nil), rec.Targets...)
	any := false
	for i := range current {
		if !want[current[i].NodeNum] {
			continue
		}
		switch current[i].Phase {
		case PhaseFailedB, PhasePushingB:
			current[i].Phase = PhasePending
			current[i].Status = statusForPhase(PhasePending)
			current[i].LastError = ""
			any = true
		case PhaseFailedC, PhasePromotingC:
			current[i].Phase = PhaseHasNewPSK
			current[i].Status = statusForPhase(PhaseHasNewPSK)
			current[i].LastError = ""
			any = true
		case PhasePending:
			// Already pending -- worker will pick it up; counts as
			// a retry request even though no reset was needed.
			any = true
		case PhaseOnNewPSK, PhaseRetired:
			// Already done; nothing to retry. Skip silently.
			continue
		case PhaseHasNewPSK:
			// In an unusual mid-rotation snapshot: B succeeded but
			// the worker hadn't reached this target's C yet. Worker
			// will pick it up.
			any = true
		}
	}
	if !any {
		return errors.New("no eligible targets matched the retry list (already done or unknown node-num)")
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
