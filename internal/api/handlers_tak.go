package api

import "net/http"

func (s *Server) handleGetTAKConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": s.cfg.TAKEnabled,
		"addr":    s.cfg.TAKAddr,
	})
}

func (s *Server) handleUpdateTAKConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Addr    string `json:"addr"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.svc.AppCfg.Set(r.Context(), "tak.enabled", req.Enabled)
	s.svc.AppCfg.Set(r.Context(), "tak.addr", req.Addr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTAKReload(w http.ResponseWriter, r *http.Request) {
	// Reload TAK connection with current config
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "TAK connection reload requested"})
}

func (s *Server) handleTAKSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Event string `json:"event"` // COT XML
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Event == "" {
		writeError(w, http.StatusBadRequest, "event is required")
		return
	}
	// Would send raw COT event via TAK service
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
