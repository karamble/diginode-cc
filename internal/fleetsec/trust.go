package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
)

// VerifyResult is what the UI's "Verify" button surfaces back.
type VerifyResult struct {
	NodeNum     uint32       `json:"nodeNum"`
	OK          bool         `json:"ok"`
	Method      VerifyMethod `json:"method"`
	AdminKeyFPs []string     `json:"adminKeyFingerprints"`
	IsManaged   bool         `json:"isManaged"`
	Err         string       `json:"error,omitempty"`
}

// ListTrust returns the per-node trust roster joined with basic node
// info, plus drift status against fleet policy. The roster is what
// drives the Trust card's table.
func (s *Service) ListTrust(ctx context.Context) ([]NodeTrustRecord, error) {
	rows, err := s.store.ListNodeTrust(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := s.store.GetPolicy(ctx)
	if err != nil {
		return nil, err
	}

	// Recompute drift in-memory based on current policy. The DB column
	// is just the last-cached value; this freshens it for display.
	for i := range rows {
		rows[i].DriftStatus = computeDriftStatus(rows[i], policy)
	}
	return rows, nil
}

// GetTrust queries the remote node's SecurityConfig over PKC, persists
// the result, and returns the freshened trust record. This is the
// expensive read path used by the Trust roster's per-row "Verify" button
// and by SetIsManaged's recency check.
//
// The local Heltec must already have the remote node's pubkey in its
// NodeDB (learned via NodeInfo broadcasts). If not, the firmware
// rejects the PKC packet and the caller gets a routing error.
func (s *Service) GetTrust(ctx context.Context, nodeNum uint32) (*NodeTrustRecord, error) {
	if nodeNum == 0 {
		return nil, errors.New("node number must be non-zero")
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	// Decide local vs remote: if asked for the local Heltec's number,
	// avoid a needless PKC round-trip.
	method := VerifyMethodRemotePKC
	var reply *pb.AdminMessage
	var err error
	if local := s.localNode.LocalNodeNum(); local != 0 && nodeNum == local {
		method = VerifyMethodLocalUSB
		reply, err = s.runLocalAdmin(ctx, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "local-get-security")
	} else {
		reply, err = s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-get-security")
	}
	if err != nil {
		// Persist unreachable status so the UI shows an honest pill,
		// but propagate the error to the handler.
		now := time.Now().UTC()
		_ = s.store.UpsertNodeTrust(ctx, NodeTrustRecord{
			NodeNum:          nodeNum,
			LastDriftCheckAt: &now,
			DriftStatus:      DriftStatusUnreachable,
		})
		return nil, fmt.Errorf("get_config from %x: %w", nodeNum, err)
	}
	sec, err := extractSecurityConfig(reply)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	keyFPs := fingerprintsFromAdminKeys(sec.GetAdminKey())
	policy, err := s.store.GetPolicy(ctx)
	if err != nil {
		return nil, err
	}
	rec := NodeTrustRecord{
		NodeNum:          nodeNum,
		AdminKeyFPs:      keyFPs,
		IsManaged:        sec.GetIsManaged(),
		LastVerifiedAt:   &now,
		LastVerifyMethod: method,
		LastDriftCheckAt: &now,
	}
	rec.DriftStatus = computeDriftStatus(rec, policy)
	if err := s.store.UpsertNodeTrust(ctx, rec); err != nil {
		return nil, fmt.Errorf("upsert trust: %w", err)
	}
	// Channel-layer PSK match is implicit in a successful PKC GetConfig
	// round-trip: the request would not have decoded at the remote
	// otherwise. Stamp current_psk_fp with the Pi-Heltec's PRIMARY-channel
	// fingerprint at this moment so the staged-rotation retirement gate
	// can see "fleet member is on the same PSK as us".
	if piFP := s.localPrimaryPSKFP(ctx); piFP != "" {
		if err := s.store.SetNodeCurrentPSKFP(ctx, nodeNum, piFP); err != nil {
			slog.Warn("set node current_psk_fp",
				"node_num", nodeNum, "error", err)
		}
	}
	return s.store.GetNodeTrust(ctx, nodeNum)
}

// localPrimaryPSKFP fetches the Pi-Heltec's current PRIMARY channel
// fingerprint from fleet_channels (channel index 0 row). Returns "" if
// no row is recorded yet (pre-rotation install) or on error -- the
// caller treats "" as "skip stamping current_psk_fp this round," which
// keeps the retirement gate's "every node confirmed" semantics correct
// even with intermittent DB errors.
func (s *Service) localPrimaryPSKFP(ctx context.Context) string {
	chs, err := s.store.ListChannels(ctx)
	if err != nil {
		return ""
	}
	for _, c := range chs {
		if c.Index == 0 {
			return c.PSKFingerprint
		}
	}
	return ""
}

// VerifyTrust is the lightweight equivalent of GetTrust used by the UI's
// "Verify now" button. Returns a VerifyResult struct optimised for the
// common case (success → green pill); on error, preserves the message
// for the UI's failed-tray.
//
// Writes a fleetsec.verify_trust audit row keyed to userID (the operator
// who clicked the button). Required for §10.10 review -- background
// drift checks call GetTrust directly and don't audit, so the audit log
// reflects only user-initiated verify actions.
func (s *Service) VerifyTrust(ctx context.Context, userID string, nodeNum uint32) VerifyResult {
	res := VerifyResult{NodeNum: nodeNum}
	got, err := s.GetTrust(ctx, nodeNum)
	if err != nil {
		res.Err = err.Error()
		s.auditFleet(ctx, userID, "verify_trust", "node", fmt.Sprintf("%d", nodeNum), map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return res
	}
	res.OK = true
	res.Method = got.LastVerifyMethod
	res.AdminKeyFPs = got.AdminKeyFPs
	res.IsManaged = got.IsManaged
	s.auditFleet(ctx, userID, "verify_trust", "node", fmt.Sprintf("%d", nodeNum), map[string]any{
		"ok":                     true,
		"method":                 string(got.LastVerifyMethod),
		"admin_key_fingerprints": got.AdminKeyFPs,
		"is_managed":             got.IsManaged,
		"drift_status":           string(got.DriftStatus),
	})
	return res
}

// SetAdminKeysOpts modifies SetAdminKeys behaviour.
type SetAdminKeysOpts struct {
	// Force allows the operation even if the resulting list contains
	// no known operator pubkey (lockout-prevention override).
	Force bool
	// Ack must be the exact string "LOCKOUT" when Force is true; this
	// is the typed-confirmation gate the UI surfaces.
	Ack string
}

// SetAdminKeys replaces the admin_key list on the target node with the
// pubkeys identified by keyFPs, then re-reads to confirm. Refuses if
// the resulting list shares zero entries with the operator's identity
// registry, unless force-acked.
//
// keyFPs are fingerprints (the UI sends fingerprints, not raw pubkeys);
// fleetsec resolves them to pubkey bytes from the registry.
func (s *Service) SetAdminKeys(ctx context.Context, userID string, nodeNum uint32, keyFPs []string, opts SetAdminKeysOpts) error {
	if nodeNum == 0 {
		return errors.New("node number must be non-zero")
	}
	if len(keyFPs) > 3 {
		// Meshtastic SecurityConfig.admin_key has 3 slots; firmware
		// silently truncates beyond that.
		return errors.New("admin_key list cannot exceed 3 entries")
	}

	known, err := s.pickKnownFingerprints(ctx, keyFPs)
	if err != nil {
		return fmt.Errorf("look up identity registry: %w", err)
	}
	if len(known) == 0 {
		if !opts.Force {
			return ErrLockoutPrevented
		}
		if opts.Ack != "LOCKOUT" {
			return ErrInvalidAck
		}
	}

	pubs, err := s.pubkeysForFingerprints(ctx, keyFPs)
	if err != nil {
		return err
	}

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	msg := AdminSetSecurity(SecurityConfigUpdate{AdminKeys: pubs})
	useRemote := true
	if local := s.localNode.LocalNodeNum(); local != 0 && nodeNum == local {
		useRemote = false
	}
	if useRemote {
		_, err = s.runRemoteAdmin(ctx, nodeNum, msg, "remote-set-admin-keys")
	} else {
		_, err = s.runLocalAdmin(ctx, msg, "local-set-admin-keys")
	}
	if err != nil {
		return fmt.Errorf("push admin_key list: %w", err)
	}

	// Re-read to confirm and refresh drift status.
	if _, err := s.getTrustLocked(ctx, nodeNum); err != nil {
		// Push succeeded but read-back failed -- still record the
		// operation as having executed. The trust pill will update on
		// the next verify pass.
		s.auditFleet(ctx, userID, "set_admin_keys_partial", "node", fmt.Sprintf("%d", nodeNum), map[string]any{
			"key_fingerprints":      keyFPs,
			"force":                 opts.Force,
			"readback_error":        err.Error(),
		})
		return nil
	}

	auditPayload := map[string]any{
		"key_fingerprints": keyFPs,
		"force":            opts.Force,
	}
	if opts.Force {
		auditPayload["severity"] = "critical"
	}
	s.auditFleet(ctx, userID, "set_admin_keys", "node", fmt.Sprintf("%d", nodeNum), auditPayload)
	return nil
}

// SetIsManagedOpts modifies SetIsManaged behaviour.
type SetIsManagedOpts struct {
	Force bool
	Ack   string // must equal "LOCKDOWN" when Force=true and value=true
}

// SetIsManaged sets the SecurityConfig.is_managed flag on a target node.
// Refuses to set true if the node hasn't been verify-acked in the last
// 24h (lockdown-without-verification protection), unless force-acked.
//
// Setting is_managed=true disables LOCAL admin on the node -- after that
// only PKC remote admin works. Setting it back to false re-enables local
// USB admin.
func (s *Service) SetIsManaged(ctx context.Context, userID string, nodeNum uint32, value bool, opts SetIsManagedOpts) error {
	if nodeNum == 0 {
		return errors.New("node number must be non-zero")
	}

	if value {
		// Recency check: trust state must show a successful verify
		// within the last 24h. This protects against enabling lockdown
		// on a node we can't actually reach.
		current, err := s.store.GetNodeTrust(ctx, nodeNum)
		if errors.Is(err, ErrNotFound) {
			if !opts.Force {
				return ErrManagedLockdownPrevented
			}
		} else if err != nil {
			return err
		} else if current.LastVerifiedAt == nil ||
			time.Since(*current.LastVerifiedAt) > 24*time.Hour {
			if !opts.Force {
				return ErrManagedLockdownPrevented
			}
		}
		if opts.Force && opts.Ack != "LOCKDOWN" {
			return ErrInvalidAck
		}
	}

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	msg := AdminSetSecurity(SecurityConfigUpdate{IsManaged: &value})
	var err error
	if local := s.localNode.LocalNodeNum(); local != 0 && nodeNum == local {
		_, err = s.runLocalAdmin(ctx, msg, "local-set-is-managed")
	} else {
		_, err = s.runRemoteAdmin(ctx, nodeNum, msg, "remote-set-is-managed")
	}
	if err != nil {
		return fmt.Errorf("push is_managed=%v: %w", value, err)
	}

	auditPayload := map[string]any{
		"value": value,
		"force": opts.Force,
	}
	if opts.Force {
		auditPayload["severity"] = "critical"
	}
	s.auditFleet(ctx, userID, "set_is_managed", "node", fmt.Sprintf("%d", nodeNum), auditPayload)
	return nil
}

// getTrustLocked is the lock-already-held variant of GetTrust used by
// SetAdminKeys for the post-push read-back. The outer lock is
// adminMu, held by the caller.
func (s *Service) getTrustLocked(ctx context.Context, nodeNum uint32) (*NodeTrustRecord, error) {
	method := VerifyMethodRemotePKC
	var reply *pb.AdminMessage
	var err error
	if local := s.localNode.LocalNodeNum(); local != 0 && nodeNum == local {
		method = VerifyMethodLocalUSB
		reply, err = s.runLocalAdmin(ctx, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "local-get-security")
	} else {
		reply, err = s.runRemoteAdmin(ctx, nodeNum, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "remote-get-security")
	}
	if err != nil {
		return nil, err
	}
	sec, err := extractSecurityConfig(reply)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	policy, _ := s.store.GetPolicy(ctx)
	rec := NodeTrustRecord{
		NodeNum:          nodeNum,
		AdminKeyFPs:      fingerprintsFromAdminKeys(sec.GetAdminKey()),
		IsManaged:        sec.GetIsManaged(),
		LastVerifiedAt:   &now,
		LastVerifyMethod: method,
		LastDriftCheckAt: &now,
	}
	if policy != nil {
		rec.DriftStatus = computeDriftStatus(rec, policy)
	} else {
		rec.DriftStatus = DriftStatusUnknown
	}
	if err := s.store.UpsertNodeTrust(ctx, rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// computeDriftStatus diffs a node's trust state against the current
// fleet policy. Pure function, no IO -- suitable for in-memory recompute
// during list operations.
func computeDriftStatus(n NodeTrustRecord, p *FleetPolicy) DriftStatus {
	if n.LastVerifiedAt == nil {
		return DriftStatusUnknown
	}
	if p == nil {
		return DriftStatusInPolicy
	}
	// is_managed must match.
	if p.ExpectedIsManaged != n.IsManaged {
		return DriftStatusDrift
	}
	// Every expected admin_key fingerprint must be present in the node's
	// current list. Extra keys on the node are tolerated (operator may
	// have added a transient operator key not in policy yet).
	got := make(map[string]bool, len(n.AdminKeyFPs))
	for _, fp := range n.AdminKeyFPs {
		got[fp] = true
	}
	for _, want := range p.ExpectedAdminKeyFPs {
		if !got[want] {
			return DriftStatusDrift
		}
	}
	return DriftStatusInPolicy
}
