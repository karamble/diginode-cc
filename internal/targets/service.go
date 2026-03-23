package targets

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

var ErrTargetNotFound = errors.New("target not found")

// Target represents a tracked entity.
type Target struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	TargetType  string    `json:"targetType,omitempty"`
	MAC         string    `json:"mac,omitempty"`
	Latitude    float64   `json:"latitude,omitempty"`
	Longitude   float64   `json:"longitude,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// PositionFix represents a triangulated position.
type PositionFix struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	AccuracyM float64   `json:"accuracyM"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// Service manages target tracking and triangulation.
type Service struct {
	db      *database.DB
	hub     *ws.Hub
	targets map[string]*Target
	mu      sync.RWMutex
}

// NewService creates a new target tracking service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:      db,
		hub:     hub,
		targets: make(map[string]*Target),
	}
}

// Load populates the in-memory cache from the database on startup.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, description, target_type, mac, latitude, longitude, status, created_at, updated_at
		FROM targets`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for rows.Next() {
		var t Target
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.TargetType, &t.MAC,
			&t.Latitude, &t.Longitude, &t.Status, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			slog.Warn("failed to scan target", "error", err)
			continue
		}
		s.targets[t.ID] = &t
		count++
	}

	slog.Info("loaded targets", "count", count)
	return nil
}

// GetAll returns all targets.
func (s *Service) GetAll() []*Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Target, 0, len(s.targets))
	for _, t := range s.targets {
		result = append(result, t)
	}
	return result
}

// Create adds a new target.
func (s *Service) Create(ctx context.Context, t *Target) error {
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO targets (name, description, target_type, mac, latitude, longitude, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		t.Name, t.Description, t.TargetType, t.MAC,
		t.Latitude, t.Longitude, t.Status,
	).Scan(&t.ID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.targets[t.ID] = t
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: t,
	})

	return nil
}

// Delete removes a target.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM targets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.targets, id)
	s.mu.Unlock()
	return nil
}

// Update modifies an existing target.
func (s *Service) Update(ctx context.Context, id string, t *Target) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET name = $2, description = $3, target_type = $4,
			mac = $5, status = $6, updated_at = NOW()
		WHERE id = $1`,
		id, t.Name, t.Description, t.TargetType, t.MAC, t.Status)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing, ok := s.targets[id]; ok {
		existing.Name = t.Name
		existing.Description = t.Description
		existing.TargetType = t.TargetType
		existing.MAC = t.MAC
		existing.Status = t.Status
	}
	s.mu.Unlock()
	return nil
}

// ClearAll removes all targets from memory and the database.
func (s *Service) ClearAll(ctx context.Context) error {
	s.mu.Lock()
	s.targets = make(map[string]*Target)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM targets`)
	return err
}

// Resolve marks a target as resolved.
func (s *Service) Resolve(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET status = 'resolved', updated_at = NOW()
		WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing, ok := s.targets[id]; ok {
		existing.Status = "resolved"
		existing.UpdatedAt = time.Now()
	}
	s.mu.Unlock()
	return nil
}

// UpdatePosition records a new position fix for a target.
func (s *Service) UpdatePosition(ctx context.Context, targetID string, fix *PositionFix) error {
	s.mu.Lock()
	t, exists := s.targets[targetID]
	if !exists {
		s.mu.Unlock()
		return ErrTargetNotFound
	}
	t.Latitude = fix.Latitude
	t.Longitude = fix.Longitude
	t.UpdatedAt = time.Now()
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO target_positions (target_id, latitude, longitude, accuracy_m, source)
		VALUES ($1, $2, $3, $4, $5)`,
		targetID, fix.Latitude, fix.Longitude, fix.AccuracyM, fix.Source,
	)
	if err != nil {
		slog.Error("failed to persist target position", "error", err)
		return err
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: t,
	})

	return nil
}

// Triangulate estimates a target position from multiple signal observations.
// Uses weighted centroid based on signal strength / distance estimates.
func Triangulate(observations []Observation) *PositionFix {
	if len(observations) < 2 {
		return nil
	}

	var totalWeight float64
	var weightedLat, weightedLng float64

	for _, obs := range observations {
		// Weight by inverse square of estimated distance
		dist := estimateDistance(obs.RSSI)
		if dist <= 0 {
			dist = 1
		}
		weight := 1.0 / (dist * dist)

		weightedLat += obs.Latitude * weight
		weightedLng += obs.Longitude * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return nil
	}

	return &PositionFix{
		Latitude:  weightedLat / totalWeight,
		Longitude: weightedLng / totalWeight,
		AccuracyM: estimateAccuracy(observations),
		Source:    "triangulation",
		Timestamp: time.Now(),
	}
}

// Observation is a single signal measurement from a known location.
type Observation struct {
	Latitude  float64
	Longitude float64
	RSSI      int
}

// estimateDistance estimates distance in meters from RSSI (free-space path loss).
func estimateDistance(rssi int) float64 {
	// Log-distance path loss model
	// RSSI = -10 * n * log10(d) + A
	// where n=2 (free space), A=-30 (reference at 1m)
	n := 2.0
	a := -30.0
	return math.Pow(10, (a-float64(rssi))/(10*n))
}

func estimateAccuracy(observations []Observation) float64 {
	if len(observations) < 2 {
		return 1000
	}
	// Rough accuracy estimate based on number of observations
	return math.Max(10, 500/float64(len(observations)))
}
