package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/auth"
)

func (s *Server) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	rules := s.svc.Alerts.GetRules()
	if rules == nil {
		rules = []*alerts.Rule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule alerts.Rule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Alerts.CreateRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create alert rule")
		return
	}

	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var rule alerts.Rule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Alerts.UpdateRule(r.Context(), id, &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update alert rule")
		return
	}

	rule.ID = id
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Alerts.DeleteRule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete alert rule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListAlertEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	events, err := s.svc.Alerts.GetEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alert events")
		return
	}

	if events == nil {
		events = []*alerts.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")

	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := s.svc.Alerts.Acknowledge(r.Context(), eventID, claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge alert")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
