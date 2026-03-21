package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---- Check Update ----

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if s.svc.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available")
		return
	}

	available, message, err := s.svc.Updates.CheckUpdate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check for updates: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available":      available,
		"currentVersion": s.svc.Updates.CurrentVersion(),
		"message":        message,
		"checkedAt":      time.Now().UTC(),
	})
}

// ---- Update Status ----

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.svc.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"currentVersion": s.svc.Updates.CurrentVersion(),
		"status":         "idle",
		"lastChecked":    time.Now().UTC(),
	})
}

// ---- Trigger Update ----

func (s *Server) handleTriggerUpdate(w http.ResponseWriter, r *http.Request) {
	if s.svc.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available")
		return
	}

	// Run update in background
	go func() {
		if err := s.svc.Updates.ApplyUpdate(); err != nil {
			slog.Error("background update failed", "error", err)
		} else {
			slog.Info("update applied successfully")
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "updating",
		"message": "update triggered — applying in background",
	})
}

// ---- Update History ----

func (s *Server) handleUpdateHistory(w http.ResponseWriter, r *http.Request) {
	// Update history would require a dedicated database table (update_log).
	// For now, return the current version as the only history entry.
	writeJSON(w, http.StatusOK, []map[string]interface{}{
		{
			"version":   s.svc.Updates.CurrentVersion(),
			"appliedAt": time.Now().UTC(),
			"status":    "current",
		},
	})
}

// ---- Rollback Update ----

func (s *Server) handleRollbackUpdate(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")

	writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
		"status":  "error",
		"message": "rollback is not supported — use git revert or redeploy a previous version",
	})
}
