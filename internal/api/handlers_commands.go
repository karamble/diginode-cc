package api

import (
	"crypto/rand"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/commands"
)

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	cmds, err := s.svc.Commands.List(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list commands")
		return
	}
	if cmds == nil {
		cmds = []*commands.Command{}
	}
	writeJSON(w, http.StatusOK, cmds)
}

func (s *Server) handleCreateCommand(w http.ResponseWriter, r *http.Request) {
	var cmd commands.Command
	if err := readJSON(r, &cmd); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Generate a UUID for the command.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate command ID")
		return
	}
	cmd.ID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])

	if err := s.svc.Commands.Enqueue(&cmd); err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, cmd)
}

func (s *Server) handleGetCommand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cmd, err := s.svc.Commands.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "command not found")
		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

func (s *Server) handleDeleteCommand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Commands.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete command")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
