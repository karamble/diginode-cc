package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"

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

// sendCommandRequest is the CC PRO-compatible structured command input.
type sendCommandRequest struct {
	Target  string   `json:"target"`           // @ALL, @NODE_22, etc.
	Name    string   `json:"name"`             // STATUS, SCAN_START, etc.
	Params  []string `json:"params,omitempty"` // command parameters
	Forever bool     `json:"forever,omitempty"`

	// Legacy fields (backward compat)
	TargetNode  uint32                 `json:"targetNode,omitempty"`
	CommandType string                 `json:"commandType,omitempty"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
}

func (s *Server) handleCreateCommand(w http.ResponseWriter, r *http.Request) {
	var req sendCommandRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Generate UUID
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate command ID")
		return
	}
	id := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])

	var cmd commands.Command

	if req.Name != "" {
		// Structured input (CC PRO parity)
		output, err := commands.Build(req.Target, req.Name, req.Params, req.Forever)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		cmd = commands.Command{
			ID:          id,
			Target:      output.Target,
			Name:        output.Name,
			Params:      output.Params,
			Line:        output.Line,
			CommandType: output.Name,
			TargetNode:  req.TargetNode,
		}
	} else {
		// Legacy input (raw commandType + payload)
		cmd = commands.Command{
			ID:          id,
			TargetNode:  req.TargetNode,
			CommandType: req.CommandType,
			Payload:     req.Payload,
		}
	}

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

// previewCommandRequest mirrors sendCommandRequest but skips persistence.
type previewCommandRequest struct {
	Target  string   `json:"target"`
	Name    string   `json:"name"`
	Params  []string `json:"params,omitempty"`
	Forever bool     `json:"forever,omitempty"`
}

// handleCommandPreview validates a structured command and returns its on-wire
// line without persisting or transmitting anything. The Command Console uses
// this to render a live preview as the operator edits the form, keeping the
// Go builder as the single source of truth for wire formatting.
func (s *Server) handleCommandPreview(w http.ResponseWriter, r *http.Request) {
	var req previewCommandRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	output, err := commands.Build(req.Target, req.Name, req.Params, req.Forever)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, output)
}

// sendRawCommandRequest carries a pre-formatted "@TARGET COMMAND:..." line.
type sendRawCommandRequest struct {
	Line string `json:"line"`
}

// handleSendRawCommand enqueues a user-typed raw line verbatim, skipping the
// Build() validator. This is the power-user escape hatch on the Command
// Console for sending commands that the structured form can't express, or
// for reproducing a line copied from a log. Target is extracted from the
// leading "@..." token so the history row stays useful.
func (s *Server) handleSendRawCommand(w http.ResponseWriter, r *http.Request) {
	var req sendRawCommandRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	line := strings.TrimSpace(req.Line)
	if line == "" {
		writeError(w, http.StatusBadRequest, "line is empty")
		return
	}

	// Derive target + name for display. The firmware filters by the @TARGET
	// prefix itself; we send as broadcast (TargetNode=0) regardless.
	target, name := "@ALL", "RAW"
	if strings.HasPrefix(line, "@") {
		if sp := strings.IndexByte(line, ' '); sp > 0 {
			target = line[:sp]
			rest := strings.TrimSpace(line[sp+1:])
			if colon := strings.IndexByte(rest, ':'); colon > 0 {
				name = rest[:colon]
			} else if rest != "" {
				name = rest
			}
		} else {
			target = line
		}
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate command ID")
		return
	}
	id := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])

	cmd := commands.Command{
		ID:          id,
		Target:      target,
		Name:        name,
		CommandType: name,
		Line:        line,
	}
	if err := s.svc.Commands.Enqueue(&cmd); err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cmd)
}

// handleListCommandTypes returns the command registry for the frontend to build forms.
func (s *Server) handleListCommandTypes(w http.ResponseWriter, r *http.Request) {
	type paramOut struct {
		Key         string   `json:"key"`
		Label       string   `json:"label"`
		Type        string   `json:"type"`
		Required    bool     `json:"required,omitempty"`
		Min         float64  `json:"min,omitempty"`
		Max         float64  `json:"max,omitempty"`
		Options     []string `json:"options,omitempty"`
		Placeholder string   `json:"placeholder,omitempty"`
	}
	type cmdOut struct {
		Name           string     `json:"name"`
		Group          string     `json:"group"`
		Description    string     `json:"description"`
		Params         []paramOut `json:"params"`
		AllowForever   bool       `json:"allowForever,omitempty"`
		SingleNode     bool       `json:"singleNode,omitempty"`
		SupportedTypes []string   `json:"supportedTypes,omitempty"`
	}

	var result []cmdOut
	for _, group := range commands.GroupOrder {
		for _, def := range commands.Registry {
			if def.Group != group {
				continue
			}
			c := cmdOut{
				Name:           def.Name,
				Group:          def.Group,
				Description:    def.Description,
				AllowForever:   def.AllowForever,
				SingleNode:     def.SingleNode,
				SupportedTypes: def.SupportedTypes,
			}
			for _, p := range def.Params {
				c.Params = append(c.Params, paramOut{
					Key:         p.Key,
					Label:       p.Label,
					Type:        p.Type,
					Required:    p.Required,
					Min:         p.Min,
					Max:         p.Max,
					Options:     p.Options,
					Placeholder: p.Placeholder,
				})
			}
			if c.Params == nil {
				c.Params = []paramOut{}
			}
			result = append(result, c)
		}
	}

	writeJSON(w, http.StatusOK, result)
}
