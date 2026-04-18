package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/karamble/diginode-cc/internal/serial"
)

// configSideEffects maps a config key to a hardware/runtime action that
// should run after AppCfg.Set succeeds. Failures are logged but don't fail
// the HTTP request — the config value is saved, and the next toggle attempt
// re-tries the hardware push. Keeps UI responsiveness + eventual convergence.
var configSideEffects = map[string]func(*Server, context.Context, json.RawMessage) error{
	"gpsBroadcastEnabled": (*Server).applyGpsBroadcast,
}

// applyGpsBroadcast flips the Heltec's gps_mode to match the boolean value:
// true  → NOT_PRESENT (2), Heltec accepts externally fed positions + broadcasts them
// false → DISABLED (0), Heltec ignores the position service entirely
// This replaces what gotailme's connector used to do directly.
func (s *Server) applyGpsBroadcast(ctx context.Context, raw json.RawMessage) error {
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		return err
	}
	nodeNum := s.svc.Nodes.GetLocalNodeNum()
	if nodeNum == 0 {
		return errors.New("local node number not yet known")
	}
	var gpsMode uint32 // 0 = DISABLED
	if enabled {
		gpsMode = 2 // NOT_PRESENT
	}
	return s.serialMgr.SendToRadio(serial.BuildAdminPositionConfig(nodeNum, gpsMode))
}

// runConfigSideEffects invokes the side-effect hook for a given key if one
// is registered. Errors are logged; the caller continues.
func (s *Server) runConfigSideEffects(ctx context.Context, key string, raw json.RawMessage) {
	fn, ok := configSideEffects[key]
	if !ok {
		return
	}
	if err := fn(s, ctx, raw); err != nil {
		slog.Warn("config side-effect failed", "key", key, "error", err)
	}
}

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
		s.runConfigSideEffects(r.Context(), key, value)
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

	// Parse value as raw JSON so we can both store it (via AppCfg.Set which
	// re-marshals) AND pass the original bytes to any side-effect hook.
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var parsed interface{}
	if err := json.Unmarshal(body.Value, &parsed); err != nil {
		writeError(w, http.StatusBadRequest, "invalid value")
		return
	}

	if err := s.svc.AppCfg.Set(r.Context(), key, parsed); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set config key")
		return
	}

	s.runConfigSideEffects(r.Context(), key, body.Value)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
