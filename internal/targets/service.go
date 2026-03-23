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
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	TargetType            string    `json:"targetType,omitempty"`
	MAC                   string    `json:"mac,omitempty"`
	Latitude              float64   `json:"latitude,omitempty"`
	Longitude             float64   `json:"longitude,omitempty"`
	Status                string    `json:"status"`
	URL                   string    `json:"url,omitempty"`
	Tags                  []string  `json:"tags,omitempty"`
	Notes                 string    `json:"notes,omitempty"`
	CreatedBy             string    `json:"createdBy,omitempty"`
	FirstNodeID           string    `json:"firstNodeId,omitempty"`
	TrackingConfidence    *float64  `json:"trackingConfidence,omitempty"`
	TrackingUncertainty   *float64  `json:"trackingUncertainty,omitempty"`
	TriangulationMethod   string    `json:"triangulationMethod,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
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
		SELECT id, name, COALESCE(description,''), COALESCE(target_type,''), COALESCE(mac,''),
			COALESCE(latitude,0), COALESCE(longitude,0), COALESCE(status,'active'),
			COALESCE(url,''), tags, COALESCE(notes,''), COALESCE(created_by,''),
			COALESCE(first_node_id,''), tracking_confidence, tracking_uncertainty,
			COALESCE(triangulation_method,''),
			created_at, updated_at
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
			&t.Latitude, &t.Longitude, &t.Status,
			&t.URL, &t.Tags, &t.Notes, &t.CreatedBy,
			&t.FirstNodeID, &t.TrackingConfidence, &t.TrackingUncertainty,
			&t.TriangulationMethod,
			&t.CreatedAt, &t.UpdatedAt,
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
	if t.Status == "" {
		t.Status = "active"
	}
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO targets (name, description, target_type, mac, latitude, longitude, status,
			url, tags, notes, created_by, first_node_id, tracking_confidence, tracking_uncertainty, triangulation_method)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`,
		t.Name, t.Description, t.TargetType, t.MAC,
		t.Latitude, t.Longitude, t.Status,
		nilStr(t.URL), t.Tags, nilStr(t.Notes), nilStr(t.CreatedBy),
		nilStr(t.FirstNodeID), t.TrackingConfidence, t.TrackingUncertainty, nilStr(t.TriangulationMethod),
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
			mac = $5, status = $6, url = $7, tags = $8, notes = $9,
			updated_at = NOW()
		WHERE id = $1`,
		id, t.Name, t.Description, t.TargetType, t.MAC, t.Status,
		nilStr(t.URL), t.Tags, nilStr(t.Notes))
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
		existing.URL = t.URL
		existing.Tags = t.Tags
		existing.Notes = t.Notes
	}
	s.mu.Unlock()
	return nil
}

// ApplyTrackingEstimate updates a target's position and tracking confidence.
// Called by the tracking service when a new position estimate is computed.
func (s *Service) ApplyTrackingEstimate(ctx context.Context, mac string, lat, lon, confidence, uncertainty float64, method string) error {
	s.mu.Lock()
	// Find target by MAC
	var target *Target
	for _, t := range s.targets {
		if t.MAC == mac {
			target = t
			break
		}
	}

	if target == nil {
		s.mu.Unlock()
		return ErrTargetNotFound
	}

	target.Latitude = lat
	target.Longitude = lon
	target.TrackingConfidence = &confidence
	target.TrackingUncertainty = &uncertainty
	if method != "" {
		target.TriangulationMethod = method
	}
	if confidence > 0.5 {
		target.Status = "active"
	}
	target.UpdatedAt = time.Now()
	id := target.ID
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET latitude = $2, longitude = $3,
			tracking_confidence = $4, tracking_uncertainty = $5,
			triangulation_method = $6, status = $7, updated_at = NOW()
		WHERE id = $1`,
		id, lat, lon, confidence, uncertainty, nilStr(method), target.Status)
	if err != nil {
		return err
	}

	// Persist position history
	s.db.Pool.Exec(ctx, `
		INSERT INTO target_positions (target_id, latitude, longitude, accuracy_m, source)
		VALUES ($1, $2, $3, $4, $5)`,
		id, lat, lon, uncertainty, method)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: target,
	})

	return nil
}

// EnsureTargetExists creates a placeholder target for a MAC if one doesn't exist.
// Used during T_D processing when triangulation detects a new MAC.
func (s *Service) EnsureTargetExists(ctx context.Context, mac, nodeID string) *Target {
	s.mu.RLock()
	for _, t := range s.targets {
		if t.MAC == mac {
			s.mu.RUnlock()
			return t
		}
	}
	s.mu.RUnlock()

	// Create placeholder
	t := &Target{
		Name:        "Auto: " + mac,
		MAC:         mac,
		Status:      "active",
		FirstNodeID: nodeID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.Create(ctx, t); err != nil {
		slog.Warn("failed to auto-create target", "mac", mac, "error", err)
		return nil
	}
	slog.Info("auto-created target for triangulation", "mac", mac, "id", t.ID)
	return t
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

func nilStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
