package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/inventory"
)

func (s *Server) handleOUIStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"totalEntries": len(inventory.GetOUIDB()),
		"source":       "embedded",
	})
}

func (s *Server) handleOUICache(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, inventory.GetOUIDB())
}

func (s *Server) handleOUIImport(w http.ResponseWriter, r *http.Request) {
	// Accept CSV/TXT file with OUI mappings
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "OUI import not yet supported"})
}

func (s *Server) handleOUIExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=oui.json")
	writeJSON(w, http.StatusOK, inventory.GetOUIDB())
}

func (s *Server) handleOUIResolve(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	vendor := inventory.LookupOUI(mac)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"mac":    mac,
		"vendor": vendor,
	})
}
