package api

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
)

// handleFleetSecRevealChannelPSK returns the raw base64 PSK for the
// PRIMARY channel plus a meshtastic enrollment URL covering every
// non-DISABLED channel. Operator pastes the PSK into flash-script
// prompts, or scans the URL as a QR from the phone app.
//
// Gated by ADMIN role at the route layer (same gate as Rotate PSK).
// Refuses any channel index whose live Role is not PRIMARY so a misclick
// can't expose a SECONDARY-rotation slot during an in-flight migration.
//
// Source of truth is the local Heltec via AdminGetChannel -- the
// fleet_channels DB row carries only fingerprints, never raw PSK bytes.
// Implementation lives in its own file to keep the secret-bearing
// surface easy to grep / review.
func (s *Server) handleFleetSecRevealChannelPSK(w http.ResponseWriter, r *http.Request) {
	if s.svc.FleetSec == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet security service not configured")
		return
	}
	idx64, err := strconv.ParseInt(chi.URLParam(r, "idx"), 10, 32)
	if err != nil || idx64 < 0 {
		writeError(w, http.StatusBadRequest, "channel index must be a non-negative integer")
		return
	}
	idx := int32(idx64)

	ch, err := s.svc.FleetSec.ReadLocalChannel(r.Context(), idx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read channel from radio: "+err.Error())
		return
	}
	if ch.GetRole() != pb.Channel_PRIMARY {
		writeError(w, http.StatusForbidden, "channel is not primary")
		return
	}

	settings := ch.GetSettings()
	var psk []byte
	if settings != nil {
		psk = settings.GetPsk()
	}
	if len(psk) == 0 {
		writeError(w, http.StatusNotFound, "channel has no PSK material")
		return
	}

	channelURL, err := s.svc.FleetSec.BuildChannelSetURL(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "build enrollment URL: "+err.Error())
		return
	}

	s.svc.FleetSec.AuditReveal(r.Context(), userIDFromCtx(r), idx)

	writeJSON(w, http.StatusOK, map[string]any{
		"index":      idx,
		"name":       settings.GetName(),
		"pskB64":     base64.StdEncoding.EncodeToString(psk),
		"channelUrl": channelURL,
	})
}
