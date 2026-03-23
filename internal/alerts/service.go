package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Severity levels for alerts.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityNotice   Severity = "NOTICE"
	SeverityAlert    Severity = "ALERT"
	SeverityCritical Severity = "CRITICAL"
)

// Rule defines an alert rule with conditions.
type Rule struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description,omitempty"`
	Condition       map[string]interface{} `json:"condition"`
	Severity        Severity               `json:"severity"`
	Enabled         bool                   `json:"enabled"`
	CooldownSeconds int                    `json:"cooldownSeconds"`
	LastTriggered   *time.Time             `json:"lastTriggered,omitempty"`
}

// Event represents a triggered alert.
type Event struct {
	ID             string                 `json:"id"`
	RuleID         string                 `json:"ruleId,omitempty"`
	Severity       Severity               `json:"severity"`
	Title          string                 `json:"title"`
	Message        string                 `json:"message,omitempty"`
	Data           map[string]interface{} `json:"data,omitempty"`
	Acknowledged   bool                   `json:"acknowledged"`
	AcknowledgedBy string                 `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt *time.Time             `json:"acknowledgedAt,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
}

// Service manages alert rules and events.
type Service struct {
	db    *database.DB
	hub   *ws.Hub
	rules map[string]*Rule
	mu    sync.RWMutex
}

// NewService creates a new alert service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:    db,
		hub:   hub,
		rules: make(map[string]*Rule),
	}
}

// Load loads all alert rules from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, description, condition, severity, enabled,
			cooldown_seconds, last_triggered
		FROM alert_rules`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var r Rule
		var condJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &condJSON,
			&r.Severity, &r.Enabled, &r.CooldownSeconds, &r.LastTriggered); err != nil {
			continue
		}
		json.Unmarshal(condJSON, &r.Condition)
		s.rules[r.ID] = &r
	}

	slog.Info("loaded alert rules", "count", len(s.rules))
	return nil
}

// Trigger fires an alert event, respecting cooldown.
func (s *Service) Trigger(ctx context.Context, ruleID string, title, message string, data map[string]interface{}) error {
	s.mu.Lock()
	rule, exists := s.rules[ruleID]
	if exists && rule.LastTriggered != nil {
		if time.Since(*rule.LastTriggered) < time.Duration(rule.CooldownSeconds)*time.Second {
			s.mu.Unlock()
			return nil // In cooldown
		}
	}

	severity := SeverityNotice
	if exists {
		severity = rule.Severity
		now := time.Now()
		rule.LastTriggered = &now
	}
	s.mu.Unlock()

	evt := &Event{
		RuleID:    ruleID,
		Severity:  severity,
		Title:     title,
		Message:   message,
		Data:      data,
		CreatedAt: time.Now(),
	}

	// Persist
	dataJSON, _ := json.Marshal(data)
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO alert_events (rule_id, severity, title, message, data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		ruleID, string(severity), title, message, dataJSON,
	).Scan(&evt.ID)
	if err != nil {
		slog.Error("failed to persist alert event", "error", err)
		return err
	}

	// Broadcast
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventAlert,
		Payload: evt,
	})

	slog.Info("alert triggered", "rule", ruleID, "title", title, "severity", severity)
	return nil
}

// TriggerDirect fires an alert without a rule.
func (s *Service) TriggerDirect(ctx context.Context, severity Severity, title, message string, data map[string]interface{}) {
	evt := &Event{
		Severity:  severity,
		Title:     title,
		Message:   message,
		Data:      data,
		CreatedAt: time.Now(),
	}

	dataJSON, _ := json.Marshal(data)
	if err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO alert_events (severity, title, message, data)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		string(severity), title, message, dataJSON,
	).Scan(&evt.ID); err != nil {
		slog.Error("failed to persist alert event", "title", title, "error", err)
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventAlert,
		Payload: evt,
	})
}

// Acknowledge marks an alert event as acknowledged.
func (s *Service) Acknowledge(ctx context.Context, eventID, userID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE alert_events SET acknowledged = true,
			acknowledged_by = $2, acknowledged_at = NOW()
		WHERE id = $1`, eventID, userID)
	return err
}

// CreateRule adds a new alert rule.
func (s *Service) CreateRule(ctx context.Context, r *Rule) error {
	condJSON, _ := json.Marshal(r.Condition)
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO alert_rules (name, description, condition, severity, enabled, cooldown_seconds)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		r.Name, r.Description, condJSON, string(r.Severity), r.Enabled, r.CooldownSeconds,
	).Scan(&r.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rules[r.ID] = r
	s.mu.Unlock()
	return nil
}

// UpdateRule updates an existing alert rule.
func (s *Service) UpdateRule(ctx context.Context, id string, r *Rule) error {
	condJSON, _ := json.Marshal(r.Condition)
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE alert_rules SET name = $2, description = $3, condition = $4,
			severity = $5, enabled = $6, cooldown_seconds = $7
		WHERE id = $1`,
		id, r.Name, r.Description, condJSON, string(r.Severity), r.Enabled, r.CooldownSeconds)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r.ID = id
	s.rules[id] = r
	s.mu.Unlock()
	return nil
}

// DeleteRule removes an alert rule.
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.rules, id)
	s.mu.Unlock()
	return nil
}

// GetEvents returns recent alert events.
func (s *Service) GetEvents(ctx context.Context, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, rule_id, severity, title, message, acknowledged,
			acknowledged_by, acknowledged_at, created_at
		FROM alert_events ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*Event
	for rows.Next() {
		var e Event
		var ruleID, ackBy sql.NullString
		var ackAt sql.NullTime
		if err := rows.Scan(&e.ID, &ruleID, &e.Severity, &e.Title, &e.Message,
			&e.Acknowledged, &ackBy, &ackAt, &e.CreatedAt); err != nil {
			slog.Warn("failed to scan alert event", "error", err)
			continue
		}
		e.RuleID = ruleID.String
		e.AcknowledgedBy = ackBy.String
		if ackAt.Valid {
			e.AcknowledgedAt = &ackAt.Time
		}
		events = append(events, &e)
	}
	return events, nil
}

// GetRules returns all alert rules.
func (s *Service) GetRules() []*Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Rule, 0, len(s.rules))
	for _, r := range s.rules {
		result = append(result, r)
	}
	return result
}
