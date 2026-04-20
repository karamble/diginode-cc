package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/karamble/diginode-cc/internal/chat"
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/serial"
)

func (s *Server) handleGetChatMessages(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 {
			limit = v
		}
	}

	mode := r.URL.Query().Get("mode")
	peerStr := r.URL.Query().Get("peer")

	var messages []*chat.Message
	var err error

	switch mode {
	case "broadcast":
		messages, err = s.svc.Chat.GetBroadcastMessages(limit)
	case "dm":
		peer, parseErr := strconv.ParseUint(peerStr, 10, 32)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid peer node number")
			return
		}
		localNum := s.svc.Nodes.GetLocalNodeNum()
		messages, err = s.svc.Chat.GetDMMessages(limit, uint32(peer), localNum)
	default:
		messages, err = s.svc.Chat.GetMessages(limit)
	}

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

	// Gatesensor (and any sensor whose Heltec runs Meshtastic SerialModule in
	// TEXTMSG mode) cannot receive DMs: since fw 2.5 the SerialModule never
	// emits PKI-encrypted DM payloads to UART, so the bridged Arduino sees
	// nothing. Reroute as a broadcast addressed by the gatesensor's @<name>
	// prefix — same pattern AntiHunter uses for @AH<id>.
	if toAddr != serial.BroadcastAddr {
		if n := s.svc.Nodes.GetByNodeNum(toAddr); n != nil && n.NodeType == nodes.NodeTypeGatesensor {
			prefix := "@" + n.ShortName + " "
			if !strings.HasPrefix(body.Message, "@") {
				body.Message = prefix + body.Message
			}
			toAddr = serial.BroadcastAddr
		}
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
