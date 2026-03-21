package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/users"
)

func (s *Server) handleListFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, permissions.AllFeatures)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.Users.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	if list == nil {
		list = []*users.User{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string     `json:"email"`
		Password string     `json:"password"`
		Name     string     `json:"name"`
		Role     users.Role `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if body.Role == "" {
		body.Role = users.RoleViewer
	}

	user, err := s.svc.Users.Create(r.Context(), body.Email, body.Password, body.Name, body.Role)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	user, err := s.svc.Users.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Name string     `json:"name"`
		Role users.Role `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Users.Update(r.Context(), id, body.Name, body.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Users.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInviteUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string     `json:"email"`
		Role  users.Role `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if body.Role == "" {
		body.Role = users.RoleViewer
	}

	claims := auth.GetClaims(r.Context())
	invitedBy := ""
	if claims != nil {
		invitedBy = claims.UserID
	}

	inv, err := s.svc.Users.CreateInvitation(r.Context(), body.Email, body.Role, invitedBy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invitation")
		return
	}

	// Send invitation email if mail is configured
	if s.svc.Mail != nil && s.svc.Mail.IsConfigured() {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		inviteURL := fmt.Sprintf("%s://%s/accept-invite?token=%s", scheme, host, inv.Token)

		// Resolve inviter name for email
		inviterName := "An administrator"
		if invitedBy != "" {
			if u, err := s.svc.Users.GetByID(r.Context(), invitedBy); err == nil && u.Name != "" {
				inviterName = u.Name
			}
		}

		go func() {
			if err := s.svc.Mail.SendInvitation(body.Email, inviteURL, inviterName); err != nil {
				slog.Error("failed to send invitation email", "email", body.Email, "error", err)
			}
		}()
	}

	writeJSON(w, http.StatusCreated, inv)
}
