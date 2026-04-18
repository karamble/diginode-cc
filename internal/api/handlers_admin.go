package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/karamble/diginode-cc/internal/serial"
)

// handleGpsBroadcastToggle is a convenience wrapper around PUT /config/gpsBroadcastEnabled.
// It accepts {"enabled": bool} and returns {"enabled": bool, "applied": bool} — "applied"
// is true when the Heltec gps_mode push succeeded. Used by gotailme's connector.
func (s *Server) handleGpsBroadcastToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "request must include {\"enabled\": bool}")
		return
	}
	if err := s.svc.AppCfg.Set(r.Context(), "gpsBroadcastEnabled", *body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist gpsBroadcastEnabled")
		return
	}
	// Run the hardware push manually (same path as the generic PUT handler).
	applied := true
	if err := s.applyGpsBroadcast(r.Context(), jsonBool(*body.Enabled)); err != nil {
		slog.Warn("gps-broadcast hardware push failed", "error", err)
		applied = false
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": *body.Enabled,
		"applied": applied,
	})
}

// handleStatusBroadcastTrigger fires an immediate STATUS broadcast. Useful
// for verifying the feature after a toggle change without waiting for the
// next tick. Returns {"triggered": true/false} — false if a trigger was
// already queued.
func (s *Server) handleStatusBroadcastTrigger(w http.ResponseWriter, r *http.Request) {
	if s.svc.StatusBroadcast == nil {
		writeError(w, http.StatusServiceUnavailable, "status broadcaster not running")
		return
	}
	ok := s.svc.StatusBroadcast.Trigger()
	writeJSON(w, http.StatusOK, map[string]bool{"triggered": ok})
}

// jsonBool returns a JSON-encoded bool as json.RawMessage for side-effect
// handlers that expect their value pre-parsed from the PUT body.
func jsonBool(b bool) []byte {
	if b {
		return []byte("true")
	}
	return []byte("false")
}

// resetHeltecNodedb fires a nodedb-reset admin command to the local Heltec best-effort.
// Used by clear-operational and factory-reset to wipe the radio's on-device node table
// so the mesh node list doesn't immediately repopulate from the radio's own cache.
// Failures (serial not connected, local node num unknown) are logged but non-fatal —
// a wipe that half-succeeds is still better than one that refuses because the radio is offline.
func (s *Server) resetHeltecNodedb() {
	nodeNum := s.svc.Nodes.GetLocalNodeNum()
	if nodeNum == 0 {
		slog.Warn("skipping Heltec nodedb reset: local node number unknown")
		return
	}
	if err := s.serialMgr.SendToRadio(serial.BuildAdminNodedbReset(nodeNum)); err != nil {
		slog.Warn("Heltec nodedb reset send failed", "error", err)
		return
	}
	slog.Info("Heltec nodedb reset sent", "nodeNum", nodeNum)
}

// handleDatabaseStats returns row counts and cache sizes for the Data Management panel.
func (s *Server) handleDatabaseStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := s.svc.DB().Pool

	countTable := func(table string) int64 {
		var n int64
		db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
		return n
	}

	stats := map[string]interface{}{
		"drones":           countTable("drones"),
		"drone_detections": countTable("drone_detections"),
		"nodes":            countTable("nodes"),
		"node_positions":   countTable("node_positions"),
		"targets":          countTable("targets"),
		"target_positions": countTable("target_positions"),
		"inventory":        countTable("inventory_devices"),
		"alert_rules":      countTable("alert_rules"),
		"alert_events":     countTable("alert_events"),
		"commands":         countTable("commands"),
		"chat_messages":    countTable("chat_messages"),
		"geofences":        countTable("geofences"),
		"webhooks":         countTable("webhooks"),
		"users":            countTable("users"),
		"audit_log":        countTable("audit_log"),
		"tile_cache_bytes": getDirSize("data/tiles"),
	}
	writeJSON(w, http.StatusOK, stats)
}

// getDirSize returns the total size in bytes of all files under a directory.
func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// handleClearDetectionData wipes all detection data (drones, targets, inventory,
// positions, detections) while preserving config, users, rules, and geofences.
func (s *Server) handleClearDetectionData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var errs []string

	if err := s.svc.Drones.ClearAll(ctx); err != nil {
		errs = append(errs, "drones: "+err.Error())
	}
	if err := s.svc.Targets.ClearAll(ctx); err != nil {
		errs = append(errs, "targets: "+err.Error())
	}
	if err := s.svc.Inventory.ClearAll(ctx); err != nil {
		errs = append(errs, "inventory: "+err.Error())
	}

	// Clear position history tables
	s.svc.DB().Pool.Exec(ctx, `DELETE FROM drone_detections`)
	s.svc.DB().Pool.Exec(ctx, `DELETE FROM target_positions`)
	s.svc.DB().Pool.Exec(ctx, `DELETE FROM node_positions`)

	if len(errs) > 0 {
		writeError(w, http.StatusInternalServerError, "partial failure: "+errs[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "cleared": "drones, targets, inventory, positions, detections"})
}

// handleClearOperationalData wipes all operational data: detection data +
// chat, commands, alerts, audit log, and the mesh node list. Keeps users,
// sites, config, rules, geofences. Also fires a nodedb-reset admin command
// to the Heltec so the radio's on-device cache doesn't immediately replay
// the old nodes back into our table.
func (s *Server) handleClearOperationalData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Reset the Heltec first so any NodeInfo traffic it replays after our
	// DELETE goes into a freshly wiped on-device table.
	s.resetHeltecNodedb()

	// In-memory + table clears via services (these also wipe caches)
	s.svc.Drones.ClearAll(ctx)
	s.svc.Targets.ClearAll(ctx)
	s.svc.Inventory.ClearAll(ctx)
	s.svc.Nodes.ClearAll(ctx)

	// History tables that have no service-level clear method
	tables := []string{
		"drone_detections", "target_positions", "node_positions",
		"chat_messages", "commands", "alert_events", "audit_log",
	}
	for _, t := range tables {
		s.svc.DB().Pool.Exec(ctx, `DELETE FROM `+t)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "cleared": "all operational data"})
}

// handlePruneOldData deletes records older than the specified retention period.
func (s *Server) handlePruneOldData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Days int `json:"days"`
	}
	if err := readJSON(r, &body); err != nil || body.Days <= 0 {
		body.Days = 30
	}

	interval := body.Days
	tables := []struct {
		name string
		col  string
	}{
		{"drone_detections", "timestamp"},
		{"target_positions", "timestamp"},
		{"node_positions", "timestamp"},
		{"chat_messages", "timestamp"},
		{"commands", "created_at"},
		{"alert_events", "created_at"},
		{"audit_log", "timestamp"},
	}

	total := int64(0)
	for _, t := range tables {
		result, _ := s.svc.DB().Pool.Exec(ctx,
			`DELETE FROM `+t.name+` WHERE `+t.col+` < NOW() - $1 * INTERVAL '1 day'`, interval)
		total += result.RowsAffected()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"days":    interval,
		"deleted": total,
	})
}

// handleFactoryReset wipes ALL data and re-seeds the default admin user.
// This is a destructive operation — everything except the schema is deleted.
func (s *Server) handleFactoryReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil || body.Confirm != "FACTORY_RESET" {
		writeError(w, http.StatusBadRequest, "must send {\"confirm\":\"FACTORY_RESET\"}")
		return
	}

	// Wipe the Heltec's on-device node database before clearing our tables.
	// Best-effort: a factory reset proceeds even if the radio is unreachable.
	s.resetHeltecNodedb()

	// Order matters: delete dependent tables first
	tables := []string{
		"drone_detections", "target_positions", "node_positions",
		"webhook_deliveries",
		"alert_events", "chat_messages", "commands", "audit_log",
		"drones", "targets", "inventory_devices",
		"nodes",
		"alert_rules", "geofences", "webhooks", "alarm_configs",
		"firewall_rules", "faa_registry",
		"password_resets", "invitations",
		"app_config",
		"users", "sites",
	}

	for _, t := range tables {
		s.svc.DB().Pool.Exec(ctx, `DELETE FROM `+t)
	}

	// Re-seed default admin (same as migration 000009)
	s.svc.DB().Pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		SELECT 'admin@example.com',
			'$2a$10$pdc.F5coo6FIwTvkD4IBUODFYY9/7QSXUcZWPvn9DKz8gTGS.OZ6q',
			'Admin', 'ADMIN'
		WHERE NOT EXISTS (SELECT 1 FROM users)`)

	// Clear in-memory caches (DELETEs above already emptied their tables)
	s.svc.Drones.ClearAll(ctx)
	s.svc.Targets.ClearAll(ctx)
	s.svc.Inventory.ClearAll(ctx)
	s.svc.Nodes.ClearAll(ctx)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "factory reset complete, default admin restored"})
}
