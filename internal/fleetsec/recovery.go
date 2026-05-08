package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/ws"
)

// RecoveryStatus is the JSON wire shape returned by GetRecovery. It
// reuses fleet_rotations under the hood (kind='recovery'), with the
// targets array tracking per-node admin_key push status. Two extra
// fields surface the lifecycle stage so the UI can render the wizard's
// step indicator without polling for additional state.
type RecoveryStatus struct {
	ID                  string           `json:"id"`
	Stage               RecoveryStage    `json:"stage"`
	StartedAt           time.Time        `json:"startedAt"`
	CompletedAt         *time.Time       `json:"completedAt,omitempty"`
	NewPrimaryFP        string           `json:"newPrimaryFingerprint"`
	RescueFP            string           `json:"rescueFingerprint"`
	OldPrimaryFP        string           `json:"oldPrimaryFingerprint,omitempty"`
	Targets             []RotationTarget `json:"targets"`
	Notes               string           `json:"notes,omitempty"`
}

// RecoveryStage tracks the wizard's lifecycle position. Used as the
// audit log resource_id suffix and surfaced in RecoveryStatus.Stage so
// the UI can render the correct active step.
type RecoveryStage string

const (
	RecoveryStageInstallRescue   RecoveryStage = "install-rescue"   // local rescue install, just before fleet push
	RecoveryStagePushFleet       RecoveryStage = "push-fleet"        // pushing admin_keys to remote nodes
	RecoveryStageRestorePrimary  RecoveryStage = "restore-primary"   // re-installing freshly-minted primary on local Heltec
	RecoveryStageDone            RecoveryStage = "done"
	RecoveryStageFailed          RecoveryStage = "failed"
)

// EventFleetSecRecovery is the WS event type for recovery-stage updates.
const EventFleetSecRecovery ws.EventType = "fleet-security.recovery.progress"

// RecoveryProgressEvent is the WS payload carrying stage transitions
// and per-target status. Frontend's RecoveryWizard subscribes and
// updates the step indicator + per-target pills in real time.
type RecoveryProgressEvent struct {
	RecoveryID string           `json:"recoveryId"`
	Stage      RecoveryStage    `json:"stage"`
	Targets    []RotationTarget `json:"targets"`
	Done       bool             `json:"done"`
	Err        string           `json:"error,omitempty"`
}

// StartRecoveryOpts modifies StartRecovery behaviour.
type StartRecoveryOpts struct {
	// Ack must be the exact string "RECOVER" -- the typed-confirmation
	// gate the UI surfaces in the wizard.
	Ack string
	// NewPrimaryLabel is the registry label for the freshly-minted
	// primary keypair. Defaults to "cc-primary-recovered-<timestamp>"
	// if empty.
	NewPrimaryLabel string
	// Notes lands in fleet_rotations.notes and the audit payload.
	Notes string
}

// StartRecovery executes the disaster-recovery flow described in
// FLEET_SECURITY.md §6.4. Returns the RecoveryID immediately; the
// actual orchestration runs in a background goroutine.
//
// Flow:
//   1. Install operator-supplied rescue keypair on the LOCAL Heltec
//      (privkey too -- this is local USB only).
//   2. Mint a new primary keypair (caller's privkey is NOT preserved
//      after this call -- it's flushed to the Heltec then zeroed).
//   3. For every node in the fleet roster, push admin_keys=[rescue,
//      new-primary] over PKC. Signed by the rescue identity since the
//      local Heltec is currently running it.
//   4. Re-install the new-primary keypair on the LOCAL Heltec, dropping
//      rescue back to "registered but inactive". Operator's cold-storage
//      copy of rescue stays the source of truth.
//   5. Mark the old primary revoked in the registry; mark the new one
//      primary; rescue stays as role='rescue'.
//
// If step 3 fails for some nodes, those nodes remain on their old
// admin_key list (whatever it was before). Operator must physically
// recover them. Even partial recovery success is committed -- step 4
// runs no matter what to leave the local Heltec in a sane state.
//
// userID is threaded through for audit + the started_by column.
func (s *Service) StartRecovery(
	ctx context.Context,
	userID string,
	rescuePriv, rescuePub []byte,
	opts StartRecoveryOpts,
) (string, error) {
	if opts.Ack != "RECOVER" {
		return "", fmt.Errorf("recovery requires Ack=\"RECOVER\": %w", ErrInvalidAck)
	}
	if err := ValidateX25519PrivateKey(rescuePriv); err != nil {
		return "", fmt.Errorf("rescue priv: %w", err)
	}
	if err := ValidateX25519PublicKey(rescuePub); err != nil {
		return "", fmt.Errorf("rescue pub: %w", err)
	}
	derived, err := DerivePubkey(rescuePriv)
	if err != nil {
		return "", fmt.Errorf("derive: %w", err)
	}
	if !bytesEqual(derived, rescuePub) {
		return "", errors.New("rescue priv/pub mismatch")
	}

	// Snapshot the current local-Heltec pubkey BEFORE any change so we
	// know what to mark revoked at the end. Best-effort: if this fails
	// (e.g. Heltec rebooting), we proceed but won't revoke an old key.
	var oldPrimaryFP string
	if old, oerr := s.GetIdentity(ctx); oerr == nil && old != nil {
		oldPrimaryFP = old.Fingerprint
	}

	// Mint the new primary keypair. priv is held in SecretBytes through
	// the runner so we can zero it after the local install.
	newPriv, newPub, err := GenerateX25519Keypair()
	if err != nil {
		return "", fmt.Errorf("mint new primary: %w", err)
	}

	// Pre-build the target list from the current node roster. Filter
	// out nodes with no last-heard / explicitly unreachable -- they
	// can't be admin'd remotely anyway.
	rows, err := s.store.ListNodeTrust(ctx)
	if err != nil {
		return "", fmt.Errorf("read fleet roster: %w", err)
	}
	local := s.localNode.LocalNodeNum()
	rotTargets := make([]RotationTarget, 0, len(rows))
	for _, n := range rows {
		if n.NodeNum == 0 || n.NodeNum == local {
			continue
		}
		rotTargets = append(rotTargets, RotationTarget{
			NodeNum: n.NodeNum,
			Status:  TargetStatusPending,
		})
	}

	// Persist the recovery row. NewPSKFP is repurposed here to hold the
	// fingerprint of the new primary -- saves a schema change.
	chanIdx := int32(-1) // sentinel; recovery isn't channel-scoped
	id, err := s.store.InsertRotation(ctx, RotationRecord{
		Kind:         RotationKindRecovery,
		ChannelIndex: &chanIdx,
		StartedBy:    userID,
		Targets:      rotTargets,
		NewPSKFP:     Fingerprint(newPub),
		Notes:        opts.Notes,
	})
	if err != nil {
		return "", fmt.Errorf("create recovery row: %w", err)
	}

	s.auditFleet(ctx, userID, "recovery_start", "recovery", id, map[string]any{
		"recovery_id":         id,
		"new_primary_fp":      Fingerprint(newPub),
		"rescue_fp":           Fingerprint(rescuePub),
		"old_primary_fp":      oldPrimaryFP,
		"target_count":        len(rotTargets),
		"severity":            "critical",
	})

	// Detached context -- the HTTP handler returns 202 and the runner
	// continues independently. Take copies of the privkey bytes so the
	// caller can't mutate them under us.
	privCopy := append([]byte(nil), rescuePriv...)
	go s.runRecovery(context.Background(), userID, id, privCopy, rescuePub,
		newPriv, newPub, oldPrimaryFP, opts.NewPrimaryLabel, rotTargets)
	return id, nil
}

func (s *Service) runRecovery(
	ctx context.Context,
	userID, id string,
	rescuePriv, rescuePub []byte,
	newPriv, newPub []byte,
	oldPrimaryFP, newPrimaryLabel string,
	targets []RotationTarget,
) {
	defer func() {
		// Best-effort zero of all key material.
		for i := range rescuePriv {
			rescuePriv[i] = 0
		}
		for i := range newPriv {
			newPriv[i] = 0
		}
	}()

	current := append([]RotationTarget(nil), targets...)

	emit := func(stage RecoveryStage, done bool, errMsg string) {
		var completedAt *time.Time
		if done {
			t := time.Now().UTC()
			completedAt = &t
		}
		_ = s.store.UpdateRotationTargets(ctx, id, current, completedAt)
		s.hubRef.broadcast(ws.Event{
			Type: EventFleetSecRecovery,
			Payload: RecoveryProgressEvent{
				RecoveryID: id,
				Stage:      stage,
				Targets:    current,
				Done:       done,
				Err:        errMsg,
			},
		})
	}

	// Stage 1: install rescue keypair on local Heltec.
	emit(RecoveryStageInstallRescue, false, "")
	if err := s.installLocalKeypair(ctx, rescuePriv, rescuePub); err != nil {
		emit(RecoveryStageFailed, true, fmt.Sprintf("install rescue: %v", err))
		s.auditFleet(ctx, userID, "recovery_failed", "recovery", id, map[string]any{
			"stage":    "install-rescue",
			"error":    err.Error(),
			"severity": "critical",
		})
		return
	}

	// Stage 2: push new admin_key list to every remote target. Signed
	// by rescue identity (local Heltec currently runs it) using PKC
	// envelope encryption, same as routine SetAdminKeys.
	emit(RecoveryStagePushFleet, false, "")
	for i := range current {
		t := &current[i]
		t.Status = TargetStatusInFlight
		t.Attempts++
		emit(RecoveryStagePushFleet, false, "")

		err := s.pushRecoveryAdminKeys(ctx, t.NodeNum, rescuePub, newPub)
		if err != nil {
			t.Status = TargetStatusFailed
			t.LastError = err.Error()
			slog.Warn("recovery target failed",
				"recovery_id", id, "node_num", t.NodeNum, "error", err)
		} else {
			t.Status = TargetStatusAcked
			t.LastError = ""
		}
		emit(RecoveryStagePushFleet, false, "")
	}

	// Stage 3: re-install new primary on local Heltec. Even on partial
	// fleet success, this runs unconditionally so the local Heltec
	// doesn't get stuck running rescue.
	emit(RecoveryStageRestorePrimary, false, "")
	if err := s.installLocalKeypair(ctx, newPriv, newPub); err != nil {
		emit(RecoveryStageFailed, true, fmt.Sprintf("restore primary: %v", err))
		s.auditFleet(ctx, userID, "recovery_partial", "recovery", id, map[string]any{
			"stage":    "restore-primary",
			"error":    err.Error(),
			"severity": "critical",
		})
		return
	}

	// Update identity registry: register the new primary, mark old
	// revoked. Best-effort; doesn't fail the recovery if the registry
	// write hits a duplicate (re-running recovery is allowed).
	label := newPrimaryLabel
	if label == "" {
		label = fmt.Sprintf("cc-primary-recovered-%s", time.Now().UTC().Format("20060102-150405"))
	}
	_, _ = s.store.InsertIdentity(ctx, IdentityRecord{
		Label:       label,
		PublicKey:   newPub,
		Fingerprint: Fingerprint(newPub),
		Role:        IdentityRolePrimary,
		Source:      IdentitySourceRotated,
	})
	if oldPrimaryFP != "" && oldPrimaryFP != Fingerprint(newPub) {
		_ = s.store.RevokeIdentity(ctx, oldPrimaryFP, "recovery: replaced after compromise/loss")
	}
	// Make sure rescue is in the registry too -- if the operator
	// imported its pubkey before, this is a no-op via UNIQUE; if not,
	// we register it now as role=rescue.
	if _, gerr := s.store.GetIdentityByFingerprint(ctx, Fingerprint(rescuePub)); errors.Is(gerr, ErrNotFound) {
		_, _ = s.store.InsertIdentity(ctx, IdentityRecord{
			Label:       "cc-rescue",
			PublicKey:   rescuePub,
			Fingerprint: Fingerprint(rescuePub),
			Role:        IdentityRoleRescue,
			Source:      IdentitySourceImported,
		})
	}

	emit(RecoveryStageDone, true, "")

	successes := 0
	failures := 0
	for _, t := range current {
		if t.Status == TargetStatusAcked {
			successes++
		} else {
			failures++
		}
	}
	s.auditFleet(ctx, userID, "recovery_complete", "recovery", id, map[string]any{
		"recovery_id":     id,
		"new_primary_fp":  Fingerprint(newPub),
		"successes":       successes,
		"failures":        failures,
		"severity":        "critical",
	})
}

func (s *Service) installLocalKeypair(ctx context.Context, priv, pub []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	msg := AdminSetSecurity(SecurityConfigUpdate{
		PrivateKey: priv,
		PublicKey:  pub,
	})
	_, err := s.runLocalAdmin(ctx, msg, "local-set-security-recovery")
	return err
}

func (s *Service) pushRecoveryAdminKeys(ctx context.Context, nodeNum uint32, rescuePub, newPub []byte) error {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	msg := AdminSetSecurity(SecurityConfigUpdate{
		AdminKeys: [][]byte{rescuePub, newPub},
	})
	_, err := s.runRemoteAdmin(ctx, nodeNum, msg, "remote-recovery-set-admin-keys")
	return err
}

// GetRecovery returns the recovery row by id. The wizard's progress
// view uses this on initial load (the WS feed only carries deltas).
// Translates the underlying RotationRecord into RecoveryStatus shape.
func (s *Service) GetRecovery(ctx context.Context, id string) (*RecoveryStatus, error) {
	rec, err := s.store.GetRotation(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Kind != RotationKindRecovery {
		return nil, errors.New("not a recovery rotation")
	}
	stage := RecoveryStageInstallRescue
	if rec.CompletedAt != nil {
		stage = RecoveryStageDone
	}
	return &RecoveryStatus{
		ID:           rec.ID,
		Stage:        stage,
		StartedAt:    rec.StartedAt,
		CompletedAt:  rec.CompletedAt,
		NewPrimaryFP: rec.NewPSKFP, // see comment in StartRecovery
		Targets:      rec.Targets,
		Notes:        rec.Notes,
	}, nil
}
