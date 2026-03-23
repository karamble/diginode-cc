package api

import (
	"net/http"
)

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
// chat, commands, alerts, audit log. Keeps users, sites, config, rules, geofences.
func (s *Server) handleClearOperationalData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Detection data
	s.svc.Drones.ClearAll(ctx)
	s.svc.Targets.ClearAll(ctx)
	s.svc.Inventory.ClearAll(ctx)

	// History tables
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

	// Clear in-memory caches
	s.svc.Drones.ClearAll(ctx)
	s.svc.Targets.ClearAll(ctx)
	s.svc.Inventory.ClearAll(ctx)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "factory reset complete, default admin restored"})
}
