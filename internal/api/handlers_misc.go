package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/alarms"
	"github.com/karamble/diginode-cc/internal/firewall"
)

// ---- Alarms ----

func (s *Server) handleListAlarms(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Alarms.GetAll()
	if list == nil {
		list = []*alarms.AlarmConfig{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateAlarm(w http.ResponseWriter, r *http.Request) {
	var a alarms.AlarmConfig
	if err := readJSON(r, &a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Alarms.Create(r.Context(), &a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create alarm")
		return
	}

	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleUpdateAlarm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var a alarms.AlarmConfig
	if err := readJSON(r, &a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Alarms.Update(r.Context(), id, &a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update alarm")
		return
	}

	a.ID = id
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAlarm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Alarms.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete alarm")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Firewall ----

func (s *Server) handleListFirewallRules(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Firewall.GetRules()
	if list == nil {
		list = []*firewall.Rule{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateFirewallRule(w http.ResponseWriter, r *http.Request) {
	var rule firewall.Rule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Firewall.CreateRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create firewall rule")
		return
	}

	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleDeleteFirewallRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Firewall.DeleteRule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete firewall rule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- FAA ----

func (s *Server) handleFAALookup(w http.ResponseWriter, r *http.Request) {
	serial := chi.URLParam(r, "serial")
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial number is required")
		return
	}

	entry, err := s.svc.FAA.Lookup(r.Context(), serial)
	if err != nil {
		writeError(w, http.StatusNotFound, "FAA record not found")
		return
	}

	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleFAAImport(w http.ResponseWriter, r *http.Request) {
	// Limit upload size to 100MB
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file upload required (form field: file)")
		return
	}
	defer file.Close()

	count, err := s.svc.FAA.ImportCSV(r.Context(), file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "import failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"imported": count,
		"message":  fmt.Sprintf("imported %d FAA records", count),
	})
}

// handleFAAStatus returns the FAA registry status (count + last import time).
func (s *Server) handleFAAStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.svc.FAA.GetStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get FAA status")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleFAASync triggers a background FAA import from a configured URL.
func (s *Server) handleFAASync(w http.ResponseWriter, r *http.Request) {
	// Check if a sync URL is configured in app_config
	var syncCfg struct {
		URL string `json:"url"`
	}
	_ = s.svc.AppCfg.GetTyped("faa_sync", &syncCfg)

	if syncCfg.URL == "" {
		writeError(w, http.StatusNotImplemented,
			"FAA sync URL not configured — set app_config key 'faa_sync' with {\"url\": \"...\"}")
		return
	}

	// Start a background download and import (use background context since
	// the HTTP request context will be canceled after we return).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", syncCfg.URL, nil)
		if err != nil {
			slog.Error("FAA sync: failed to create request", "error", err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("FAA sync: download failed", "error", err)
			return
		}
		defer resp.Body.Close()
		count, err := s.svc.FAA.ImportCSV(ctx, resp.Body)
		if err != nil {
			slog.Error("FAA sync: import failed", "error", err)
			return
		}
		slog.Info("FAA sync completed", "records", count)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "ok",
		"message": "FAA sync started in background",
	})
}

// handleFAAUpload accepts a multipart MASTER.txt file upload (up to 250MB) and imports it.
func (s *Server) handleFAAUpload(w http.ResponseWriter, r *http.Request) {
	// Limit upload size to 250MB
	r.Body = http.MaxBytesReader(w, r.Body, 250<<20)

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file upload required (form field: file)")
		return
	}
	defer file.Close()

	count, err := s.svc.FAA.ImportCSV(r.Context(), file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "import failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"imported": count,
		"message":  fmt.Sprintf("imported %d FAA records", count),
	})
}

// ---- Exports ----

func (s *Server) handleExportDrones(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=drones.csv")

	if err := s.svc.Exports.ExportDronesCSV(r.Context(), w); err != nil {
		writeError(w, http.StatusInternalServerError, "export failed")
	}
}

func (s *Server) handleExportNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=nodes.json")

	if err := s.svc.Exports.ExportNodesJSON(r.Context(), w); err != nil {
		writeError(w, http.StatusInternalServerError, "export failed")
	}
}

func (s *Server) handleExportAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=alerts.csv")

	if err := s.svc.Exports.ExportAlertsCSV(r.Context(), w); err != nil {
		writeError(w, http.StatusInternalServerError, "export failed")
	}
}

// ---- Alarm Sounds ----

// handleUploadAlarmSound accepts a multipart audio file upload (5MB max, .wav/.mp3/.ogg)
// for the given alarm level and stores the reference.
func (s *Server) handleUploadAlarmSound(w http.ResponseWriter, r *http.Request) {
	level := chi.URLParam(r, "level")

	// Validate level
	validLevels := map[string]bool{"INFO": true, "NOTICE": true, "ALERT": true, "CRITICAL": true}
	if !validLevels[strings.ToUpper(level)] {
		writeError(w, http.StatusBadRequest, "invalid alarm level — must be INFO, NOTICE, ALERT, or CRITICAL")
		return
	}
	level = strings.ToUpper(level)

	// Limit upload to 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file upload required (form field: file)")
		return
	}
	defer file.Close()

	// Validate extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	validExts := map[string]bool{".wav": true, ".mp3": true, ".ogg": true}
	if !validExts[ext] {
		writeError(w, http.StatusBadRequest, "unsupported file type — must be .wav, .mp3, or .ogg")
		return
	}

	// Read the file data (we store the filename; in a full implementation you'd
	// write to disk or object storage, but here we store just the reference).
	_, err = io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file")
		return
	}

	soundFile := fmt.Sprintf("alarm_%s%s", strings.ToLower(level), ext)

	if err := s.svc.Alarms.SetSoundFile(r.Context(), level, soundFile); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save alarm sound")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"level":     level,
		"soundFile": soundFile,
	})
}

// handleDeleteAlarmSound removes the sound file for the given alarm level.
func (s *Server) handleDeleteAlarmSound(w http.ResponseWriter, r *http.Request) {
	level := chi.URLParam(r, "level")

	validLevels := map[string]bool{"INFO": true, "NOTICE": true, "ALERT": true, "CRITICAL": true}
	if !validLevels[strings.ToUpper(level)] {
		writeError(w, http.StatusBadRequest, "invalid alarm level — must be INFO, NOTICE, ALERT, or CRITICAL")
		return
	}
	level = strings.ToUpper(level)

	if err := s.svc.Alarms.DeleteSoundFile(r.Context(), level); err != nil {
		if alarms.IsAlarmSoundNotFound(err) {
			writeError(w, http.StatusNotFound, "no sound file found for level: "+level)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete alarm sound")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Export by Type ----

// handleExportByType is a catch-all export endpoint that delegates to the
// specific export method based on the {type} URL parameter.
func (s *Server) handleExportByType(w http.ResponseWriter, r *http.Request) {
	exportType := chi.URLParam(r, "type")

	switch strings.ToLower(exportType) {
	case "drones":
		s.handleExportDrones(w, r)
	case "nodes":
		s.handleExportNodes(w, r)
	case "alerts":
		s.handleExportAlerts(w, r)
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported export type: %s — supported types: drones, nodes, alerts", exportType))
	}
}

// ---- System Update ----

func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	// In production, updates are handled by Watchtower polling Docker Hub.
	// This endpoint signals that an update check has been requested.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "update check requested — Watchtower will pull the latest image on next poll",
	})
}
