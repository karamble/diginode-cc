package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/karamble/diginode-cc/internal/probes"
)

// handleListProbeSSIDs returns the probe_ssids table ordered by last_seen DESC.
// Optional ?limit=N caps results (default 500). Each row is enriched with
// distinct_macs_24h via a correlated subquery.
func (s *Server) handleListProbeSSIDs(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10000 {
			limit = n
		}
	}
	rows, err := s.svc.Probes.ListAll(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list probe ssids: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ensureProbeRows(rows))
}

// handleProbeSSIDsByName returns all rows matching ?ssid=<name> across all
// nodes — answers "which sensors have seen this network probed for?".
func (s *Server) handleProbeSSIDsByName(w http.ResponseWriter, r *http.Request) {
	ssid := r.URL.Query().Get("ssid")
	if ssid == "" {
		writeError(w, http.StatusBadRequest, "ssid query parameter is required")
		return
	}
	rows, err := s.svc.Probes.GetForSSID(r.Context(), ssid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query probe ssids: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ensureProbeRows(rows))
}

// handleProbeSSIDsForCommand returns probe_ssids rows last_seen within an
// optional time window, optionally filtered to a single sensor. Used by the
// command-details modal to show the SSIDs captured during a specific
// PROBE_START scan window. Query params: nodeId, seenAfter (RFC3339Nano),
// seenBefore (RFC3339Nano). Any can be omitted.
func (s *Server) handleProbeSSIDsForCommand(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nodeID := q.Get("nodeId")
	var after, before time.Time
	if v := q.Get("seenAfter"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "seenAfter must be RFC3339: "+err.Error())
			return
		}
		after = t
	}
	if v := q.Get("seenBefore"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "seenBefore must be RFC3339: "+err.Error())
			return
		}
		before = t
	}
	rows, err := s.svc.Probes.GetForCommandWindow(r.Context(), nodeID, after, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query probe ssids: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ensureProbeRows(rows))
}

// handleProbeSSIDsByNode returns all rows for ?nodeId=<id> — answers
// "what SSIDs has this sensor seen probed for?".
func (s *Server) handleProbeSSIDsByNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId query parameter is required")
		return
	}
	rows, err := s.svc.Probes.GetForNode(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query probe ssids: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ensureProbeRows(rows))
}

// ensureProbeRows replaces a nil slice with [] so JSON consumers always get
// an array, never null. Mirrors handleListInventory's behavior.
func ensureProbeRows(rows []probes.SSIDStat) []probes.SSIDStat {
	if rows == nil {
		return []probes.SSIDStat{}
	}
	return rows
}
