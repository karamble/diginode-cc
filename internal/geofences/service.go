package geofences

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

var ErrGeofenceNotFound = errors.New("geofence not found")

// Action determines what happens when a geofence is triggered.
type Action string

const (
	ActionAlert Action = "ALERT"
	ActionLog   Action = "LOG"
	ActionAlarm Action = "ALARM"
)

// Point represents a lat/lng coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Geofence defines a geographic boundary polygon.
type Geofence struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Polygon     []Point `json:"polygon"`
	Action      Action  `json:"action"`
	Enabled     bool    `json:"enabled"`
}

// Service manages geofences and checks points against them.
type Service struct {
	db        *database.DB
	hub       *ws.Hub
	geofences map[string]*Geofence
	mu        sync.RWMutex
}

// NewService creates a new geofence service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:        db,
		hub:       hub,
		geofences: make(map[string]*Geofence),
	}
}

// Load loads all geofences from the database.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, description, polygon, action, enabled
		FROM geofences WHERE enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var g Geofence
		var polygonJSON []byte
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &polygonJSON, &g.Action, &g.Enabled); err != nil {
			continue
		}
		json.Unmarshal(polygonJSON, &g.Polygon)
		s.geofences[g.ID] = &g
	}

	slog.Info("loaded geofences", "count", len(s.geofences))
	return nil
}

// CheckPoint tests if a point is inside any active geofence.
// Returns the triggered geofences.
func (s *Service) CheckPoint(lat, lng float64) []*Geofence {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var triggered []*Geofence
	pt := Point{Lat: lat, Lng: lng}

	for _, g := range s.geofences {
		if !g.Enabled {
			continue
		}
		if pointInPolygon(pt, g.Polygon) {
			triggered = append(triggered, g)
		}
	}

	return triggered
}

// pointInPolygon uses ray casting to determine if a point is inside a polygon.
func pointInPolygon(pt Point, polygon []Point) bool {
	n := len(polygon)
	if n < 3 {
		return false
	}

	inside := false
	j := n - 1

	for i := 0; i < n; i++ {
		yi := polygon[i].Lat
		xi := polygon[i].Lng
		yj := polygon[j].Lat
		xj := polygon[j].Lng

		if ((yi > pt.Lat) != (yj > pt.Lat)) &&
			(pt.Lng < (xj-xi)*(pt.Lat-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}

	return inside
}

// Create adds a new geofence.
func (s *Service) Create(ctx context.Context, g *Geofence) error {
	polygonJSON, _ := json.Marshal(g.Polygon)

	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO geofences (name, description, polygon, action, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		g.Name, g.Description, polygonJSON, string(g.Action), g.Enabled,
	).Scan(&g.ID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.geofences[g.ID] = g
	s.mu.Unlock()

	return nil
}

// Delete removes a geofence.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM geofences WHERE id = $1`, id)
	if err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.geofences, id)
	s.mu.Unlock()

	return nil
}

// GetAll returns all geofences.
func (s *Service) GetAll() []*Geofence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Geofence, 0, len(s.geofences))
	for _, g := range s.geofences {
		result = append(result, g)
	}
	return result
}

// NotifyViolation broadcasts a geofence event.
func (s *Service) NotifyViolation(geofence *Geofence, entityType string, entityID string, lat, lng float64) {
	s.hub.Broadcast(ws.Event{
		Type: ws.EventGeofence,
		Payload: map[string]interface{}{
			"geofence":   geofence,
			"entityType": entityType,
			"entityId":   entityID,
			"latitude":   lat,
			"longitude":  lng,
			"timestamp":  time.Now(),
		},
	})
}
