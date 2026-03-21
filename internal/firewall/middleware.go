package firewall

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// Rule represents a firewall rule.
type Rule struct {
	ID       string `json:"id"`
	RuleType string `json:"ruleType"` // "ip", "cidr", "country"
	Value    string `json:"value"`
	Action   string `json:"action"` // "block", "allow"
	Reason   string `json:"reason,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// Service manages IP/geo-blocking.
type Service struct {
	db    *database.DB
	rules []*Rule
	mu    sync.RWMutex
}

// NewService creates a new firewall service.
func NewService(db *database.DB) *Service {
	return &Service{
		db: db,
	}
}

// Load loads firewall rules from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, rule_type, value, action, reason, enabled
		FROM firewall_rules WHERE enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rules = nil
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Value, &r.Action, &r.Reason, &r.Enabled); err != nil {
			continue
		}
		s.rules = append(s.rules, &r)
	}

	slog.Info("loaded firewall rules", "count", len(s.rules))
	return nil
}

// Middleware returns HTTP middleware that blocks requests based on rules.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if s.isBlocked(ip) {
			slog.Warn("request blocked by firewall", "ip", ip)
			http.Error(w, `{"error":"access denied"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) isBlocked(ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, rule := range s.rules {
		if !rule.Enabled || rule.Action != "block" {
			continue
		}

		switch rule.RuleType {
		case "ip":
			if rule.Value == ip {
				return true
			}
		case "cidr":
			_, cidr, err := net.ParseCIDR(rule.Value)
			if err == nil && cidr.Contains(parsedIP) {
				return true
			}
		}
	}

	return false
}

// GetRules returns all firewall rules.
func (s *Service) GetRules() []*Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Rule, len(s.rules))
	copy(result, s.rules)
	return result
}

// CreateRule adds a new firewall rule.
func (s *Service) CreateRule(ctx context.Context, r *Rule) error {
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO firewall_rules (rule_type, value, action, reason, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		r.RuleType, r.Value, r.Action, r.Reason, r.Enabled,
	).Scan(&r.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rules = append(s.rules, r)
	s.mu.Unlock()
	return nil
}

// DeleteRule removes a firewall rule.
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM firewall_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// JailedIP represents a temporarily blocked IP (TEMP_BLOCK rule).
type JailedIP struct {
	ID        string    `json:"id"`
	Value     string    `json:"value"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// AuditEntry represents a firewall audit log entry.
type AuditEntry struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource,omitempty"`
	ResourceID string    `json:"resourceId,omitempty"`
	Details    []byte    `json:"details,omitempty"`
	IPAddress  string    `json:"ipAddress,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// RuleCount returns the number of active firewall rules.
func (s *Service) RuleCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rules)
}

// GetJailedIPs returns all temporary block rules that have not yet expired.
func (s *Service) GetJailedIPs(ctx context.Context) ([]*JailedIP, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, value, reason, expires_at, created_at
		FROM firewall_rules
		WHERE rule_type = 'TEMP_BLOCK' AND expires_at > NOW()
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jailed []*JailedIP
	for rows.Next() {
		var j JailedIP
		if err := rows.Scan(&j.ID, &j.Value, &j.Reason, &j.ExpiresAt, &j.CreatedAt); err != nil {
			continue
		}
		jailed = append(jailed, &j)
	}
	return jailed, nil
}

// UnjailIP removes a temporary block rule by ID.
func (s *Service) UnjailIP(ctx context.Context, id string) error {
	tag, err := s.db.Pool.Exec(ctx,
		`DELETE FROM firewall_rules WHERE id = $1 AND rule_type = 'TEMP_BLOCK'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	// Also remove from in-memory rules
	s.mu.Lock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// GetFirewallLogs returns recent audit log entries related to firewall actions.
func (s *Service) GetFirewallLogs(ctx context.Context, limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, action, resource, resource_id, details, ip_address, timestamp
		FROM audit_log
		WHERE action LIKE 'firewall%'
		ORDER BY timestamp DESC
		LIMIT $1`, limit)
	if err != nil {
		// Fall back to audit_logs table (migration 003 created both names)
		rows, err = s.db.Pool.Query(ctx, `
			SELECT id, action, resource, resource_id, details, ip_address, timestamp
			FROM audit_logs
			WHERE action LIKE 'firewall%'
			ORDER BY timestamp DESC
			LIMIT $1`, limit)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Action, &e.Resource, &e.ResourceID,
			&e.Details, &e.IPAddress, &e.Timestamp); err != nil {
			continue
		}
		entries = append(entries, &e)
	}
	return entries, nil
}

// errNotFound is returned when the requested resource does not exist.
var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "not found" }

// IsNotFound reports whether err is a not-found error.
func IsNotFound(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

func extractIP(r *http.Request) string {
	// Check X-Forwarded-For first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to remote address
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
