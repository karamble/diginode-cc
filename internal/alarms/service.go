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

// Create adds a new alarm configuration.
func (s *Service) Create(ctx context.Context, a *AlarmConfig) error {
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO alarm_configs (name, alarm_type, sound_file, trigger_events, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		a.Name, a.AlarmType, a.SoundFile, a.TriggerEvents, a.Enabled,
	).Scan(&a.ID)
	if err != nil {
		return err
	}
	s.alarms = append(s.alarms, a)
	return nil
}

// Delete removes an alarm configuration.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM alarm_configs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	for i, a := range s.alarms {
		if a.ID == id {
			s.alarms = append(s.alarms[:i], s.alarms[i+1:]...)
			break
		}
	}
	return nil
}

// Update modifies an existing alarm config.
func (s *Service) Update(ctx context.Context, id string, a *AlarmConfig) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE alarm_configs SET name = $2, alarm_type = $3, sound_file = $4,
			trigger_events = $5, enabled = $6
		WHERE id = $1`,
		id, a.Name, a.AlarmType, a.SoundFile, a.TriggerEvents, a.Enabled)
	if err != nil {
		return err
	}

	// Update in-memory list
	for i, existing := range s.alarms {
		if existing.ID == id {
			a.ID = id
			s.alarms[i] = a
			return nil
		}
	}
	return nil
}

// GetAll returns all alarm configs.
func (s *Service) GetAll() []*AlarmConfig {
	return s.alarms
}

// SetSoundFile stores a sound file reference for the given alarm level.
func (s *Service) SetSoundFile(ctx context.Context, level string, soundFile string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO alarm_sounds (level, sound_file)
		VALUES ($1, $2)
		ON CONFLICT (level) DO UPDATE SET sound_file = EXCLUDED.sound_file`,
		level, soundFile,
	)
	return err
}

// DeleteSoundFile removes the sound file reference for the given alarm level.
func (s *Service) DeleteSoundFile(ctx context.Context, level string) error {
	tag, err := s.db.Pool.Exec(ctx, `DELETE FROM alarm_sounds WHERE level = $1`, level)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errAlarmSoundNotFound
	}
	return nil
}

var errAlarmSoundNotFound = &alarmSoundNotFoundError{}

type alarmSoundNotFoundError struct{}

func (e *alarmSoundNotFoundError) Error() string { return "alarm sound not found" }

// IsAlarmSoundNotFound reports whether err is an alarm-sound-not-found error.
func IsAlarmSoundNotFound(err error) bool {
	_, ok := err.(*alarmSoundNotFoundError)
	return ok
}
