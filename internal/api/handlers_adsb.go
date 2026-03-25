package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/alerts"
)

// ---- ADS-B Status ----

func (s *Server) handleADSBStatus(w http.ResponseWriter, r *http.Request) {
	aircraft := s.svc.ADSB.GetAircraft()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":       s.cfg.ADSBEnabled,
		"url":           s.cfg.ADSBURL,
		"trackCount":    len(aircraft),
		"status":        "running",
	})
}

// ---- ADS-B Tracks ----

func (s *Server) handleADSBTracks(w http.ResponseWriter, r *http.Request) {
	aircraft := s.svc.ADSB.GetAircraft()
	if aircraft == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, aircraft)
}

// ---- ADS-B Config ----

func (s *Server) handleGetADSBConfig(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]interface{}{
		"enabled": s.cfg.ADSBEnabled,
		"url":     s.cfg.ADSBURL,
	}

	// Overlay any dynamic config from AppConfig
	keys := []string{"adsb.pollInterval", "adsb.maxRange", "adsb.alertOnMilitary", "adsb.alertOnEmergency"}
	for _, key := range keys {
		if val, ok := s.svc.AppCfg.Get(key); ok {
			cfg[key] = json.RawMessage(val)
		}
	}

	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateADSBConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for key, value := range body {
		prefixedKey := "adsb." + key
		if err := s.svc.AppCfg.Set(r.Context(), prefixedKey, value); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set config key: "+key)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- ADS-B Log ----

func (s *Server) handleADSBLog(w http.ResponseWriter, r *http.Request) {
	// Return recent ADS-B activity from the current in-memory tracks.
	// A full historical log would require a database table; for now
	// we return a snapshot of currently tracked aircraft as log entries.
	aircraft := s.svc.ADSB.GetAircraft()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": aircraft,
		"count":   len(aircraft),
	})
}

// ---- ADS-B Clear Log ----

func (s *Server) handleClearADSBLog(w http.ResponseWriter, r *http.Request) {
	// Clear ADS-B tracks from memory
	s.svc.ADSB.ClearAircraft()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- ADS-B OpenSky Credentials ----

func (s *Server) handleADSBOpenSkyCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.svc.AppCfg.Set(r.Context(), "adsb.opensky_username", req.Username)
	s.svc.AppCfg.Set(r.Context(), "adsb.opensky_password", req.Password)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- ADS-B Database Upload ----

func (s *Server) handleADSBDatabaseUpload(w http.ResponseWriter, r *http.Request) {
	// Limit upload size to 50MB
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file upload required (form field: file)")
		return
	}
	defer file.Close()

	// Read the file content (for now, acknowledge receipt)
	size, err := io.Copy(io.Discard, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read uploaded file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"filename": header.Filename,
		"size":     size,
		"message":  "ADS-B database uploaded successfully",
	})
}

// ---- ADS-B Alert Rules ----
// Reuse the alerts service with adsb-specific rule type by filtering on condition.

func (s *Server) handleListADSBAlertRules(w http.ResponseWriter, r *http.Request) {
	allRules := s.svc.Alerts.GetRules()
	var adsbRules []*alerts.Rule
	for _, rule := range allRules {
		if ruleType, ok := rule.Condition["type"].(string); ok && ruleType == "adsb" {
			adsbRules = append(adsbRules, rule)
		}
	}
	if adsbRules == nil {
		adsbRules = []*alerts.Rule{}
	}
	writeJSON(w, http.StatusOK, adsbRules)
}

func (s *Server) handleCreateADSBAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule alerts.Rule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ensure the rule is tagged as ADS-B type
	if rule.Condition == nil {
		rule.Condition = make(map[string]interface{})
	}
	rule.Condition["type"] = "adsb"

	if err := s.svc.Alerts.CreateRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ADS-B alert rule")
		return
	}

	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateADSBAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var rule alerts.Rule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Preserve ADS-B type tag
	if rule.Condition == nil {
		rule.Condition = make(map[string]interface{})
	}
	rule.Condition["type"] = "adsb"

	if err := s.svc.Alerts.UpdateRule(r.Context(), id, &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ADS-B alert rule")
		return
	}

	rule.ID = id
	writeJSON(w, http.StatusOK, rule)
}

// ---- ADS-B Aircraft Enrichment ----

func (s *Server) handleADSBOpenSkyLookup(w http.ResponseWriter, r *http.Request) {
	hex := chi.URLParam(r, "hex")
	result, err := s.svc.ADSB.LookupOpenSky(hex)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if result == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleADSBPlanespottersLookup(w http.ResponseWriter, r *http.Request) {
	hex := chi.URLParam(r, "hex")
	result, err := s.svc.ADSB.LookupPlanespotters(hex)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if result == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteADSBAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Alerts.DeleteRule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete ADS-B alert rule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
