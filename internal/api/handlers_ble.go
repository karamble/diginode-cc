package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/bleclassify"
)

// handleListBLEDetections answers GET /api/ble/detections with the most
// recent classified BLE advertisements. Filters: ?mac=, ?node_id=, ?type=,
// ?since= (RFC3339), ?limit= (default 100, max 1000). Newest first.
func (s *Server) handleListBLEDetections(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := bleclassify.ListFilter{
		MAC:           q.Get("mac"),
		NodeID:        q.Get("node_id"),
		DetectionType: q.Get("type"),
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339: "+err.Error())
			return
		}
		filter.Since = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		filter.Limit = n
	}

	out, err := s.svc.BLEClassify.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list ble detections: "+err.Error())
		return
	}
	if out == nil {
		out = []*bleclassify.Detection{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetBLEDetectionsByMAC answers GET /api/ble/detections/{mac}.
// Convenience wrapper around handleListBLEDetections with the MAC filter
// fixed and a higher default limit (per-MAC history is the use case).
func (s *Server) handleGetBLEDetectionsByMAC(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	if mac == "" {
		writeError(w, http.StatusBadRequest, "mac is required")
		return
	}

	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}

	out, err := s.svc.BLEClassify.List(r.Context(), bleclassify.ListFilter{
		MAC:   mac,
		Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list ble detections: "+err.Error())
		return
	}
	if out == nil {
		out = []*bleclassify.Detection{}
	}
	writeJSON(w, http.StatusOK, out)
}
