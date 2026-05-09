package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Recovery cache CRUD over fleet_recovery_psks. Slot is the actual
// Pi-Heltec slot index (2..7). PSK material lives only here (no
// duplicate copy on fleet_node_trust).

// AddRecoveryPSK inserts a new recovery cache entry. If all 6 slots
// (2..7) are full, evicts the oldest by added_at and reuses its slot.
// Returns the slot index assigned. Does NOT touch the radio — the
// caller is responsible for SetChannel(slot, SECONDARY, psk) on
// Pi-Heltec.
func (s *Store) AddRecoveryPSK(ctx context.Context, fp string, psk []byte, hash byte, rotationID *string) (int32, error) {
	if len(psk) == 0 {
		return 0, errors.New("recovery psk must be non-empty")
	}
	if fp == "" {
		return 0, errors.New("recovery fp must be non-empty")
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// If this fp is already cached, refresh added_at and return the
	// existing slot (no eviction).
	var existingSlot int32
	err = tx.QueryRow(ctx, `SELECT slot FROM fleet_recovery_psks WHERE fp = $1`, fp).Scan(&existingSlot)
	if err == nil {
		if _, uErr := tx.Exec(ctx, `UPDATE fleet_recovery_psks SET added_at = now(), rotation_id = $2 WHERE fp = $1`, fp, rotationID); uErr != nil {
			return 0, fmt.Errorf("refresh existing recovery psk: %w", uErr)
		}
		if cErr := tx.Commit(ctx); cErr != nil {
			return 0, fmt.Errorf("commit refresh: %w", cErr)
		}
		return existingSlot, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("check existing fp: %w", err)
	}

	// Find first free slot in 2..7.
	var slot int32 = -1
	rows, err := tx.Query(ctx, `SELECT slot FROM fleet_recovery_psks ORDER BY slot`)
	if err != nil {
		return 0, fmt.Errorf("scan used slots: %w", err)
	}
	used := make(map[int32]bool, 6)
	for rows.Next() {
		var s32 int32
		if err := rows.Scan(&s32); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan slot: %w", err)
		}
		used[s32] = true
	}
	rows.Close()
	for i := int32(2); i <= 7; i++ {
		if !used[i] {
			slot = i
			break
		}
	}

	if slot < 0 {
		// Cache full: evict oldest and reuse its slot.
		var evictSlot int32
		err = tx.QueryRow(ctx, `SELECT slot FROM fleet_recovery_psks ORDER BY added_at ASC LIMIT 1`).Scan(&evictSlot)
		if err != nil {
			return 0, fmt.Errorf("pick eviction slot: %w", err)
		}
		if _, dErr := tx.Exec(ctx, `DELETE FROM fleet_recovery_psks WHERE slot = $1`, evictSlot); dErr != nil {
			return 0, fmt.Errorf("evict oldest: %w", dErr)
		}
		slot = evictSlot
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO fleet_recovery_psks (slot, fp, raw_psk, psk_hash, rotation_id)
		VALUES ($1, $2, $3, $4, $5)`,
		slot, fp, psk, int16(hash), rotationID)
	if err != nil {
		return 0, fmt.Errorf("insert recovery psk: %w", err)
	}
	if cErr := tx.Commit(ctx); cErr != nil {
		return 0, fmt.Errorf("commit insert: %w", cErr)
	}
	return slot, nil
}

// ListRecoveryPSKs returns every cached recovery PSK ordered by slot.
// Used by the dispatcher hook (rebuild in-memory hash table) and the
// startup reconcile (compare against Pi-Heltec slot state).
func (s *Store) ListRecoveryPSKs(ctx context.Context) ([]RecoveryPSKRecord, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT slot, fp, raw_psk, psk_hash, added_at, rotation_id::text
		  FROM fleet_recovery_psks
		 ORDER BY slot`)
	if err != nil {
		return nil, fmt.Errorf("list recovery psks: %w", err)
	}
	defer rows.Close()
	var out []RecoveryPSKRecord
	for rows.Next() {
		var r RecoveryPSKRecord
		var rotID *string
		if err := rows.Scan(&r.Slot, &r.FP, &r.PSK, &r.PSKHash, &r.AddedAt, &rotID); err != nil {
			return nil, fmt.Errorf("scan recovery psk: %w", err)
		}
		r.RotationID = rotID
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRecoveryPSKByFP fetches one cached PSK by fingerprint. Returns
// ErrNotFound if absent. Used by the recover_stranded job handler.
func (s *Store) GetRecoveryPSKByFP(ctx context.Context, fp string) (*RecoveryPSKRecord, error) {
	var r RecoveryPSKRecord
	var rotID *string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT slot, fp, raw_psk, psk_hash, added_at, rotation_id::text
		  FROM fleet_recovery_psks
		 WHERE fp = $1`, fp).
		Scan(&r.Slot, &r.FP, &r.PSK, &r.PSKHash, &r.AddedAt, &rotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get recovery psk: %w", err)
	}
	r.RotationID = rotID
	return &r, nil
}

// DeleteRecoveryPSKBySlot removes a cached entry by slot. Caller must
// SetChannel(slot, DISABLED, empty) on Pi-Heltec to wipe the firmware
// state. Used by the GC pass when no stranded node references the fp
// any longer.
func (s *Store) DeleteRecoveryPSKBySlot(ctx context.Context, slot int32) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM fleet_recovery_psks WHERE slot = $1`, slot)
	if err != nil {
		return fmt.Errorf("delete recovery psk: %w", err)
	}
	return nil
}

// --- stranded markers on fleet_node_trust ---

// MarkStranded records that the node failed to migrate during the most
// recent rotation. Sets stranded_since=now (idempotent — preserves the
// original timestamp on repeated calls), stamps previous_psk_fp, and
// resets recovery_attempts to 0 since this is a fresh strand event.
func (s *Store) MarkStranded(ctx context.Context, nodeNum uint32, prevPSKFP string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO fleet_node_trust (node_num, stranded_since, previous_psk_fp, recovery_attempts)
		VALUES ($1, now(), $2, 0)
		ON CONFLICT (node_num) DO UPDATE SET
			stranded_since   = COALESCE(fleet_node_trust.stranded_since, EXCLUDED.stranded_since),
			previous_psk_fp  = EXCLUDED.previous_psk_fp,
			recovery_attempts = 0,
			last_recovery_error = NULL`,
		int64(nodeNum), prevPSKFP)
	if err != nil {
		return fmt.Errorf("mark stranded: %w", err)
	}
	return nil
}

// ClearStranded zeros the stranded markers on a node, called by the
// recover_stranded job after a successful migrate. Leaves
// previous_psk_fp set as a record of which PSK the recovery used; the
// next stranding event will overwrite.
func (s *Store) ClearStranded(ctx context.Context, nodeNum uint32) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_node_trust SET
			stranded_since      = NULL,
			recovery_attempts   = 0,
			last_recovery_at    = now(),
			last_recovery_error = NULL
		 WHERE node_num = $1`,
		int64(nodeNum))
	if err != nil {
		return fmt.Errorf("clear stranded: %w", err)
	}
	return nil
}

// IncrementRecoveryAttempt bumps recovery_attempts and stamps
// last_recovery_at + last_recovery_error. Called on every recover job
// outcome regardless of success/failure (success calls ClearStranded
// instead, which zeros the counter).
func (s *Store) IncrementRecoveryAttempt(ctx context.Context, nodeNum uint32, errMsg string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_node_trust SET
			recovery_attempts   = recovery_attempts + 1,
			last_recovery_at    = now(),
			last_recovery_error = NULLIF($2, '')
		 WHERE node_num = $1`,
		int64(nodeNum), errMsg)
	if err != nil {
		return fmt.Errorf("increment recovery attempt: %w", err)
	}
	return nil
}

// ListStranded returns every node with stranded_since IS NOT NULL.
// Used by the periodic scan + Stranded Nodes UI section.
func (s *Store) ListStranded(ctx context.Context) ([]NodeTrustRecord, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT n.node_num,
		       COALESCE(n.node_id, ''),
		       COALESCE(n.long_name, ''),
		       COALESCE(n.short_name, ''),
		       COALESCE(n.sensor_short_id, ''),
		       n.last_heard,
		       COALESCE(n.is_online, false),
		       COALESCE(t.admin_key_fps, '[]'::jsonb),
		       COALESCE(t.is_managed, false),
		       t.last_verified_at,
		       COALESCE(t.last_verify_method, ''),
		       t.last_drift_check_at,
		       COALESCE(t.drift_status, 'unknown'),
		       COALESCE(t.current_psk_fp, ''),
		       COALESCE(t.previous_psk_fp, ''),
		       t.stranded_since,
		       COALESCE(t.recovery_attempts, 0),
		       t.last_recovery_at,
		       COALESCE(t.last_recovery_error, ''),
		       COALESCE(t.notes, '')
		  FROM fleet_node_trust t
		  JOIN nodes n ON n.node_num = t.node_num
		 WHERE t.stranded_since IS NOT NULL
		 ORDER BY t.stranded_since ASC`)
	if err != nil {
		return nil, fmt.Errorf("list stranded: %w", err)
	}
	defer rows.Close()
	var out []NodeTrustRecord
	for rows.Next() {
		var r NodeTrustRecord
		var nodeNum int64
		var fpsJSON []byte
		if err := rows.Scan(&nodeNum, &r.NodeID, &r.LongName, &r.ShortName,
			&r.SensorShortID, &r.LastHeard, &r.IsOnline, &fpsJSON,
			&r.IsManaged, &r.LastVerifiedAt, &r.LastVerifyMethod,
			&r.LastDriftCheckAt, &r.DriftStatus, &r.CurrentPSKFP,
			&r.PreviousPSKFP, &r.StrandedSince, &r.RecoveryAttempts,
			&r.LastRecoveryAt, &r.LastRecoveryError, &r.Notes); err != nil {
			return nil, fmt.Errorf("scan stranded: %w", err)
		}
		r.NodeNum = uint32(nodeNum)
		_ = fpsJSON // admin_key_fps unused for stranded listing
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountStrandedReferencingFP returns how many node trust rows still
// have previous_psk_fp = fp AND stranded_since IS NOT NULL. Used by
// the GC to decide whether a recovery cache slot can be reclaimed.
func (s *Store) CountStrandedReferencingFP(ctx context.Context, fp string) (int, error) {
	var n int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM fleet_node_trust
		 WHERE previous_psk_fp = $1 AND stranded_since IS NOT NULL`, fp).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count stranded references: %w", err)
	}
	return n, nil
}

// --- stranded GC ---

// GCStrandedOlderThan zeros previous_psk_fp + stranded_since on rows
// where stranded_since is older than the threshold. Returns the
// number of rows affected. Audit-log this in the caller. The recovery
// cache eviction is a separate concern (handled by the recovery cache
// FIFO in AddRecoveryPSK).
func (s *Store) GCStrandedOlderThan(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold)
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_node_trust SET
			previous_psk_fp = NULL,
			stranded_since  = NULL,
			recovery_attempts = 0,
			last_recovery_error = 'gc: stranded > ' || $1::text
		 WHERE stranded_since IS NOT NULL AND stranded_since < $2`,
		threshold.String(), cutoff)
	if err != nil {
		return 0, fmt.Errorf("gc stranded: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
