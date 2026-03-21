package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/geofences"
)

func (s *Server) handleListGeofences(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Geofences.GetAll()
	if list == nil {
		list = []*geofences.Geofence{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateGeofence(w http.ResponseWriter, r *http.Request) {
	var g geofences.Geofence
	if err := readJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if g.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if len(g.Polygon) < 3 {
		writeError(w, http.StatusBadRequest, "polygon must have at least 3 points")
		return
	}

	if err := s.svc.Geofences.Create(r.Context(), &g); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create geofence")
		return
	}

	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGeofence(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var g geofences.Geofence
	if err := readJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Geofences.Update(r.Context(), id, &g); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update geofence")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteGeofence(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Geofences.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete geofence")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
