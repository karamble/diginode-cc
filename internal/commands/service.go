package commands

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// CommandStatus represents the lifecycle state of a command.
type CommandStatus string

const (
	StatusPending CommandStatus = "PENDING"
	StatusSent    CommandStatus = "SENT"
	StatusAcked   CommandStatus = "ACKED"
	StatusFailed  CommandStatus = "FAILED"
	StatusTimeout CommandStatus = "TIMEOUT"
)

var (
	ErrCommandNotFound = errors.New("command not found")
	ErrRateLimited     = errors.New("rate limited: too many commands to this node")
)

// Command represents a queued command to a mesh node.
type Command struct {
	ID          string                 `json:"id"`
	TargetNode  uint32                 `json:"targetNode"`
	CommandType string                 `json:"commandType"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	Status      CommandStatus          `json:"status"`
	SentAt      *time.Time             `json:"sentAt,omitempty"`
	AckedAt     *time.Time             `json:"ackedAt,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	RetryCount  int                    `json:"retryCount"`
	MaxRetries  int                    `json:"maxRetries"`
	CreatedAt   time.Time              `json:"createdAt"`
}

// Service manages the command queue with rate limiting and ACK tracking.
type Service struct {
	db       *database.DB
	hub      *ws.Hub
	pending  map[string]*Command
	nodeRate map[uint32]time.Time // last command time per node
	mu       sync.Mutex
	sendFn   func(nodeNum uint32, cmdType string, payload []byte) error
}

// NewService creates a new command queue service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:       db,
		hub:      hub,
		pending:  make(map[string]*Command),
		nodeRate: make(map[uint32]time.Time),
	}
}

// SetSendFunc sets the function used to actually transmit commands via serial.
func (s *Service) SetSendFunc(fn func(nodeNum uint32, cmdType string, payload []byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendFn = fn
}

// Enqueue adds a new command to the queue.
func (s *Service) Enqueue(cmd *Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rate limit: 1 command per node per 2 seconds
	if lastSent, ok := s.nodeRate[cmd.TargetNode]; ok {
		if time.Since(lastSent) < 2*time.Second {
			return ErrRateLimited
		}
	}

	cmd.Status = StatusPending
	cmd.CreatedAt = time.Now()
	if cmd.MaxRetries == 0 {
		cmd.MaxRetries = 3
	}

	s.pending[cmd.ID] = cmd

	go s.send(cmd)
	return nil
}

// HandleACK processes an acknowledgment for a pending command.
func (s *Service) HandleACK(cmdID string, result map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd, exists := s.pending[cmdID]
	if !exists {
		return
	}

	now := time.Now()
	cmd.Status = StatusAcked
	cmd.AckedAt = &now
	cmd.Result = result
	delete(s.pending, cmdID)

	slog.Info("command acknowledged", "id", cmdID, "targetNode", cmd.TargetNode)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventCommand,
		Payload: cmd,
	})

	go s.persistCommand(cmd)
}

func (s *Service) send(cmd *Command) {
	s.mu.Lock()
	sendFn := s.sendFn
	s.mu.Unlock()

	if sendFn == nil {
		slog.Warn("no send function configured, command queued but not sent", "id", cmd.ID)
		return
	}

	payload, _ := json.Marshal(cmd.Payload)

	err := sendFn(cmd.TargetNode, cmd.CommandType, payload)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		slog.Error("failed to send command", "id", cmd.ID, "error", err)
		cmd.RetryCount++
		if cmd.RetryCount >= cmd.MaxRetries {
			cmd.Status = StatusFailed
			delete(s.pending, cmd.ID)
		}
	} else {
		now := time.Now()
		cmd.Status = StatusSent
		cmd.SentAt = &now
		s.nodeRate[cmd.TargetNode] = now
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventCommand,
		Payload: cmd,
	})

	go s.persistCommand(cmd)
}

func (s *Service) persistCommand(cmd *Command) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payloadJSON, _ := json.Marshal(cmd.Payload)
	resultJSON, _ := json.Marshal(cmd.Result)

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO commands (id, target_node, command_type, payload, status,
			sent_at, acked_at, result, retry_count, max_retries, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			sent_at = EXCLUDED.sent_at,
			acked_at = EXCLUDED.acked_at,
			result = EXCLUDED.result,
			retry_count = EXCLUDED.retry_count,
			updated_at = NOW()`,
		cmd.ID, cmd.TargetNode, cmd.CommandType, payloadJSON,
		string(cmd.Status), cmd.SentAt, cmd.AckedAt, resultJSON,
		cmd.RetryCount, cmd.MaxRetries, cmd.CreatedAt,
	)
	if err != nil {
		slog.Error("failed to persist command", "id", cmd.ID, "error", err)
	}
}
