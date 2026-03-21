package api

import (
	"net/http"
)

func (s *Server) handleACARSStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": s.cfg.ACARSEnabled,
		"port":    s.cfg.ACARSPort,
	})
}

func (s *Server) handleGetACARSMessages(w http.ResponseWriter, r *http.Request) {
	if s.svc.ACARS == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	msgs := s.svc.ACARS.GetMessages(1000)
	if msgs == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleClearACARSMessages(w http.ResponseWriter, r *http.Request) {
	if s.svc.ACARS != nil {
		s.svc.ACARS.ClearMessages()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUpdateACARSConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.svc.AppCfg.Set(r.Context(), "acars.enabled", req.Enabled)
	s.svc.AppCfg.Set(r.Context(), "acars.port", req.Port)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
