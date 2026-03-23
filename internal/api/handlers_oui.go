package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/inventory"
)

func (s *Server) handleOUIStats(w http.ResponseWriter, r *http.Request) {
	count := inventory.GetOUICount()
	source := "ieee-csv"
	if count == 0 {
		source = "none"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"totalEntries": count,
		"source":       source,
	})
}

func (s *Server) handleOUICache(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, inventory.GetOUIDB())
}

func (s *Server) handleOUIImport(w http.ResponseWriter, r *http.Request) {
	n, err := inventory.LoadOUIFromFile("data/oui.csv")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"entries": n,
	})
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
