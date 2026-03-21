package firewall

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

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
