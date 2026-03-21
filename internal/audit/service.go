package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// Entry represents a single audit log record.
type Entry struct {
	ID         string                 `json:"id"`
	UserID     string                 `json:"userId,omitempty"`
	Action     string                 `json:"action"`
	Resource   string                 `json:"resource,omitempty"`
	ResourceID string                 `json:"resourceId,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	IPAddress  string                 `json:"ipAddress,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
}

// Service provides audit logging backed by the audit_logs table.
type Service struct {
	db *database.DB
}

// NewService creates a new audit logging service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// Log writes an audit entry. Errors are logged but never returned to avoid
// disrupting the caller's business logic.
func (s *Service) Log(ctx context.Context, userID, action, resource, resourceID, ipAddress string, details map[string]interface{}) {
	detailsJSON, _ := json.Marshal(details)
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource, resource_id, details, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		nullStr(userID), action, nullStr(resource), nullStr(resourceID), detailsJSON, nullStr(ipAddress))
	if err != nil {
		slog.Error("audit log failed", "action", action, "error", err)
	}
}

// GetRecent returns the most recent audit log entries across all users.
func (s *Service) GetRecent(ctx context.Context, limit int) ([]*Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, user_id, action, resource, resource_id, details, ip_address, timestamp
		FROM audit_logs ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		var e Entry
		var detailsJSON []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.Resource, &e.ResourceID,
			&detailsJSON, &e.IPAddress, &e.Timestamp); err != nil {
			continue
		}
		if detailsJSON != nil {
			json.Unmarshal(detailsJSON, &e.Details)
		}
		entries = append(entries, &e)
	}
	return entries, nil
}

// Prune deletes audit log entries older than the given retention period.
func (s *Service) Prune(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 365
	}
	result, err := s.db.Pool.Exec(ctx, `
		DELETE FROM audit_logs WHERE timestamp < NOW() - $1 * INTERVAL '1 day'`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// nullStr returns nil for empty strings (maps to SQL NULL).
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
