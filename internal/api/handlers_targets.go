package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/targets"
)

func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Targets.GetAll()
	if list == nil {
		list = []*targets.Target{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	var t targets.Target
	if err := readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if t.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := s.svc.Targets.Create(r.Context(), &t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create target")
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var t targets.Target
	if err := readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Targets.Update(r.Context(), id, &t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update target")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleClearTargets removes all targets from memory and the database.
func (s *Server) handleClearTargets(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Targets.ClearAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear targets: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleResolveTarget marks a target as resolved/closed.
func (s *Server) handleResolveTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Targets.Resolve(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Targets.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete target")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetTargetPositions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []struct{}{})
}
