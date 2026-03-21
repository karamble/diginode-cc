package api

import (
	"fmt"
	"net/http"

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

// ---- System Update ----

func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	// In production, updates are handled by Watchtower polling Docker Hub.
	// This endpoint signals that an update check has been requested.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "update check requested — Watchtower will pull the latest image on next poll",
	})
}
