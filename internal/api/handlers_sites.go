package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/sites"
)

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.Sites.GetAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sites")
		return
	}
	if list == nil {
		list = []*sites.Site{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var site sites.Site
	if err := readJSON(r, &site); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if site.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := s.svc.Sites.Create(r.Context(), &site); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create site")
		return
	}

	writeJSON(w, http.StatusCreated, site)
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	site, err := s.svc.Sites.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sites.ErrSiteNotFound) {
			writeError(w, http.StatusNotFound, "site not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get site")
		return
	}

	writeJSON(w, http.StatusOK, site)
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var site sites.Site
	if err := readJSON(r, &site); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Sites.Update(r.Context(), id, &site); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update site")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Sites.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete site")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
