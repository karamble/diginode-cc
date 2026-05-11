package fleetsec

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/karamble/diginode-cc/internal/fleetsec/jobs"
	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Commit verify-via-probe timing knobs. Vars (not consts) so tests can
// shrink them. See migrateRemoteAtomic for the rationale.
var (
	commitVerifyInitialWait = 12 * time.Second
	commitVerifyDeadline    = 60 * time.Second
	commitVerifyBackoff     = 8 * time.Second
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
	// Notice is a short human-readable status line emitted at key
	// rotation milestones ("Phase 0 reconcile on HB55", "Picked
	// staging slot 1", etc). The drawer's status rail shows the
	// most recent notice so operators see what the worker is doing
	// without tailing logs. Empty when this event is a pure
	// target-state delta with no narrative to add.
	Notice string `json:"notice,omitempty"`
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

// ReadLocalChannel is the public wrapper around readLocalChannel that
// the API layer calls into. Returns the full Channel proto so the
// handler can inspect Role + Settings.Psk + Settings.Name without
// reaching into package internals.
func (s *Service) ReadLocalChannel(ctx context.Context, idx int32) (*pb.Channel, error) {
	return s.readLocalChannel(ctx, idx)
}

// BuildChannelSetURL probes slots 0..7 on the local Heltec and returns
// a meshtastic://-style channel URL that encodes every non-DISABLED
// channel's settings. The returned URL is the same format meshtastic
// CLI's --seturl consumes and the phone app scans from a QR -- the
// meshtastic.org/e/ prefix is decorative, the receiver parses the
// fragment locally without any network round-trip.
//
// See encodeChannelSetURL for the wire format. LoRaConfig (ChannelSet
// field 2) is omitted: the receiving node keeps its own LoRa config
// (region, bandwidth, etc.) and only learns the channel set from this
// URL. The flash scripts that consume this URL configure lora.region
// separately.
func (s *Service) BuildChannelSetURL(ctx context.Context) (string, error) {
	var settings []*pb.ChannelSettings
	for idx := int32(0); idx < 8; idx++ {
		ch, err := s.readLocalChannel(ctx, idx)
		if err != nil {
			// Missing/unreadable slot: skip. Matches probeSlotsLocal's
			// per-slot tolerance -- the firmware returns an error for
			// slots that have never been written.
			continue
		}
		if ch.GetRole() == pb.Channel_DISABLED {
			continue
		}
		if cs := ch.GetSettings(); cs != nil {
			settings = append(settings, cs)
		}
	}
	return encodeChannelSetURL(settings)
}

// encodeChannelSetURL serialises a ChannelSet protobuf containing the
// supplied ChannelSettings entries and wraps the result in the standard
// meshtastic.org/e/ URL form.
//
// Wire format (one of these per entry, concatenated):
//
//	0x0A                       // ChannelSet field 1 (settings), wire type 2 (length-delimited)
//	<varint(len(marshalled))>  // ChannelSettings size
//	<marshalled bytes>         // proto.Marshal(*pb.ChannelSettings)
//
// Pure function -- separated from BuildChannelSetURL so the encoding
// can be round-trip tested without a serial-port stub.
func encodeChannelSetURL(settings []*pb.ChannelSettings) (string, error) {
	if len(settings) == 0 {
		return "", errors.New("no enabled channels found on local Heltec")
	}
	var body []byte
	for i, cs := range settings {
		payload, err := proto.Marshal(cs)
		if err != nil {
			return "", fmt.Errorf("marshal channel %d settings: %w", i, err)
		}
		body = append(body, 0x0A)
		var lenBuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenBuf[:], uint64(len(payload)))
		body = append(body, lenBuf[:n]...)
		body = append(body, payload...)
	}
	return "https://meshtastic.org/e/#" + base64.RawURLEncoding.EncodeToString(body), nil
}

// AuditReveal writes a fleetsec.reveal_psk audit row. The detail map
// deliberately carries no PSK material -- auditFleet's redactSecrets
// layer would catch a key named "psk" but we avoid passing it through
// at all. Called from the reveal-PSK API handler.
func (s *Service) AuditReveal(ctx context.Context, userID string, channelIndex int32) {
	s.auditFleet(ctx, userID, "reveal_psk", "channel",
		fmt.Sprintf("%d", channelIndex), map[string]any{
			"channel_url_included": true,
		})
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
	// Pre-flight: refuse to stage a PSK whose 1-byte channel hash
	// collides with the active PRIMARY or any recovery cache slot.
	// Firmware's lowest-slot-wins decryption would silently drop
	// traffic on the colliding channel. Random-source callers
	// (handler) regenerate via GenerateRandomPSKAvoidCollision so
	// in practice this only triggers for explicit operator-supplied
	// PSKs that happen to collide.
	if cErr := s.CheckPSKHashCollision(ctx, newPSK); cErr != nil {
		return "", cErr
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

	// In-memory copy is no longer needed; PSK plaintext lives on the
	// rotation row (InsertRotation stashed it) for handlers to pull.
	for i := range pskCopy {
		pskCopy[i] = 0
	}

	// Enqueue Phase A. The handler probes Pi, picks the staging slot,
	// stages the new PSK, then enqueues per-remote Phase B jobs. The
	// HTTP caller returns immediately; the worker drives the rotation
	// out of band so a 30-node fleet doesn't time out the request.
	if s.jobs == nil {
		return "", errors.New("fleet-security jobs queue not wired (svc.SetJobsStore not called)")
	}
	if _, err := s.jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:       jobs.KindRotatePhaseA,
		RotationID: &rotID,
		Payload:    PhaseAPayload{},
	}); err != nil {
		return rotID, fmt.Errorf("enqueue phase A: %w", err)
	}

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

// applyLocalStagingChannel runs Phase A: write the new PSK into a
// SECONDARY-role channel slot on the local Heltec. After this returns
// the Pi can decrypt traffic on EITHER the existing PRIMARY (old PSK)
// or the staging slot (new PSK), so per-target acks during Phase B
// land on the unchanged PRIMARY and decode cleanly.
//
// Local admin path -- no PSK gap, no session_passkey enforcement, just
// a SetChannel that the firmware applies live (channel admin is the
// one set_* verb that doesn't trigger a reboot post-save).
func (s *Service) applyLocalStagingChannel(ctx context.Context, stagingIdx int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	msg := AdminSetChannel(stagingIdx, "", pb.Channel_SECONDARY, newPSK)
	if _, err := s.runLocalAdmin(ctx, msg, "local-stage-secondary"); err != nil {
		return fmt.Errorf("apply staging channel locally: %w", err)
	}
	return nil
}

// migrateRemoteAtomic walks one managed remote from "old PRIMARY at
// oldSlot" to "new PRIMARY at stagingIdx, oldSlot wiped" inside a
// single atomic admin transaction:
// begin_edit_settings -> SetChannel(stagingIdx, PRIMARY, newPSK) ->
// SetChannel(oldSlot, DISABLED, empty) -> commit_edit_settings.
//
// DISABLE-with-empty-psk wipes residual PSK material; SetChannel with
// role=DISABLED alone leaves the bytes in place (firmware quirk).
//
// CRITICAL: no read admin frames between begin and commit. Any non-
// set admin verb received inside the transaction discards the
// pending writes firmware-side.
//
// Caller must have staged the new PSK on Pi as SECONDARY at
// stagingIdx (Phase A) so Pi can decode replies from the post-commit
// remote.
func (s *Service) migrateRemoteAtomic(ctx context.Context, nodeNum uint32, stagingIdx, oldSlot int32, newPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	// AdminModule rejects state-changing verbs without a valid
	// session_passkey; AdminGetChannel(0) returns one. Long timeout
	// because the establish frame can wait 20-30s in the TX queue
	// under EU 868 duty-cycle throttling.
	if _, err := s.runRemoteAdminLong(ctx, nodeNum, AdminGetChannel(0), "remote-establish-session"); err != nil {
		return fmt.Errorf("session establish: %w", err)
	}
	// begin + 2x SetChannel + commit are all fire-and-forget. The PKI
	// commit-style routing ack is dropped firmware-side (AdminModule
	// flags the auto-generated ROUTING_APP reply pki_encrypted=true,
	// but Router refuses PKC on ROUTING_APP and aborts encoding).
	// Verification comes from the post-commit get_channel probe below,
	// whose reply rides ADMIN_APP and IS allowed under PKC.
	if err := s.fireAndForgetRemoteAdmin(nodeNum, AdminBeginEditSettings()); err != nil {
		return fmt.Errorf("queue begin edit: %w", err)
	}
	promote := AdminSetChannel(stagingIdx, "", pb.Channel_PRIMARY, newPSK)
	if err := s.fireAndForgetRemoteAdmin(nodeNum, promote); err != nil {
		return fmt.Errorf("queue set primary: %w", err)
	}
	disable := AdminSetChannel(oldSlot, "", pb.Channel_DISABLED, nil)
	if err := s.fireAndForgetRemoteAdmin(nodeNum, disable); err != nil {
		return fmt.Errorf("queue disable old: %w", err)
	}
	if err := s.fireAndForgetRemoteAdmin(nodeNum, AdminCommitEditSettings()); err != nil {
		return fmt.Errorf("queue commit edit: %w", err)
	}
	// Hold for the flash write to complete before the first probe.
	// Commit-to-applied is ~3-5s on USB; PKC mesh adds 5-15s for the
	// commit frame to leave Pi's TX queue under EU 868 duty cycle.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(commitVerifyInitialWait):
	}
	// Verify by reading the staging slot back over PKC. get_channel
	// returns get_channel_response on ADMIN_APP which IS allowed under
	// PKC, so the reply path works. PRIMARY role + matching PSK
	// fingerprint = transaction applied. Anything else after the
	// retry window = real failure.
	pskFP := Fingerprint(newPSK)
	verifyDeadline := time.Now().Add(commitVerifyDeadline)
	verifyBackoff := commitVerifyBackoff
	var lastProbeErr error
	for time.Now().Before(verifyDeadline) {
		probeReply, perr := s.runRemoteAdmin(ctx, nodeNum, AdminGetChannel(uint32(stagingIdx)), "remote-commit-verify")
		if perr == nil {
			ch, cerr := extractChannel(probeReply)
			if cerr == nil && ch.GetRole() == pb.Channel_PRIMARY {
				var psk []byte
				if ch.GetSettings() != nil {
					psk = ch.GetSettings().GetPsk()
				}
				if Fingerprint(psk) == pskFP {
					slog.Info("remote commit verified via probe",
						"node_num", nodeNum, "staging_idx", stagingIdx)
					return nil
				}
				lastProbeErr = fmt.Errorf("staging slot is PRIMARY but PSK fp mismatch (got %s, want %s)", Fingerprint(psk), pskFP)
			} else if cerr != nil {
				lastProbeErr = cerr
			} else {
				lastProbeErr = fmt.Errorf("staging slot role is %v, expected PRIMARY", ch.GetRole())
			}
		} else {
			lastProbeErr = perr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(verifyBackoff):
		}
	}
	if lastProbeErr == nil {
		lastProbeErr = errors.New("verify deadline reached with no successful probe")
	}
	return fmt.Errorf("commit verify: %w", lastProbeErr)
}

// migratePiAtomic is the local equivalent of migrateRemoteAtomic. Runs
// the same atomic transaction on the local Heltec to promote
// stagingIdx to PRIMARY (auto-demoting old PRIMARY to SECONDARY) and
// then DISABLE-wipe the old slot in the same flash write. After this
// returns the Pi-Heltec is on the new PSK only.
//
// Operator-paced Phase C runner: fires after all reachable managed
// remotes complete Phase B and the operator clicks Retire. Local-admin
// path still uses session_passkey (AdminModule enforces on local admin).
func (s *Service) migratePiAtomic(ctx context.Context, stagingIdx, oldSlot int32, newPSK []byte) error {
	return s.migratePiAtomicWithRecovery(ctx, stagingIdx, oldSlot, newPSK, -1, nil)
}

// migratePiAtomicWithRecovery folds the recovery-cache slot write into
// the same atomic transaction as the migrate. Two consecutive flash
// writes on some firmware versions trigger a soft reboot mid-write
// that leaves the radio unresponsive for ~5 minutes. One commit = one
// flash write. Pass recoverySlot=-1 + recoveryPSK=nil to skip the
// recovery write entirely.
func (s *Service) migratePiAtomicWithRecovery(ctx context.Context, stagingIdx, oldSlot int32, newPSK []byte, recoverySlot int32, recoveryPSK []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if _, err := s.runLocalAdmin(ctx, AdminBeginEditSettings(), "local-begin-edit"); err != nil {
		return fmt.Errorf("begin edit: %w", err)
	}
	promote := AdminSetChannel(stagingIdx, "", pb.Channel_PRIMARY, newPSK)
	if _, err := s.runLocalAdmin(ctx, promote, "local-set-primary"); err != nil {
		return fmt.Errorf("set primary: %w", err)
	}
	disable := AdminSetChannel(oldSlot, "", pb.Channel_DISABLED, nil)
	if _, err := s.runLocalAdmin(ctx, disable, "local-disable-old"); err != nil {
		return fmt.Errorf("disable old: %w", err)
	}
	if recoverySlot >= 0 && len(recoveryPSK) > 0 {
		recovery := AdminSetChannel(recoverySlot, "", pb.Channel_SECONDARY, recoveryPSK)
		if _, err := s.runLocalAdmin(ctx, recovery, "local-set-recovery"); err != nil {
			return fmt.Errorf("set recovery: %w", err)
		}
	}
	if _, err := s.runLocalAdmin(ctx, AdminCommitEditSettings(), "local-commit-edit"); err != nil {
		return fmt.Errorf("commit edit: %w", err)
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
	// Already-retired check FIRST -- both pi_local_phase=retired and
	// retired_at are set by MarkRotationRetired, and a stale UI tab
	// re-clicking Retire would otherwise hit the phase check below
	// and surface a misleading "want phase_d_promoted" error.
	if rec.RetiredAt != nil || rec.PiLocalPhase == PiPhaseRetired {
		return nil, errors.New("rotation already retired")
	}
	// pi_local_phase=staging_added is the "ready for Phase C" signal:
	// Phase A staged the new PSK as SECONDARY on Pi and the operator
	// clicked Retire to trigger Phase C atomically. phase_d_promoted
	// is accepted for in-flight rows from before the 3-phase rewrite.
	if rec.PiLocalPhase != PiPhaseStagingAdded && rec.PiLocalPhase != PiPhasePhaseDPromoted {
		return nil, fmt.Errorf("rotation not ready to retire (pi_local_phase=%s, want staging_added or phase_d_promoted)", rec.PiLocalPhase)
	}

	if rec.ChannelIndex == nil {
		return nil, errors.New("rotation has no channel_index")
	}
	newPSKFP := rec.NewPSKFP

	// After Phase B has migrated every reachable remote, Pi is the only
	// fleet member still on the OLD PSK as PRIMARY. The endpoint enqueues
	// the Phase C job for the worker to run out of band.
	if rec.StagingChannelIndex == nil {
		return nil, errors.New("rotation has no staging_channel_index (rotation may pre-date the atomic worker)")
	}
	stagingIdx := *rec.StagingChannelIndex

	// Gate: every managed fleet member must show current_psk_fp ==
	// newPSKFP. Operator must Verify any laggards (or use Retry)
	// before Pi can promote. Run gate before enqueueing so the UI
	// can surface the laggard list inline.
	allMigrated, laggards, err := s.store.AllManagedNodesOnPSK(ctx, newPSKFP)
	if err != nil {
		return nil, err
	}
	if !allMigrated {
		s.auditFleet(ctx, userID, "rotate_psk_retire_blocked", "channel",
			fmt.Sprintf("%d", stagingIdx), map[string]any{
				"rotation_id": rotID,
				"laggards":    laggards,
				"new_psk_fp":  newPSKFP,
			})
		return &RetireOldPSKResult{OK: false, Laggards: laggards, OldChannelIndex: stagingIdx, NewPSKFP: newPSKFP}, nil
	}

	if s.jobs == nil {
		return nil, errors.New("fleet-security jobs queue not wired (svc.SetJobsStore not called)")
	}

	// Debounce: if a Phase C job is already pending or in progress for
	// this rotation, reuse it instead of enqueueing a duplicate.
	existing, lerr := s.jobs.ListByRotation(ctx, rotID)
	if lerr == nil {
		for _, j := range existing {
			if j.Kind == jobs.KindRotatePhaseC && (j.State == jobs.StateQueued || j.State == jobs.StateInProgress) {
				slog.Info("retire: Phase C already queued; reusing",
					"rotation_id", rotID, "job_id", j.ID)
				return &RetireOldPSKResult{OK: true, OldChannelIndex: stagingIdx, NewPSKFP: newPSKFP}, nil
			}
		}
	}

	if _, err := s.jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:       jobs.KindRotatePhaseC,
		RotationID: &rotID,
		Payload:    PhaseCPayload{StagingIdx: stagingIdx},
	}); err != nil {
		return nil, fmt.Errorf("enqueue phase C: %w", err)
	}

	s.auditFleet(ctx, userID, "rotate_psk_retire", "channel",
		fmt.Sprintf("%d", stagingIdx), map[string]any{
			"rotation_id":  rotID,
			"staging_slot": stagingIdx,
			"new_psk_fp":   newPSKFP,
		})

	return &RetireOldPSKResult{OK: true, OldChannelIndex: stagingIdx, NewPSKFP: newPSKFP}, nil
}

// broadcastNotice emits a status-line WS event without touching the
// DB. The drawer's status rail picks these up and shows what the
// worker is doing in real time ("Phase 0 reconcile on HB55", "Picked
// staging slot 1", etc). Cheap fire-and-forget; never blocks the
// rotation flow on broadcast errors.
func (s *Service) broadcastNotice(rotID string, targets []RotationTarget, pskFP, msg string) {
	if msg == "" {
		return
	}
	s.hubRef.broadcast(ws.Event{
		Type: EventFleetSecRotation,
		Payload: RotationProgressEvent{
			RotationID: rotID,
			Kind:       RotationKindPSK,
			Targets:    targets,
			NewPSKFP:   pskFP,
			Notice:     msg,
		},
	})
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
	anyEligible := false
	for i := range current {
		if !want[current[i].NodeNum] {
			continue
		}
		switch current[i].Phase {
		case PhaseFailedB, PhasePushingB:
			current[i].Phase = PhasePending
			current[i].Status = statusForPhase(PhasePending)
			current[i].LastError = ""
			anyEligible = true
		case PhaseFailedC, PhasePromotingC:
			current[i].Phase = PhaseHasNewPSK
			current[i].Status = statusForPhase(PhaseHasNewPSK)
			current[i].LastError = ""
			anyEligible = true
		case PhasePending:
			// Already pending -- worker will pick it up; counts as
			// a retry request even though no reset was needed.
			anyEligible = true
		case PhaseOnNewPSK, PhaseRetired:
			// Already done; nothing to retry. Skip silently.
			continue
		case PhaseHasNewPSK:
			// In an unusual mid-rotation snapshot: B succeeded but
			// the worker hadn't reached this target's C yet. Worker
			// will pick it up.
			anyEligible = true
		}
	}
	if !anyEligible {
		return errors.New("no eligible targets matched the retry list (already done or unknown node-num)")
	}

	// Persist the reset target rows so the worker sees the right
	// resting state when it picks each Phase B job up.
	if err := s.store.UpdateRotationTargets(ctx, id, current, nil); err != nil {
		return fmt.Errorf("persist retry target reset: %w", err)
	}

	if s.jobs == nil {
		return errors.New("fleet-security jobs queue not wired (svc.SetJobsStore not called)")
	}

	// Re-enqueue Phase A. The handler is idempotent: it skips Pi
	// staging if pi_local_phase is already past pending, then
	// enqueues fresh Phase B jobs for any target not on_new_psk.
	// Using Phase A as the entry point lets us reuse its slot-pick
	// + per-target debounce instead of duplicating the loop here.
	if _, err := s.jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:       jobs.KindRotatePhaseA,
		RotationID: &id,
		Payload:    PhaseAPayload{},
	}); err != nil {
		return fmt.Errorf("enqueue phase A retry: %w", err)
	}
	s.auditFleet(ctx, userID, "rotate_psk_retry", "channel",
		fmt.Sprintf("%d", *rec.ChannelIndex), map[string]any{
			"rotation_id":  id,
			"target_count": len(targetNodeNums),
			"new_psk_fp":   rec.NewPSKFP,
		})
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
