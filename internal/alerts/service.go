package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
	NotifyWebhook   bool                   `json:"notifyWebhook"`
	NotifyEmail     bool                   `json:"notifyEmail"`
	EmailRecipients string                 `json:"emailRecipients,omitempty"`
	NotifyVisual    bool                   `json:"notifyVisual"`
	NotifyAudible   bool                   `json:"notifyAudible"`
	LastTriggered   *time.Time             `json:"lastTriggered,omitempty"`
}

// EmailSender is a callback for sending alert emails.
type EmailSender func(to, subject, body string) error

// Event represents a triggered alert.
type Event struct {
	ID             string                 `json:"id"`
	RuleID         string                 `json:"ruleId,omitempty"`
	Severity       Severity               `json:"severity"`
	Title          string                 `json:"title"`
	Message        string                 `json:"message,omitempty"`
	Data           map[string]interface{} `json:"data,omitempty"`
	NotifyVisual   bool                   `json:"notifyVisual"`
	NotifyAudible  bool                   `json:"notifyAudible"`
	Acknowledged   bool                   `json:"acknowledged"`
	AcknowledgedBy string                 `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt *time.Time             `json:"acknowledgedAt,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
}

// Service manages alert rules and events.
type Service struct {
	db          *database.DB
	hub         *ws.Hub
	rules       map[string]*Rule
	mu          sync.RWMutex
	emailSender EmailSender
}

// NewService creates a new alert service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:    db,
		hub:   hub,
		rules: make(map[string]*Rule),
	}
}

// SetEmailSender sets the callback used to send alert notification emails.
func (s *Service) SetEmailSender(fn EmailSender) {
	s.emailSender = fn
}

// Load loads all alert rules from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, description, condition, severity, enabled,
			cooldown_seconds, COALESCE(notify_webhook, false),
			COALESCE(notify_email, false), COALESCE(email_recipients, ''),
			COALESCE(notify_visual, true), COALESCE(notify_audible, true),
			last_triggered
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
			&r.Severity, &r.Enabled, &r.CooldownSeconds,
			&r.NotifyWebhook, &r.NotifyEmail, &r.EmailRecipients,
			&r.NotifyVisual, &r.NotifyAudible,
			&r.LastTriggered); err != nil {
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

	notifyVisual := true
	notifyAudible := true
	if exists {
		notifyVisual = rule.NotifyVisual
		notifyAudible = rule.NotifyAudible
	}

	evt := &Event{
		RuleID:        ruleID,
		Severity:      severity,
		Title:         title,
		Message:       message,
		Data:          data,
		NotifyVisual:  notifyVisual,
		NotifyAudible: notifyAudible,
		CreatedAt:     time.Now(),
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

	// Broadcast via WebSocket
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventAlert,
		Payload: evt,
	})

	// Send email notification if configured
	if exists && rule.NotifyEmail && rule.EmailRecipients != "" && s.emailSender != nil {
		go s.sendAlertEmail(rule, evt)
	}

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

// sendAlertEmail sends email notifications for a triggered alert.
func (s *Service) sendAlertEmail(rule *Rule, evt *Event) {
	recipients := strings.Split(rule.EmailRecipients, ",")
	subject := fmt.Sprintf("[DigiNode CC] Alert: %s", rule.Name)
	body := fmt.Sprintf(`
		<h2>DigiNode CC — Alert Triggered</h2>
		<p><strong>Rule:</strong> %s</p>
		<p><strong>Severity:</strong> %s</p>
		<p><strong>Title:</strong> %s</p>
		<p><strong>Message:</strong> %s</p>
		<p><strong>Time:</strong> %s</p>
	`, rule.Name, string(evt.Severity), evt.Title, evt.Message, evt.CreatedAt.Format(time.RFC3339))

	for _, to := range recipients {
		to = strings.TrimSpace(to)
		if to == "" {
			continue
		}
		if err := s.emailSender(to, subject, body); err != nil {
			slog.Error("failed to send alert email", "to", to, "rule", rule.Name, "error", err)
		}
	}
}

// CreateRule adds a new alert rule.
func (s *Service) CreateRule(ctx context.Context, r *Rule) error {
	condJSON, _ := json.Marshal(r.Condition)
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO alert_rules (name, description, condition, severity, enabled, cooldown_seconds,
			notify_webhook, notify_email, email_recipients, notify_visual, notify_audible)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id`,
		r.Name, r.Description, condJSON, string(r.Severity), r.Enabled, r.CooldownSeconds,
		r.NotifyWebhook, r.NotifyEmail, r.EmailRecipients, r.NotifyVisual, r.NotifyAudible,
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
			severity = $5, enabled = $6, cooldown_seconds = $7,
			notify_webhook = $8, notify_email = $9, email_recipients = $10,
			notify_visual = $11, notify_audible = $12
		WHERE id = $1`,
		id, r.Name, r.Description, condJSON, string(r.Severity), r.Enabled, r.CooldownSeconds,
		r.NotifyWebhook, r.NotifyEmail, r.EmailRecipients, r.NotifyVisual, r.NotifyAudible)
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
