package acars

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/karamble/diginode-cc/internal/ws"
)

// Message represents a decoded ACARS message.
type Message struct {
	Timestamp   float64 `json:"timestamp"`
	Channel     int     `json:"channel"`
	Frequency   float64 `json:"freq"`
	Level       float64 `json:"level"`
	Error       int     `json:"error"`
	Mode        string  `json:"mode"`
	Label       string  `json:"label"`
	BlockID     string  `json:"block_id"`
	Ack         string  `json:"ack"`
	Tail        string  `json:"tail"`
	Flight      string  `json:"flight"`
	MessageNum  string  `json:"msgno"`
	Text        string  `json:"text"`
	IsOnGround  bool    `json:"is_onground"`
	IsResponse  bool    `json:"is_response"`
	Station     string  `json:"station_id"`
}

// Service listens for ACARS messages on a UDP port.
type Service struct {
	hub      *ws.Hub
	port     int
	messages []*Message
	mu       sync.RWMutex
	stopCh   chan struct{}
}

// NewService creates a new ACARS listener.
func NewService(hub *ws.Hub, port int) *Service {
	return &Service{
		hub:    hub,
		port:   port,
		stopCh: make(chan struct{}),
	}
}

// Start begins listening for ACARS UDP messages.
func (s *Service) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	slog.Info("ACARS listener started", "port", s.port)

	buf := make([]byte, 4096)
	for {
		select {
		case <-s.stopCh:
			return nil
		default:
		}

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		var msg Message
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		s.mu.Lock()
		s.messages = append(s.messages, &msg)
		// Keep last 1000 messages
		if len(s.messages) > 1000 {
			s.messages = s.messages[len(s.messages)-1000:]
		}
		s.mu.Unlock()

		s.hub.Broadcast(ws.Event{
			Type:    ws.EventACARS,
			Payload: &msg,
		})
	}
}

// Stop halts the ACARS listener.
func (s *Service) Stop() {
	close(s.stopCh)
}

// GetMessages returns recent ACARS messages.
func (s *Service) GetMessages(limit int) []*Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit > len(s.messages) {
		limit = len(s.messages)
	}
	return s.messages[len(s.messages)-limit:]
}

// ClearMessages removes all stored messages.
func (s *Service) ClearMessages() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
}
