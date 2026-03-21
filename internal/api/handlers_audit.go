package api

import (
	"net/http"
	"strconv"

	"github.com/karamble/diginode-cc/internal/audit"
)

func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entries, err := s.svc.Audit.GetRecent(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get audit logs")
		return
	}
	if entries == nil {
		entries = []*audit.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
