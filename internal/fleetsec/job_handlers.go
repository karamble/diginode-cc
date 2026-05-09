package fleetsec

// Job handlers for the fleet_jobs queue. Each handler wraps the
// existing low-level migrate primitives (applyLocalStagingChannel,
// migrateRemoteAtomic, migratePiAtomic) with the job-payload decode
// and post-success bookkeeping (transitionTarget, stamps,
// notifications). Registered with jobs.Loop in main.go startup.
//
// Handlers MUST be idempotent. The worker may re-lease a job after
// a crashed in_progress run. Idempotency comes from the firmware-side
// atomic transactions (begin/commit overrides whatever the slot held)
// plus DB-state checks before duplicate work.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/fleetsec/jobs"
	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"github.com/karamble/diginode-cc/internal/ws"
)

// PhaseAPayload carries no per-job state. The Phase A handler probes
// Pi, picks the staging slot (reusing a pinned slot from the rotation
// row if present), stages the new PSK as SECONDARY, then enqueues a
// Phase B job per remote target. All state lives on fleet_rotations.
type PhaseAPayload struct{}

// PhaseBPayload is the per-remote atomic-migrate job. The handler
// receives stagingIdx + oldSlot pre-computed by Phase A so it does
// not need to re-probe Pi for every remote.
type PhaseBPayload struct {
	StagingIdx    int32  `json:"stagingIdx"`
	OldSlot       int32  `json:"oldSlot"`
	TargetNodeNum uint32 `json:"targetNodeNum"`
}

// PhaseCPayload is the operator-paced Pi-promote job. Enqueued by
// RetireOldPSK after the gate check passes. The handler re-probes
// Pi to find the OLD slot dynamically (resilient to slot drift
// between gate check and Phase C dispatch).
type PhaseCPayload struct {
	StagingIdx int32 `json:"stagingIdx"`
}

// ---- Phase A handler ----

type phaseAHandler struct{ svc *Service }

func (h *phaseAHandler) Kind() jobs.Kind { return jobs.KindRotatePhaseA }

func (h *phaseAHandler) Run(ctx context.Context, job *jobs.Job) error {
	if job.RotationID == nil {
		return errors.New("phase A job missing rotation_id")
	}
	rotID := *job.RotationID

	rec, err := h.svc.store.GetRotation(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation: %w", err)
	}

	psk, err := h.svc.store.GetRotationPSK(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation psk: %w", err)
	}
	if len(psk) == 0 {
		return errors.New("rotation has no stored PSK (already cleared?)")
	}
	defer NewSecret(psk).Clear()

	pskFP := Fingerprint(psk)
	current := append([]RotationTarget(nil), rec.Targets...)
	channelIdx := int32(0)
	if rec.ChannelIndex != nil {
		channelIdx = *rec.ChannelIndex
	}

	// Probe Pi for primary slot. We need this both to pick the
	// staging slot (if not pinned) and as the OLD slot for Phase B.
	_, primarySlot, perr := h.svc.probeSlotsLocal(ctx)
	if perr != nil {
		return fmt.Errorf("probe Pi slots: %w", perr)
	}

	// Determine staging slot. Reuse a pinned slot from a prior run
	// (retry / re-lease) so the catch-up lands at the same place.
	var stagingIdx int32
	if rec.StagingChannelIndex != nil {
		stagingIdx = *rec.StagingChannelIndex
	} else {
		switch primarySlot {
		case 0:
			stagingIdx = 1
		case 1:
			stagingIdx = 0
		default:
			return fmt.Errorf("Pi PRIMARY at non-canonical slot %d (expected 0 or 1); run reset_node first", primarySlot)
		}
		if err := h.svc.store.SetStagingChannelIndex(ctx, rotID, stagingIdx); err != nil {
			slog.Warn("persist staging_channel_index", "rotation_id", rotID, "error", err)
		}
		h.svc.broadcastNotice(rotID, current, pskFP,
			fmt.Sprintf("Picked staging slot %d (Pi PRIMARY at %d)", stagingIdx, primarySlot))
	}

	// Stage Pi if not already staged. SetChannel(stagingIdx, SECONDARY,
	// newPSK) is idempotent at the firmware level (overwrites whatever
	// the slot held). DB phase check just avoids re-broadcasting.
	if rec.PiLocalPhase == PiPhasePending {
		h.svc.broadcastNotice(rotID, current, pskFP,
			fmt.Sprintf("Phase A · staging new PSK on Pi at slot %d", stagingIdx))
		if err := h.svc.applyLocalStagingChannel(ctx, stagingIdx, psk); err != nil {
			return fmt.Errorf("phase A staging: %w", err)
		}
		if err := h.svc.store.UpsertPiLocalPhase(ctx, rotID, PiPhaseStagingAdded); err != nil {
			slog.Warn("persist pi_local_phase=staging_added",
				"rotation_id", rotID, "error", err)
		}
	} else {
		slog.Info("phase A: Pi already staged, skipping staging step",
			"rotation_id", rotID, "pi_local_phase", rec.PiLocalPhase)
	}

	// Update fleet_channels with the new PSK fingerprint so the
	// channels card reflects the rotation. The retirement gate
	// (AllManagedNodesOnPSK) compares per-node current_psk_fp against
	// this; Phase B stamps that as it succeeds per remote.
	if err := h.svc.store.UpsertChannel(ctx, ChannelRecord{
		Index:          channelIdx,
		Name:           "",
		Role:           "",
		PSKFingerprint: pskFP,
		PSKLength:      len(psk),
		LastRotatedAt:  timeNowPtr(),
		LastRotatedBy:  rec.StartedBy,
		LastRotationID: rotID,
	}); err != nil {
		slog.Warn("update fleet_channels after staging",
			"rotation_id", rotID, "error", err)
	}

	// Enqueue Phase B for each remote target not yet done. Skip the
	// local node (Phase C handles Pi). Skip targets that already
	// reached on_new_psk or retired (idempotent re-enqueue is harmless
	// but pollutes job history). Debounce against existing pending
	// jobs to avoid duplicate enqueues on re-lease.
	localNum := h.svc.localNode.LocalNodeNum()
	enqueued := 0
	for _, t := range current {
		if t.NodeNum == localNum {
			continue
		}
		if t.Phase == PhaseOnNewPSK || t.Phase == PhaseRetired {
			continue
		}
		nodeNum64 := int64(t.NodeNum)
		pending, err := h.svc.jobs.HasPendingFor(ctx, jobs.KindRotatePhaseB, nodeNum64)
		if err != nil {
			slog.Warn("phase B debounce check",
				"rotation_id", rotID, "node_num", t.NodeNum, "error", err)
		}
		if pending {
			continue
		}
		_, err = h.svc.jobs.Enqueue(ctx, jobs.EnqueueOpts{
			Kind:          jobs.KindRotatePhaseB,
			RotationID:    &rotID,
			TargetNodeNum: &nodeNum64,
			Payload: PhaseBPayload{
				StagingIdx:    stagingIdx,
				OldSlot:       primarySlot,
				TargetNodeNum: t.NodeNum,
			},
		})
		if err != nil {
			return fmt.Errorf("enqueue phase B for !%08x: %w", t.NodeNum, err)
		}
		enqueued++
	}

	h.svc.broadcastNotice(rotID, current, pskFP,
		fmt.Sprintf("Phase A done · %d Phase B job(s) queued", enqueued))
	return nil
}

// ---- Phase B handler ----

type phaseBHandler struct{ svc *Service }

func (h *phaseBHandler) Kind() jobs.Kind { return jobs.KindRotatePhaseB }

func (h *phaseBHandler) Run(ctx context.Context, job *jobs.Job) error {
	if job.RotationID == nil {
		return errors.New("phase B job missing rotation_id")
	}
	rotID := *job.RotationID
	var p PhaseBPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("decode phase B payload: %w", err)
	}
	if p.TargetNodeNum == 0 {
		return errors.New("phase B targetNodeNum must be non-zero")
	}
	psk, err := h.svc.store.GetRotationPSK(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation psk: %w", err)
	}
	if len(psk) == 0 {
		return errors.New("rotation has no stored PSK")
	}
	defer NewSecret(psk).Clear()

	rec, err := h.svc.store.GetRotation(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation: %w", err)
	}
	pskFP := Fingerprint(psk)
	current := append([]RotationTarget(nil), rec.Targets...)
	channelIdx := int32(0)
	if rec.ChannelIndex != nil {
		channelIdx = *rec.ChannelIndex
	}

	// Find this target's row. Idempotent: skip if already on_new_psk.
	var t *RotationTarget
	for i := range current {
		if current[i].NodeNum == p.TargetNodeNum {
			t = &current[i]
			break
		}
	}
	if t == nil {
		return fmt.Errorf("target %x not found in rotation row", p.TargetNodeNum)
	}
	if t.Phase == PhaseOnNewPSK || t.Phase == PhaseRetired {
		slog.Info("phase B: skipping (already on new PSK)",
			"rotation_id", rotID, "node_num", p.TargetNodeNum)
		return nil
	}

	_ = h.svc.store.IncrementTargetAttempts(ctx, rotID, p.TargetNodeNum)
	h.svc.transitionTarget(ctx, rotID, channelIdx, current, t, PhasePushingB, "", pskFP)
	h.svc.broadcastNotice(rotID, current, pskFP,
		fmt.Sprintf("Phase B · atomic migrate of !%08x", p.TargetNodeNum))

	if err := h.svc.migrateRemoteAtomic(ctx, p.TargetNodeNum, p.StagingIdx, p.OldSlot, psk); err != nil {
		h.svc.transitionTarget(ctx, rotID, channelIdx, current, t, PhaseFailedB, err.Error(), pskFP)
		h.svc.broadcastNotice(rotID, current, pskFP,
			fmt.Sprintf("Phase B failed for !%08x", p.TargetNodeNum))
		return fmt.Errorf("phase B atomic migrate: %w", err)
	}

	h.svc.transitionTarget(ctx, rotID, channelIdx, current, t, PhaseOnNewPSK, "", pskFP)
	if mErr := h.svc.store.MarkTrustVerifiedNow(ctx, p.TargetNodeNum, VerifyMethodRemotePKC); mErr != nil {
		slog.Warn("mark trust verified after migrate",
			"rotation_id", rotID, "node_num", p.TargetNodeNum, "error", mErr)
	}
	if mErr := h.svc.store.SetNodeCurrentPSKFP(ctx, p.TargetNodeNum, pskFP); mErr != nil {
		slog.Warn("stamp current_psk_fp after migrate",
			"rotation_id", rotID, "node_num", p.TargetNodeNum, "error", mErr)
	}
	h.svc.broadcastNotice(rotID, current, pskFP,
		fmt.Sprintf("Phase B done · !%08x on new PSK", p.TargetNodeNum))

	// If every remote target is now on_new_psk (or retired), stamp
	// completed_at so the UI drawer flips to its "Phase B complete · ready
	// to retire" state. The local Pi target stays in pending until Phase C
	// promotes it; that's expected for the operator-paced retirement gate.
	allRemotesDone := true
	localNum := h.svc.localNode.LocalNodeNum()
	for _, ct := range current {
		if ct.NodeNum == localNum {
			continue
		}
		if ct.Phase != PhaseOnNewPSK && ct.Phase != PhaseRetired {
			allRemotesDone = false
			break
		}
	}
	if allRemotesDone {
		if uErr := h.svc.store.UpdateRotationTargets(ctx, rotID, current, timeNowPtr()); uErr != nil {
			slog.Warn("stamp completed_at after final phase B",
				"rotation_id", rotID, "error", uErr)
		}
		h.svc.hubRef.broadcast(ws.Event{
			Type: EventFleetSecRotation,
			Payload: RotationProgressEvent{
				RotationID: rotID,
				Kind:       RotationKindPSK,
				Targets:    current,
				Done:       true,
				NewPSKFP:   pskFP,
			},
		})
	}
	return nil
}

// ---- Phase C handler ----

type phaseCHandler struct{ svc *Service }

func (h *phaseCHandler) Kind() jobs.Kind { return jobs.KindRotatePhaseC }

func (h *phaseCHandler) Run(ctx context.Context, job *jobs.Job) error {
	if job.RotationID == nil {
		return errors.New("phase C job missing rotation_id")
	}
	rotID := *job.RotationID
	var p PhaseCPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("decode phase C payload: %w", err)
	}
	rec, err := h.svc.store.GetRotation(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation: %w", err)
	}
	if rec.RetiredAt != nil || rec.PiLocalPhase == PiPhaseRetired {
		slog.Info("phase C: skipping (already retired)", "rotation_id", rotID)
		return nil
	}

	// Find Pi's actual OLD-PSK slot dynamically: PRIMARY whose fp != newFP.
	// Capture the OLD PSK bytes too so we can stash them in the recovery
	// cache after Phase C succeeds (lets us auto-rotate stranded nodes
	// when they come back online days later).
	newPSKFP := rec.NewPSKFP
	oldSlot := int32(-1)
	var oldPSK []byte
	for idx := int32(0); idx < 8; idx++ {
		ch, perr := h.svc.readLocalChannel(ctx, idx)
		if perr != nil {
			continue
		}
		if ch.GetRole() != pb.Channel_PRIMARY {
			continue
		}
		var pskBytes []byte
		if ch.GetSettings() != nil {
			pskBytes = ch.GetSettings().GetPsk()
		}
		if Fingerprint(pskBytes) != newPSKFP {
			oldSlot = idx
			oldPSK = append([]byte(nil), pskBytes...)
			break
		}
	}
	if oldSlot < 0 {
		// Pi already on new PSK as PRIMARY (previous Phase C succeeded
		// firmware-side but lost the reply). Mark retired and exit.
		slog.Info("phase C: Pi already on new PSK; nothing to migrate",
			"rotation_id", rotID)
		return h.svc.store.MarkRotationRetired(ctx, rotID)
	}

	psk, err := h.svc.store.GetRotationPSK(ctx, rotID)
	if err != nil {
		return fmt.Errorf("fetch rotation psk: %w", err)
	}
	if len(psk) == 0 {
		return errors.New("rotation has no stored PSK; cannot promote Pi without it")
	}
	defer NewSecret(psk).Clear()

	pskFP := Fingerprint(psk)
	current := append([]RotationTarget(nil), rec.Targets...)

	// Pre-pick the recovery slot for the OLD PSK so we can fold its
	// SetChannel into the same atomic transaction as the migrate.
	// Two consecutive flash writes on some firmware versions trigger
	// a soft reboot mid-write that leaves the radio unresponsive for
	// ~5 minutes (observed 2026-05-09 against firmware 2.7.21.e854894).
	// One commit = one flash write = no reboot.
	oldPSKFP := Fingerprint(oldPSK)
	recoverySlot := int32(-1)
	if len(oldPSK) > 0 {
		recRotID := rotID
		oldHash := ChannelHash("", oldPSK)
		slot, addErr := h.svc.store.AddRecoveryPSK(ctx, oldPSKFP, oldPSK, oldHash, &recRotID)
		if addErr != nil {
			slog.Warn("recovery cache: AddRecoveryPSK failed; rotation will retire without cache entry",
				"rotation_id", rotID, "old_fp", oldPSKFP, "error", addErr)
		} else {
			recoverySlot = slot
			slog.Info("recovery cache: pre-allocated slot for atomic write",
				"rotation_id", rotID, "slot", slot,
				"old_fp", oldPSKFP, "hash", oldHash)
		}
	}

	h.svc.broadcastNotice(rotID, current, pskFP,
		fmt.Sprintf("Phase C · Pi atomic promote slot %d, wipe slot %d, cache old at slot %d", p.StagingIdx, oldSlot, recoverySlot))

	if err := h.svc.migratePiAtomicWithRecovery(ctx, p.StagingIdx, oldSlot, psk, recoverySlot, oldPSK); err != nil {
		return fmt.Errorf("Pi atomic migrate: %w", err)
	}

	// Stamp local target on_new_psk.
	for i := range current {
		if current[i].NodeNum == h.svc.localNode.LocalNodeNum() {
			current[i].Phase = PhaseOnNewPSK
			current[i].Status = statusForPhase(PhaseOnNewPSK)
			current[i].LastError = ""
			break
		}
	}
	if uErr := h.svc.store.UpdateRotationTargets(ctx, rotID, current, timeNowPtr()); uErr != nil {
		slog.Warn("update rotation targets after Pi promote",
			"rotation_id", rotID, "error", uErr)
	}
	if mErr := h.svc.store.MarkTrustVerifiedNow(ctx, h.svc.localNode.LocalNodeNum(), VerifyMethodLocalUSB); mErr != nil {
		slog.Warn("mark local trust verified post-Pi-migrate",
			"rotation_id", rotID, "error", mErr)
	}
	if err := h.svc.store.MarkRotationRetired(ctx, rotID); err != nil {
		return fmt.Errorf("mark retired: %w", err)
	}

	// Mark every remote target NOT on the new PSK as stranded. The
	// recover_stranded job (enqueued by the dispatcher hook when one
	// of these nodes is heard on the recovery channel) will re-rotate
	// them onto the new PSK without operator intervention.
	for _, ct := range current {
		if ct.NodeNum == h.svc.localNode.LocalNodeNum() {
			continue
		}
		if ct.Phase == PhaseOnNewPSK || ct.Phase == PhaseRetired {
			continue
		}
		if mErr := h.svc.store.MarkStranded(ctx, ct.NodeNum, oldPSKFP); mErr != nil {
			slog.Warn("mark target stranded after retire",
				"rotation_id", rotID, "node_num", ct.NodeNum, "error", mErr)
		}
	}

	// Drop the stashed raw PSK now that Pi is on new and old is wiped fleet-wide.
	if cErr := h.svc.store.ClearRotationPSK(ctx, rotID); cErr != nil {
		slog.Warn("clear rotation psk after retire",
			"rotation_id", rotID, "error", cErr)
	}

	// Refresh the dispatcher's recovery-hash table so any subsequent
	// inbound packet from a stranded node on the just-retired PSK
	// triggers a recover_stranded job immediately.
	h.svc.rebuildRecoveryHook(ctx)

	h.svc.broadcastNotice(rotID, current, pskFP,
		fmt.Sprintf("Phase C done · Pi on new PSK · old slot %d wiped fleet-wide", oldSlot))
	return nil
}

// ---- recover_stranded handler ----

// RecoverStrandedPayload addresses one stranded node + the fingerprint
// of the PSK it was last on. The handler looks up the recovery cache
// row by fp, then runs the standard atomic migrate primitive against
// the node using the recovery slot index as the OLD slot the remote
// will demote/disable. The new PSK + staging slot are derived from
// Pi's current PRIMARY at execution time.
type RecoverStrandedPayload struct {
	NodeNum   uint32 `json:"nodeNum"`
	PrevPSKFP string `json:"prevPskFp"`
	// Source: "dispatcher" (event-driven hook), "scan" (periodic
	// stranded scan), or "manual" (operator forced via API). Audit-only.
	Source string `json:"source,omitempty"`
}

type recoverStrandedHandler struct{ svc *Service }

func (h *recoverStrandedHandler) Kind() jobs.Kind { return jobs.KindRecoverStrand }

func (h *recoverStrandedHandler) Run(ctx context.Context, job *jobs.Job) error {
	var p RecoverStrandedPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("decode recover-stranded payload: %w", err)
	}
	if p.NodeNum == 0 {
		return errors.New("recover-stranded payload missing nodeNum")
	}
	if p.PrevPSKFP == "" {
		return errors.New("recover-stranded payload missing prevPskFp")
	}

	// Fetch the cached PSK bytes that the stranded node is still on.
	rec, err := h.svc.store.GetRecoveryPSKByFP(ctx, p.PrevPSKFP)
	if err != nil {
		// Cache evicted (FIFO push from a later rotation while this
		// node was stranded across that rotation). Operator must
		// USB-recover. Stamp the failure on the node row.
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum,
			"prev psk evicted from recovery cache; manual USB recovery required")
		return fmt.Errorf("recovery psk for fp=%s not in cache (evicted)", p.PrevPSKFP)
	}
	defer NewSecret(rec.PSK).Clear()

	// Find Pi's current PRIMARY slot + the new PSK to migrate the
	// stranded node to. The new PSK lives at Pi's PRIMARY slot post-
	// Phase-C; we read it directly off the radio for accuracy.
	//
	// Resilience: probeSlotsLocal returns "no PRIMARY found" both when
	// every slot is genuinely DISABLED AND when the radio is busy
	// rebooting (every read times out). Empirically the Heltec V3
	// reboots after a Phase C atomic-with-recovery write and stays
	// down for 4-6 minutes before USB re-enumeration. Retry the
	// probe up to 5 times with 60s spacing so recovery jobs that
	// fire during the settle window catch the radio when it's back.
	var primarySlot int32
	var perr error
	probeAttempts := 5
	for attempt := 1; attempt <= probeAttempts; attempt++ {
		_, primarySlot, perr = h.svc.probeSlotsLocal(ctx)
		if perr == nil {
			break
		}
		if attempt == probeAttempts {
			break
		}
		slog.Warn("recover_stranded: probe Pi failed, will retry",
			"node_num", p.NodeNum, "attempt", attempt,
			"max_attempts", probeAttempts, "error", perr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(60 * time.Second):
		}
	}
	if perr != nil {
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum, "probe Pi after retries: "+perr.Error())
		return fmt.Errorf("probe Pi slots after %d attempts: %w", probeAttempts, perr)
	}
	if primarySlot < 0 || primarySlot > 1 {
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum,
			fmt.Sprintf("Pi PRIMARY at non-canonical slot %d", primarySlot))
		return fmt.Errorf("Pi PRIMARY at non-canonical slot %d", primarySlot)
	}
	primaryCh, perr := h.svc.readLocalChannel(ctx, primarySlot)
	if perr != nil {
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum, "read Pi PRIMARY: "+perr.Error())
		return fmt.Errorf("read Pi PRIMARY slot %d: %w", primarySlot, perr)
	}
	var newPSK []byte
	if primaryCh.GetSettings() != nil {
		newPSK = primaryCh.GetSettings().GetPsk()
	}
	if len(newPSK) == 0 {
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum, "Pi PRIMARY has no PSK material")
		return errors.New("Pi PRIMARY has no PSK material; cannot recover")
	}
	newPSKFP := Fingerprint(newPSK)
	if newPSKFP == p.PrevPSKFP {
		// Pi has rolled back somehow OR this node is already on the
		// current PSK. Either way nothing to do.
		_ = h.svc.store.ClearStranded(ctx, p.NodeNum)
		return nil
	}

	// Staging slot = the other of {0, 1}. The remote already has the
	// OLD PSK at SOME slot; the atomic transaction tells the remote
	// to install the new PSK at stagingIdx as PRIMARY (auto-demoting
	// its current PRIMARY) and then DISABLE the slot we tell it the
	// old PSK lives in. We don't know the remote's slot layout, but
	// firmware accepts SetChannel by slot index unconditionally — we
	// can target the same slot we use on Pi (0 or 1) since the
	// remote almost certainly mirrors that layout. If it doesn't, the
	// migrate succeeds for the new slot and the remote's stale old
	// slot is wiped on the next rotation.
	stagingIdx := int32(1)
	if primarySlot == 1 {
		stagingIdx = 0
	}
	oldSlot := primarySlot

	slog.Info("recover_stranded: starting",
		"node_num", p.NodeNum, "prev_fp", p.PrevPSKFP,
		"new_fp", newPSKFP, "staging_idx", stagingIdx,
		"recovery_slot", rec.Slot, "source", p.Source)

	// Standard atomic migrate. Same primitive Phase B uses; the
	// verify-via-probe fallback inside it handles the lost-ack case.
	if err := h.svc.migrateRemoteAtomic(ctx, p.NodeNum, stagingIdx, oldSlot, newPSK); err != nil {
		_ = h.svc.store.IncrementRecoveryAttempt(ctx, p.NodeNum, err.Error())
		return fmt.Errorf("recover-stranded migrate: %w", err)
	}

	// Stamp success on the node row.
	if mErr := h.svc.store.MarkTrustVerifiedNow(ctx, p.NodeNum, VerifyMethodRemotePKC); mErr != nil {
		slog.Warn("mark trust verified after recovery",
			"node_num", p.NodeNum, "error", mErr)
	}
	if mErr := h.svc.store.SetNodeCurrentPSKFP(ctx, p.NodeNum, newPSKFP); mErr != nil {
		slog.Warn("stamp current_psk_fp after recovery",
			"node_num", p.NodeNum, "error", mErr)
	}
	if cErr := h.svc.store.ClearStranded(ctx, p.NodeNum); cErr != nil {
		slog.Warn("clear stranded marker after recovery",
			"node_num", p.NodeNum, "error", cErr)
	}
	slog.Info("recover_stranded: succeeded",
		"node_num", p.NodeNum, "prev_fp", p.PrevPSKFP, "new_fp", newPSKFP)
	return nil
}

// ---- Wiring ----

// RegisterJobHandlers registers Phase A/B/C and recover_stranded
// handlers with the loop. Called from main.go startup after the
// fleetsec service is constructed.
func (s *Service) RegisterJobHandlers(loop *jobs.Loop) {
	loop.Register(&phaseAHandler{svc: s})
	loop.Register(&phaseBHandler{svc: s})
	loop.Register(&phaseCHandler{svc: s})
	loop.Register(&recoverStrandedHandler{svc: s})
}

// SetJobsStore wires the jobs.Store into the service so RotatePSK,
// RetireOldPSK, RetryRotation can enqueue jobs instead of running
// goroutines. Called from main.go startup.
func (s *Service) SetJobsStore(store *jobs.Store) {
	s.jobs = store
}
