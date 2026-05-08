package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/fleetsec"
)

// Routes registered under /api/fleet-security in setupRoutes (server.go).
// Read endpoints require OPERATOR or higher; mutating endpoints require
// ADMIN. The route group itself sits inside the JWT-authenticated /api
// subtree -- public requests never reach these handlers.

// ---- Identity ----

func (s *Server) handleFleetSecGetIdentity(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	id, err := s.svc.FleetSec.GetIdentity(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, id)
}

func (s *Server) handleFleetSecExportPubkey(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	b64, fp, err := s.svc.FleetSec.ExportPubkey(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"publicKeyB64": b64,
		"fingerprint":  fp,
	})
}

func (s *Server) handleFleetSecImportIdentity(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	var body struct {
		Label   string `json:"label"`
		PrivB64 string `json:"privB64"`
		PubB64  string `json:"pubB64"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Label == "" || body.PrivB64 == "" || body.PubB64 == "" {
		writeError(w, http.StatusBadRequest, "label, privB64, pubB64 are all required")
		return
	}
	priv, err := fleetsec.DecodePrivkeyB64(body.PrivB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid privkey: "+err.Error())
		return
	}
	defer fleetsec.NewSecret(priv).Clear()
	pub, err := fleetsec.DecodePubkeyB64(body.PubB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid pubkey: "+err.Error())
		return
	}
	rec, err := s.svc.FleetSec.ImportIdentity(r.Context(), userIDFromCtx(r), body.Label, priv, pub)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleFleetSecListIdentities(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	out, err := s.svc.FleetSec.ListIdentities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []fleetsec.IdentityRecord{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFleetSecRegisterIdentity(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	var body struct {
		Label  string                `json:"label"`
		PubB64 string                `json:"pubB64"`
		Role   fleetsec.IdentityRole `json:"role"`
	}
	if err := readJSON(r, &body); err != nil || body.Label == "" || body.PubB64 == "" {
		writeError(w, http.StatusBadRequest, "label, pubB64, role required")
		return
	}
	pub, err := fleetsec.DecodePubkeyB64(body.PubB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid pubkey: "+err.Error())
		return
	}
	rec, err := s.svc.FleetSec.RegisterIdentity(r.Context(), userIDFromCtx(r), body.Label, pub, body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleFleetSecRevokeIdentity(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	fp := chi.URLParam(r, "fingerprint")
	if fp == "" {
		writeError(w, http.StatusBadRequest, "fingerprint required")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = readJSON(r, &body) // body optional
	if err := s.svc.FleetSec.RevokeIdentity(r.Context(), userIDFromCtx(r), fp, body.Reason); err != nil {
		if errors.Is(err, fleetsec.ErrNotFound) {
			writeError(w, http.StatusNotFound, "identity not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// ---- Trust roster ----

func (s *Server) handleFleetSecListTrust(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	out, err := s.svc.FleetSec.ListTrust(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []fleetsec.NodeTrustRecord{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFleetSecGetTrust(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	num, ok := parseNodeNumParam(w, r)
	if !ok {
		return
	}
	out, err := s.svc.FleetSec.GetTrust(r.Context(), num)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFleetSecVerifyTrust(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	num, ok := parseNodeNumParam(w, r)
	if !ok {
		return
	}
	res := s.svc.FleetSec.VerifyTrust(r.Context(), num)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleFleetSecSetAdminKeys(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	num, ok := parseNodeNumParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Keys  []string `json:"keys"` // fingerprints
		Force bool     `json:"force"`
		Ack   string   `json:"ack"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	err := s.svc.FleetSec.SetAdminKeys(r.Context(), userIDFromCtx(r), num, body.Keys, fleetsec.SetAdminKeysOpts{
		Force: body.Force,
		Ack:   body.Ack,
	})
	if err != nil {
		// Lockout-prevention errors are user errors → 400; everything
		// else is upstream → 502.
		switch {
		case errors.Is(err, fleetsec.ErrLockoutPrevented), errors.Is(err, fleetsec.ErrInvalidAck):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"applied": true})
}

func (s *Server) handleFleetSecSetIsManaged(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	num, ok := parseNodeNumParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Value bool   `json:"value"`
		Force bool   `json:"force"`
		Ack   string `json:"ack"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	err := s.svc.FleetSec.SetIsManaged(r.Context(), userIDFromCtx(r), num, body.Value, fleetsec.SetIsManagedOpts{
		Force: body.Force,
		Ack:   body.Ack,
	})
	if err != nil {
		switch {
		case errors.Is(err, fleetsec.ErrManagedLockdownPrevented), errors.Is(err, fleetsec.ErrInvalidAck):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"applied": true})
}

// ---- Helpers ----

func parseNodeNumParam(w http.ResponseWriter, r *http.Request) (uint32, bool) {
	raw := chi.URLParam(r, "nodeNum")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "nodeNum required")
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		writeError(w, http.StatusBadRequest, "nodeNum must be a non-zero unsigned 32-bit integer")
		return 0, false
	}
	return uint32(n), true
}

// userIDFromCtx extracts the JWT subject (user UUID) from the request
// context. Returns the synthetic "service" id for machine tokens and ""
// if no claims are present (shouldn't happen since the route group sits
// inside the auth middleware).
func userIDFromCtx(r *http.Request) string {
	c := auth.GetClaims(r.Context())
	if c == nil {
		return ""
	}
	return c.UserID
}

// jsonReadable is a small helper to detect empty / whitespace-only
// request bodies so the optional-body handlers (RevokeIdentity) don't
// trip over a missing body. Re-uses encoding/json's behaviour.
func bodyIsEmpty(r *http.Request) bool {
	dec := json.NewDecoder(r.Body)
	return !dec.More()
}
