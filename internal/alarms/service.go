package alarms

import (
	"context"
	"log/slog"

	"github.com/karamble/diginode-cc/internal/database"
)

// AlarmConfig represents an alarm configuration.
type AlarmConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AlarmType     string   `json:"alarmType"` // "audio", "visual", "both"
	SoundFile     string   `json:"soundFile,omitempty"`
	TriggerEvents []string `json:"triggerEvents"`
	Enabled       bool     `json:"enabled"`
}

// Service manages alarm configurations.
type Service struct {
	db     *database.DB
	alarms []*AlarmConfig
}

// NewService creates a new alarm service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// Load loads alarm configs from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, alarm_type, sound_file, trigger_events, enabled
		FROM alarm_configs WHERE enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.alarms = nil
	for rows.Next() {
		var a AlarmConfig
		if err := rows.Scan(&a.ID, &a.Name, &a.AlarmType, &a.SoundFile,
			&a.TriggerEvents, &a.Enabled); err != nil {
			continue
		}
		s.alarms = append(s.alarms, &a)
	}

	slog.Info("loaded alarm configs", "count", len(s.alarms))
	return nil
}

// ShouldTrigger checks if an event type should trigger any alarm.
func (s *Service) ShouldTrigger(eventType string) []*AlarmConfig {
	var matching []*AlarmConfig
	for _, a := range s.alarms {
		if !a.Enabled {
			continue
		}
		for _, evt := range a.TriggerEvents {
			if evt == eventType || evt == "*" {
				matching = append(matching, a)
				break
			}
		}
	}
	return matching
}

// GetAll returns all alarm configs.
func (s *Service) GetAll() []*AlarmConfig {
	return s.alarms
}
