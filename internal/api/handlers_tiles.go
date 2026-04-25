package api

import "net/http"

func (s *Server) handleTileRequest(w http.ResponseWriter, r *http.Request) {
	s.tileCache.HandleTileRequest(w, r)
}

func (s *Server) handleTilePreload(w http.ResponseWriter, r *http.Request) {
	s.tileCache.HandlePreloadStart(w, r)
}

func (s *Server) handleTilePreloadStatus(w http.ResponseWriter, r *http.Request) {
	s.tileCache.HandlePreloadStatus(w, r)
}

func (s *Server) handleTilePreloadCancel(w http.ResponseWriter, r *http.Request) {
	s.tileCache.HandlePreloadCancel(w, r)
}

func (s *Server) handleClearTileCache(w http.ResponseWriter, r *http.Request) {
	if err := s.tileCache.ClearCache(); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to clear tile cache")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "Map tile cache cleared"})
}

// handleTilesInfo reports environment-derived tile availability so the UI can
// decide whether to enable the JAWG ("matrix") option. The token itself never
// crosses the wire — the UI only sees the boolean.
func (s *Server) handleTilesInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"jawgAvailable": s.cfg.JawgAccessToken != "",
	})
}
