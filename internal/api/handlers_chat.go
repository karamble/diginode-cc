package api

import (
	"net/http"
	"strconv"

	"github.com/karamble/diginode-cc/internal/chat"
)

func (s *Server) handleGetChatMessages(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 {
			limit = v
		}
	}

	messages, err := s.svc.Chat.GetMessages(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get chat messages")
		return
	}
	if messages == nil {
		messages = []*chat.Message{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// handleClearChatMessages deletes all chat messages from the DB and serial ring buffer.
func (s *Server) handleClearChatMessages(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Chat.ClearAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear chat messages: "+err.Error())
		return
	}

	// Also clear the serial ring buffer
	s.serialMgr.ClearTextMessages()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSendChatMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
		To      string `json:"to,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Queue the message for serial transmission via the ring buffer.
	s.serialMgr.AddTextMessage("", body.Message, "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
