package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/karamble/diginode-cc/internal/chat"
	"github.com/karamble/diginode-cc/internal/serial"
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

	// Build and transmit the message via serial port to the Heltec radio
	var toAddr uint32 = serial.BroadcastAddr
	if body.To != "" {
		toAddr = serial.ParseNodeNum(body.To)
	}
	data := serial.BuildTextMessage(toAddr, body.Message)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		slog.Warn("failed to send chat message via serial", "error", err)
		// Still store locally even if serial send fails
	}

	// Store in ring buffer (gotailme polls this) with "local" nodeId
	s.serialMgr.AddTextMessage("local", body.Message, "")

	// Persist to DB and broadcast via WebSocket (bypassing the ring buffer callback
	// since we already added to the buffer above)
	s.svc.Chat.PersistAndBroadcast(0, toAddr, 0, body.Message)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
