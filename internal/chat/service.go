package chat

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Message represents a mesh chat message.
type Message struct {
	ID        string    `json:"id,omitempty"`
	FromNode  uint32    `json:"fromNode"`
	ToNode    uint32    `json:"toNode"` // 0 = broadcast
	Channel   uint32    `json:"channel"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Service manages mesh chat messages.
type Service struct {
	db          *database.DB
	hub         *ws.Hub
	addToBuffer func(nodeID, message, siteID string) // serial ring buffer callback
}

// NewService creates a new chat service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{db: db, hub: hub}
}

// SetBufferCallback sets the callback to add messages to the serial ring buffer.
func (s *Service) SetBufferCallback(fn func(nodeID, message, siteID string)) {
	s.addToBuffer = fn
}

// HandleTextMessage processes an incoming text message from the mesh.
func (s *Service) HandleTextMessage(from, to uint32, channel uint32, text string) {
	msg := &Message{
		FromNode:  from,
		ToNode:    to,
		Channel:   channel,
		Text:      text,
		Timestamp: time.Now(),
	}

	slog.Info("chat message",
		"from", from,
		"to", to,
		"channel", channel,
		"text", text)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventChat,
		Payload: msg,
	})

	// Add to serial ring buffer (gotailme polls this)
	if s.addToBuffer != nil {
		nodeID := fmt.Sprintf("!%08x", from)
		s.addToBuffer(nodeID, text, "")
	}

	go s.persistMessage(msg)
}

// GetMessages retrieves recent chat messages.
func (s *Service) GetMessages(limit int) ([]*Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, from_node, to_node, channel, message, timestamp
		FROM chat_messages
		ORDER BY timestamp DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.FromNode, &m.ToNode, &m.Channel, &m.Text, &m.Timestamp); err != nil {
			continue
		}
		messages = append(messages, &m)
	}
	return messages, nil
}

func (s *Service) persistMessage(msg *Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO chat_messages (from_node, to_node, channel, message, timestamp)
		VALUES ($1, $2, $3, $4, $5)`,
		msg.FromNode, msg.ToNode, msg.Channel, msg.Text, msg.Timestamp,
	)
	if err != nil {
		slog.Error("failed to persist chat message", "error", err)
	}
}
