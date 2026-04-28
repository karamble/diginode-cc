package api

import (
	"net/http"
	"strconv"

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
