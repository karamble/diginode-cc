package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

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
	res := s.svc.FleetSec.VerifyTrust(r.Context(), userIDFromCtx(r), num)
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

// ---- Channels + PSK rotation ----

func (s *Server) handleFleetSecListChannels(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	out, err := s.svc.FleetSec.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []fleetsec.ChannelRecord{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFleetSecRotatePSK(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	idx, err := strconv.ParseInt(chi.URLParam(r, "idx"), 10, 32)
	if err != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, "channel index must be a non-negative integer")
		return
	}
	var body struct {
		Source           string   `json:"source"`            // "random" | "explicit"
		PSKBase64        string   `json:"pskB64"`            // when source=explicit
		Targets          []uint32 `json:"targets"`           // remote node nums
		Ack              string   `json:"ack"`               // must be "ROTATE"
		Notes            string   `json:"notes"`
		InterTargetDelayMs int    `json:"interTargetDelayMs"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var psk []byte
	switch body.Source {
	case "random":
		// Generate a random PSK whose channel hash doesn't collide
		// with any active Pi-Heltec channel (PRIMARY + recovery cache).
		// 8 attempts is deep overkill at ~7 active channels in 256
		// hash buckets but cheap.
		psk, err = s.svc.FleetSec.GenerateRandomPSKAvoidCollision(r.Context(), 16, 8)
	case "explicit":
		psk, err = fleetsec.DecodePubkeyB64(body.PSKBase64)
		// DecodePubkeyB64 enforces 32-byte length; for 16-byte PSKs the
		// operator must supply via random. (A future iteration could add
		// a DecodePSKB64 with looser length checks.)
		if err != nil {
			err = fmt.Errorf("explicit PSK must be a 32-byte base64 value: %w", err)
		}
	default:
		writeError(w, http.StatusBadRequest, "source must be \"random\" or \"explicit\"")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer fleetsec.NewSecret(psk).Clear()

	delay := time.Duration(body.InterTargetDelayMs) * time.Millisecond

	id, err := s.svc.FleetSec.RotatePSK(r.Context(), userIDFromCtx(r),
		int32(idx), psk, body.Targets, fleetsec.RotatePSKOpts{
			Ack:              body.Ack,
			Notes:            body.Notes,
			InterTargetDelay: delay,
		})
	if err != nil {
		switch {
		case errors.Is(err, fleetsec.ErrInvalidAck):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"rotationId": id,
	})
}

func (s *Server) handleFleetSecGetRotation(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rotation id required")
		return
	}
	rec, err := s.svc.FleetSec.GetRotation(r.Context(), id)
	if err != nil {
		if errors.Is(err, fleetsec.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rotation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleFleetSecRetryRotation(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rotation id required")
		return
	}
	var body struct {
		PSKBase64 string   `json:"pskB64"`
		Targets   []uint32 `json:"targets"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// pskB64 is optional. When empty the service picks up the PSK
	// stashed at rotation-start (cleared once all targets reach
	// acked), which is the common case from the UI's Retry button.
	// When supplied it must decode to a valid Meshtastic PSK length
	// (16 or 32 bytes) -- DecodePSKB64 enforces that.
	var psk []byte
	if body.PSKBase64 != "" {
		var err error
		psk, err = fleetsec.DecodePSKB64(body.PSKBase64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "PSK decode: "+err.Error())
			return
		}
		defer fleetsec.NewSecret(psk).Clear()
	}

	if err := s.svc.FleetSec.RetryRotation(r.Context(), userIDFromCtx(r), id, psk, body.Targets); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// handleFleetSecRetireOldPSK runs Phase E for a completed staged
// rotation: disables the old SECONDARY slot on the local Heltec and
// (best-effort) on each fleet member that's still reachable. Gated on
// every managed-trust row's current_psk_fp matching the rotation's new
// fingerprint -- if any node is still on a stale fingerprint, returns
// 409 with the laggards list rather than the lossy 200 the UI would
// have to second-guess.
//
// Idempotent: a rotation already retired returns 409.
func (s *Server) handleFleetSecRetireOldPSK(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rotation id required")
		return
	}
	var body struct {
		Ack string `json:"ack"`
	}
	if err := readJSON(r, &body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Ack != "RETIRE" {
		writeError(w, http.StatusBadRequest, `ack must be "RETIRE" -- typed confirmation is required because retiring removes the old PSK and any laggard not yet on the new PSK loses comms permanently`)
		return
	}

	res, err := s.svc.FleetSec.RetireOldPSK(r.Context(), userIDFromCtx(r), id)
	if err != nil {
		switch {
		case errors.Is(err, fleetsec.ErrNotFound):
			writeError(w, http.StatusNotFound, "rotation not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if !res.OK {
		// Gate failed: laggards still on old PSK. 409 Conflict surfaces
		// the laggards array so the UI can render "still pending: HB55,
		// HB99" before re-enabling the Retire button.
		writeJSON(w, http.StatusConflict, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- Stranded nodes (post-rotation recovery) ----

// handleFleetSecListStranded returns every node currently flagged
// stranded (failed to migrate during the most recent rotation that
// retired its PSK). Read-only; the dispatcher hook + recover_stranded
// job handler do the actual work.
func (s *Server) handleFleetSecListStranded(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	out, err := s.svc.FleetSec.ListStranded(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []fleetsec.NodeTrustRecord{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFleetSecRecoverStrandedNow forces an immediate
// recover_stranded job for the named node, bypassing the dispatcher
// hook's wait-for-inbound-traffic gate. The node must already have
// stranded_since + previous_psk_fp set (the operator can't recover a
// node we never tried to rotate). Returns the queued job id.
func (s *Server) handleFleetSecRecoverStrandedNow(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	nodeNum, err := strconv.ParseUint(chi.URLParam(r, "nodeNum"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "nodeNum must be a uint32")
		return
	}
	jobID, err := s.svc.FleetSec.ForceRecoverStranded(r.Context(), uint32(nodeNum))
	if err != nil {
		switch {
		case errors.Is(err, fleetsec.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// handleFleetSecCancelStranded clears the stranded markers + previous
// PSK pointer on a node, telling diginode-cc to stop trying to recover
// it. Used when the operator gives up + plans to USB-recover (or the
// node is permanently lost). Audit-logged.
func (s *Server) handleFleetSecCancelStranded(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	nodeNum, err := strconv.ParseUint(chi.URLParam(r, "nodeNum"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "nodeNum must be a uint32")
		return
	}
	if err := s.svc.FleetSec.CancelStranded(r.Context(), userIDFromCtx(r), uint32(nodeNum)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- Recovery (legacy CC-PRO recovery flow, distinct from stranded) ----

func (s *Server) handleFleetSecStartRecovery(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	var body struct {
		RescuePrivB64    string `json:"rescuePrivB64"`
		RescuePubB64     string `json:"rescuePubB64"`
		Ack              string `json:"ack"`
		NewPrimaryLabel  string `json:"newPrimaryLabel"`
		Notes            string `json:"notes"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.RescuePrivB64 == "" || body.RescuePubB64 == "" {
		writeError(w, http.StatusBadRequest, "rescuePrivB64 + rescuePubB64 required")
		return
	}
	priv, err := fleetsec.DecodePrivkeyB64(body.RescuePrivB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "rescue priv: "+err.Error())
		return
	}
	defer fleetsec.NewSecret(priv).Clear()
	pub, err := fleetsec.DecodePubkeyB64(body.RescuePubB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "rescue pub: "+err.Error())
		return
	}

	id, err := s.svc.FleetSec.StartRecovery(r.Context(), userIDFromCtx(r),
		priv, pub, fleetsec.StartRecoveryOpts{
			Ack:             body.Ack,
			NewPrimaryLabel: body.NewPrimaryLabel,
			Notes:           body.Notes,
		})
	if err != nil {
		switch {
		case errors.Is(err, fleetsec.ErrInvalidAck):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"recoveryId": id})
}

func (s *Server) handleFleetSecGetRecovery(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.svc.FleetSec.GetRecovery(r.Context(), id)
	if err != nil {
		if errors.Is(err, fleetsec.ErrNotFound) {
			writeError(w, http.StatusNotFound, "recovery not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
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
