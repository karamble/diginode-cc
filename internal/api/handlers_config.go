package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	all := s.svc.AppCfg.GetAll()
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for key, value := range body {
		var v interface{}
		if err := json.Unmarshal(value, &v); err != nil {
			writeError(w, http.StatusBadRequest, "invalid value for key: "+key)
			return
		}
		if err := s.svc.AppCfg.Set(r.Context(), key, v); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set config key: "+key)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetConfigKey(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")

	value, ok := s.svc.AppCfg.Get(key)
	if !ok {
		writeError(w, http.StatusNotFound, "config key not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"value": value})
}

func (s *Server) handleUpdateConfigKey(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")

	var body struct {
		Value interface{} `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.AppCfg.Set(r.Context(), key, body.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set config key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
