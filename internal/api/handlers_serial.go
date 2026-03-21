package api

import (
	"net/http"
	"strconv"
)

// handleGetTextMessages returns text messages since a given sequence number.
// This is polled by gotailme for inter-system messaging (CC PRO compat).
func (s *Server) handleGetTextMessages(w http.ResponseWriter, r *http.Request) {
	sinceSeq := int64(0)
	if q := r.URL.Query().Get("sinceSeq"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			sinceSeq = v
		}
	}

	messages := s.serialMgr.GetTextMessages(sinceSeq)
	if messages == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleGetDeviceTime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hasTime":    false,
		"deviceTime": nil,
		"ageSeconds": 0,
	})
}

func (s *Server) handleGetSerialConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"devicePath": s.cfg.SerialDevice,
		"baud":       s.cfg.SerialBaud,
		"enabled":    s.cfg.SerialDevice != "",
	})
}

func (s *Server) handleUpdateSerialConfig(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSerialConnect(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSerialDisconnect(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialTextMessage(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialTextAlert(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialPosition(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialDeviceMetrics(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialDisplayConfig(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSendSerialShutdown(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleSerialSimulate(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
