package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/fleetsec/jobs"
)

// recoveryDispatchHook holds the in-memory channel-hash → recovery
// PSK fingerprint table the dispatcher uses to detect stranded-node
// returns. Rebuilt from fleet_recovery_psks on:
//   - service startup (initial load)
//   - after every Phase C completion (new entry added)
//   - after every recovery cache GC (entries removed)
//
// Lookup is O(1) on the hot path. The mu protects against concurrent
// rebuilds + concurrent dispatcher reads.
type recoveryDispatchHook struct {
	svc *Service
	mu  sync.RWMutex
	// hashFP maps channel_hash byte -> recovery PSK fingerprint.
	// Multiple slots can share a hash (collision); we record the
	// most-recent one.
	hashFP map[byte]string
	// cooldown tracks per-node enqueue debounce so a flood of
	// inbound packets from the same stranded node only enqueues
	// one recovery job per cooldown window.
	cooldown   map[uint32]time.Time
	cooldownMu sync.Mutex
}

func newRecoveryDispatchHook(svc *Service) *recoveryDispatchHook {
	return &recoveryDispatchHook{
		svc:      svc,
		hashFP:   map[byte]string{},
		cooldown: map[uint32]time.Time{},
	}
}

// RebuildHashTable refreshes the in-memory hash → fp table from the
// store. Cheap (6 rows max). Called from Phase C completion + on
// service startup.
func (h *recoveryDispatchHook) RebuildHashTable(ctx context.Context) error {
	recs, err := h.svc.store.ListRecoveryPSKs(ctx)
	if err != nil {
		return err
	}
	next := make(map[byte]string, len(recs))
	for _, r := range recs {
		next[byte(r.PSKHash)] = r.FP
	}
	h.mu.Lock()
	h.hashFP = next
	h.mu.Unlock()
	slog.Info("recovery hash table rebuilt",
		"recovery_count", len(recs))
	return nil
}

// ObserveInboundPacket implements meshtastic.StrandedRecoveryHook. The
// 4-step filter ladder eliminates false positives:
//   1. Did this packet arrive on a recovery-cache channel hash?
//   2. Is the sender flagged stranded in fleet_node_trust?
//   3. Already a recovery job pending or in-flight for this node?
//   4. Per-node cooldown not yet elapsed (avoids hot-loop enqueues
//      while the worker is already running this node's recovery)?
func (h *recoveryDispatchHook) ObserveInboundPacket(from uint32, channelHash byte, portNum uint32) {
	if h.svc.jobs == nil {
		return
	}

	h.mu.RLock()
	fp, ok := h.hashFP[channelHash]
	h.mu.RUnlock()
	if !ok {
		return
	}

	// Brief cooldown on enqueue (30s). Even with debounce inside the
	// store HasPendingFor, a rapid-fire of inbound packets can race
	// the lease window. Per-node throttle keeps us out of trouble.
	h.cooldownMu.Lock()
	if last, seen := h.cooldown[from]; seen && time.Since(last) < 30*time.Second {
		h.cooldownMu.Unlock()
		return
	}
	h.cooldown[from] = time.Now()
	h.cooldownMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trust, err := h.svc.store.GetNodeTrust(ctx, from)
	if err != nil || trust == nil {
		return
	}
	if trust.StrandedSince == nil {
		// Node isn't flagged stranded. Either it's already current
		// (in which case the recovery channel has stale traffic) or
		// it's an unmanaged operator-channel collision. Ignore.
		return
	}
	if trust.PreviousPSKFP != "" && trust.PreviousPSKFP != fp {
		// Node was last on a different historical PSK than the one
		// matching this hash. Could be a hash collision against a
		// different cached slot. Trust the recorded prev_fp.
		fp = trust.PreviousPSKFP
	}

	nodeNum64 := int64(from)
	pending, err := h.svc.jobs.HasPendingFor(ctx, jobs.KindRecoverStrand, nodeNum64)
	if err != nil {
		slog.Warn("recovery hook: HasPendingFor",
			"node_num", from, "error", err)
		return
	}
	if pending {
		return
	}

	payload := RecoverStrandedPayload{
		NodeNum:   from,
		PrevPSKFP: fp,
		Source:    "dispatcher",
	}
	if _, err := h.svc.jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:          jobs.KindRecoverStrand,
		TargetNodeNum: &nodeNum64,
		Payload:       payload,
	}); err != nil {
		slog.Warn("recovery hook: enqueue recover_stranded",
			"node_num", from, "error", err)
		return
	}
	slog.Info("recovery hook: enqueued recover_stranded",
		"node_num", from, "prev_fp", fp,
		"channel_hash", channelHash, "port_num", portNum)
}

// SetupRecoveryHook wires the hook into the Service + returns a
// reference for the dispatcher to register and the startup code to
// trigger an initial RebuildHashTable. main.go calls this after the
// jobs.Store has been registered via SetJobsStore.
func (s *Service) SetupRecoveryHook() *recoveryDispatchHook {
	h := newRecoveryDispatchHook(s)
	s.recoveryHook = h
	return h
}

// rebuildRecoveryHook is the post-Phase-C trigger to refresh the
// in-memory hash table after the recovery cache changed. Called from
// the Phase C handler. Safe to call when no hook is registered.
func (s *Service) rebuildRecoveryHook(ctx context.Context) {
	if s.recoveryHook == nil {
		return
	}
	if err := s.recoveryHook.RebuildHashTable(ctx); err != nil {
		slog.Warn("recovery hook: rebuild hash table",
			"error", err)
	}
}

// ---- Stranded API surface ----

// ListStranded returns every node currently flagged stranded. Read-
// only mirror of store.ListStranded for the API handler.
func (s *Service) ListStranded(ctx context.Context) ([]NodeTrustRecord, error) {
	return s.store.ListStranded(ctx)
}

// ForceRecoverStranded enqueues a recover_stranded job for the named
// node, bypassing the dispatcher's wait-for-inbound-traffic gate.
// Used from the operator UI's "Recover now" button. Returns the job
// id. Errors if the node has no stranded_since marker (nothing to
// recover) or no previous_psk_fp pointer (we don't know which
// recovery slot to use).
func (s *Service) ForceRecoverStranded(ctx context.Context, nodeNum uint32) (string, error) {
	if s.jobs == nil {
		return "", errors.New("fleet-security jobs queue not wired")
	}
	trust, err := s.store.GetNodeTrust(ctx, nodeNum)
	if err != nil {
		return "", err
	}
	if trust.StrandedSince == nil {
		return "", fmt.Errorf("node %d is not flagged stranded", nodeNum)
	}
	if trust.PreviousPSKFP == "" {
		return "", fmt.Errorf("node %d has no previous_psk_fp; cannot pick recovery slot", nodeNum)
	}
	nodeNum64 := int64(nodeNum)
	pending, err := s.jobs.HasPendingFor(ctx, jobs.KindRecoverStrand, nodeNum64)
	if err != nil {
		return "", err
	}
	if pending {
		return "", fmt.Errorf("recover_stranded job already pending for node %d", nodeNum)
	}
	payload := RecoverStrandedPayload{
		NodeNum:   nodeNum,
		PrevPSKFP: trust.PreviousPSKFP,
		Source:    "manual",
	}
	id, err := s.jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:          jobs.KindRecoverStrand,
		TargetNodeNum: &nodeNum64,
		Payload:       payload,
	})
	if err != nil {
		return "", err
	}
	s.auditFleet(ctx, "", "force_recover_stranded", "node",
		fmt.Sprintf("%d", nodeNum), map[string]any{
			"prev_psk_fp": trust.PreviousPSKFP,
			"job_id":      id,
		})
	return id, nil
}

// CancelStranded clears the stranded markers + previous_psk_fp pointer
// on a node, telling diginode-cc to stop attempting recovery.
// Audit-logged. The recovery cache slot the node referenced stays
// alive for any other stranded nodes (FIFO GC handles eviction).
func (s *Service) CancelStranded(ctx context.Context, userID string, nodeNum uint32) error {
	if err := s.store.ClearStranded(ctx, nodeNum); err != nil {
		return err
	}
	// Wipe the prev_fp pointer too; ClearStranded leaves it set as
	// historical. CancelStranded is a "give up entirely" gesture.
	if _, err := s.store.db.Pool.Exec(ctx, `
		UPDATE fleet_node_trust SET previous_psk_fp = NULL,
			last_recovery_error = 'operator: stop trying'
		 WHERE node_num = $1`, int64(nodeNum)); err != nil {
		return fmt.Errorf("wipe previous_psk_fp: %w", err)
	}
	s.auditFleet(ctx, userID, "cancel_stranded", "node",
		fmt.Sprintf("%d", nodeNum), map[string]any{})
	return nil
}
