package geofences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Description      string  `json:"description,omitempty"`
	Color            string  `json:"color,omitempty"`
	Polygon          []Point `json:"polygon"`
	Action           Action  `json:"action"`
	Enabled          bool    `json:"enabled"`
	AlarmEnabled     bool    `json:"alarmEnabled"`
	AlarmLevel       string  `json:"alarmLevel,omitempty"`       // INFO/NOTICE/ALERT/CRITICAL
	AlarmMessage     string  `json:"alarmMessage,omitempty"`
	TriggerOnEntry   bool    `json:"triggerOnEntry"`
	TriggerOnExit    bool    `json:"triggerOnExit"`
	AppliesToADSB    bool    `json:"appliesToAdsb"`
	AppliesToDrones  bool    `json:"appliesToDrones"`
	AppliesToTargets bool    `json:"appliesToTargets"`
	AppliesToDevices bool    `json:"appliesToDevices"`
	SiteID           string  `json:"siteId,omitempty"`
	OriginSiteID     string  `json:"originSiteId,omitempty"`
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
		SELECT id, name, description, polygon, action, enabled,
			color, alarm_enabled, alarm_level, alarm_message,
			trigger_on_entry, trigger_on_exit,
			applies_to_adsb, applies_to_drones, applies_to_targets, applies_to_devices,
			site_id, origin_site_id
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
		var color, alarmLevel, alarmMessage, siteID, originSiteID sql.NullString
		if err := rows.Scan(
			&g.ID, &g.Name, &g.Description, &polygonJSON, &g.Action, &g.Enabled,
			&color, &g.AlarmEnabled, &alarmLevel, &alarmMessage,
			&g.TriggerOnEntry, &g.TriggerOnExit,
			&g.AppliesToADSB, &g.AppliesToDrones, &g.AppliesToTargets, &g.AppliesToDevices,
			&siteID, &originSiteID,
		); err != nil {
			slog.Warn("failed to scan geofence row", "error", err)
			continue
		}
		json.Unmarshal(polygonJSON, &g.Polygon)
		g.Color = color.String
		g.AlarmLevel = alarmLevel.String
		g.AlarmMessage = alarmMessage.String
		g.SiteID = siteID.String
		g.OriginSiteID = originSiteID.String
		s.geofences[g.ID] = &g
	}

	slog.Info("loaded geofences", "count", len(s.geofences))
	return nil
}

// CheckPoint tests if a point is inside any active geofence that applies to the given entity type.
// entityType is one of: "adsb", "drone", "target", "device".
// Returns the triggered geofences.
func (s *Service) CheckPoint(lat, lng float64, entityType string) []*Geofence {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var triggered []*Geofence
	pt := Point{Lat: lat, Lng: lng}

	for _, g := range s.geofences {
		if !g.Enabled {
			continue
		}
		// Filter by entity type
		switch entityType {
		case "adsb":
			if !g.AppliesToADSB {
				continue
			}
		case "drone":
			if !g.AppliesToDrones {
				continue
			}
		case "target":
			if !g.AppliesToTargets {
				continue
			}
		case "device":
			if !g.AppliesToDevices {
				continue
			}
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

// nullIfEmpty returns a sql.NullString that is NULL when the input is empty.
func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Create adds a new geofence.
func (s *Service) Create(ctx context.Context, g *Geofence) error {
	polygonJSON, _ := json.Marshal(g.Polygon)

	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO geofences (name, description, polygon, action, enabled,
			color, alarm_enabled, alarm_level, alarm_message,
			trigger_on_entry, trigger_on_exit,
			applies_to_adsb, applies_to_drones, applies_to_targets, applies_to_devices,
			site_id, origin_site_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id`,
		g.Name, g.Description, polygonJSON, string(g.Action), g.Enabled,
		nullIfEmpty(g.Color), g.AlarmEnabled, nullIfEmpty(g.AlarmLevel), nullIfEmpty(g.AlarmMessage),
		g.TriggerOnEntry, g.TriggerOnExit,
		g.AppliesToADSB, g.AppliesToDrones, g.AppliesToTargets, g.AppliesToDevices,
		nullIfEmpty(g.SiteID), nullIfEmpty(g.OriginSiteID),
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

// Update modifies an existing geofence.
func (s *Service) Update(ctx context.Context, id string, g *Geofence) error {
	polygonJSON, _ := json.Marshal(g.Polygon)
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE geofences SET name = $2, description = $3, polygon = $4,
			action = $5, enabled = $6,
			color = $7, alarm_enabled = $8, alarm_level = $9, alarm_message = $10,
			trigger_on_entry = $11, trigger_on_exit = $12,
			applies_to_adsb = $13, applies_to_drones = $14, applies_to_targets = $15, applies_to_devices = $16,
			site_id = $17, origin_site_id = $18
		WHERE id = $1`,
		id, g.Name, g.Description, polygonJSON, string(g.Action), g.Enabled,
		nullIfEmpty(g.Color), g.AlarmEnabled, nullIfEmpty(g.AlarmLevel), nullIfEmpty(g.AlarmMessage),
		g.TriggerOnEntry, g.TriggerOnExit,
		g.AppliesToADSB, g.AppliesToDrones, g.AppliesToTargets, g.AppliesToDevices,
		nullIfEmpty(g.SiteID), nullIfEmpty(g.OriginSiteID),
	)
	if err != nil {
		return err
	}
	s.mu.Lock()
	g.ID = id
	s.geofences[id] = g
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

// NotifyViolation broadcasts a geofence event with alarm metadata.
func (s *Service) NotifyViolation(geofence *Geofence, entityType string, entityID string, lat, lng float64) {
	// Format alarm message with template placeholders
	msg := geofence.AlarmMessage
	msg = strings.Replace(msg, "{entity}", entityType+"/"+entityID, 1)
	msg = strings.Replace(msg, "{geofence}", geofence.Name, 1)

	s.hub.Broadcast(ws.Event{
		Type: ws.EventGeofence,
		Payload: map[string]interface{}{
			"geofence":   geofence,
			"entityType": entityType,
			"entityId":   entityID,
			"latitude":   lat,
			"longitude":  lng,
			"alarmLevel": geofence.AlarmLevel,
			"message":    msg,
			"timestamp":  time.Now(),
		},
	})
}
