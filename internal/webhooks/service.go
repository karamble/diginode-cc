package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

var ErrWebhookNotFound = errors.New("webhook not found")

// Webhook defines an HTTP callback configuration.
type Webhook struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Method        string            `json:"method"` // POST, PUT, PATCH
	Headers       map[string]string `json:"headers,omitempty"`
	Secret        string            `json:"secret,omitempty"` // HMAC signing
	Events        []string          `json:"events"`
	Enabled       bool              `json:"enabled"`
	LastTriggered *time.Time        `json:"lastTriggered,omitempty"`
	LastStatus    int               `json:"lastStatus,omitempty"`
}

// Service manages webhooks and dispatches events.
type Service struct {
	db       *database.DB
	webhooks map[string]*Webhook
	mu       sync.RWMutex
	client   *http.Client
}

// NewService creates a new webhook service.
func NewService(db *database.DB) *Service {
	return &Service{
		db:       db,
		webhooks: make(map[string]*Webhook),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Load loads all webhooks from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, url, method, headers, secret, events, enabled
		FROM webhooks WHERE enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var w Webhook
		var headersJSON []byte
		if err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.Method,
			&headersJSON, &w.Secret, &w.Events, &w.Enabled); err != nil {
			continue
		}
		json.Unmarshal(headersJSON, &w.Headers)
		s.webhooks[w.ID] = &w
	}

	slog.Info("loaded webhooks", "count", len(s.webhooks))
	return nil
}

// Dispatch sends an event to all matching webhooks.
func (s *Service) Dispatch(eventType string, payload interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, w := range s.webhooks {
		if !w.Enabled || !matchesEvent(w.Events, eventType) {
			continue
		}
		go s.send(w, eventType, payload)
	}
}

func (s *Service) send(w *Webhook, eventType string, payload interface{}) {
	body := map[string]interface{}{
		"event":     eventType,
		"payload":   payload,
		"timestamp": time.Now().UTC(),
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		slog.Error("webhook marshal error", "id", w.ID, "error", err)
		return
	}

	req, err := http.NewRequest(w.Method, w.URL, bytes.NewReader(jsonData))
	if err != nil {
		slog.Error("webhook request error", "id", w.ID, "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DigiNode-CC/1.0")

	// Custom headers
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}

	// HMAC signature
	if w.Secret != "" {
		sig := computeHMAC(jsonData, []byte(w.Secret))
		req.Header.Set("X-Signature-256", "sha256="+sig)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		slog.Error("webhook delivery failed", "id", w.ID, "url", w.URL, "error", err)
		s.updateStatus(w.ID, 0)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	slog.Debug("webhook delivered", "id", w.ID, "status", resp.StatusCode)
	s.updateStatus(w.ID, resp.StatusCode)
}

func (s *Service) updateStatus(id string, status int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.db.Pool.Exec(ctx, `
		UPDATE webhooks SET last_triggered = NOW(), last_status = $2
		WHERE id = $1`, id, status)
}

func computeHMAC(data, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func matchesEvent(events []string, eventType string) bool {
	for _, e := range events {
		if e == eventType || e == "*" {
			return true
		}
	}
	return false
}

// GetAll returns all webhooks.
func (s *Service) GetAll() []*Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Webhook, 0, len(s.webhooks))
	for _, w := range s.webhooks {
		result = append(result, w)
	}
	return result
}
