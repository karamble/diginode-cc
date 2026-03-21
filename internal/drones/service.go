package drones

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Status represents a drone's operational status.
type Status string

const (
	StatusUnknown  Status = "UNKNOWN"
	StatusFriendly Status = "FRIENDLY"
	StatusNeutral  Status = "NEUTRAL"
	StatusHostile  Status = "HOSTILE"
)

// Drone represents a detected drone.
type Drone struct {
	ID             string                 `json:"id"`
	MAC            string                 `json:"mac,omitempty"`
	SerialNumber   string                 `json:"serialNumber,omitempty"`
	UASID          string                 `json:"uasId,omitempty"`
	OperatorID     string                 `json:"operatorId,omitempty"`
	Description    string                 `json:"description,omitempty"`
	UAType         string                 `json:"uaType,omitempty"`
	Manufacturer   string                 `json:"manufacturer,omitempty"`
	Model          string                 `json:"model,omitempty"`
	Latitude       float64                `json:"latitude,omitempty"`
	Longitude      float64                `json:"longitude,omitempty"`
	Altitude       float64                `json:"altitude,omitempty"`
	Speed          float64                `json:"speed,omitempty"`
	Heading        float64                `json:"heading,omitempty"`
	VerticalSpeed  float64                `json:"verticalSpeed,omitempty"`
	PilotLatitude  float64                `json:"pilotLatitude,omitempty"`
	PilotLongitude float64                `json:"pilotLongitude,omitempty"`
	RSSI           int                    `json:"rssi,omitempty"`
	Status         Status                 `json:"status"`
	Source         string                 `json:"source,omitempty"`
	NodeRefID      string                 `json:"nodeId,omitempty"`
	SiteID         string                 `json:"siteId,omitempty"`
	OriginSiteID   string                 `json:"originSiteId,omitempty"`
	FAAData        map[string]interface{} `json:"faaData,omitempty"`
	FirstSeen      time.Time              `json:"firstSeen"`
	LastSeen       time.Time              `json:"lastSeen"`
}

// Service manages drone detection and tracking.
type Service struct {
	db     *database.DB
	hub    *ws.Hub
	drones map[string]*Drone // keyed by MAC or serial
	mu     sync.RWMutex
}

// NewService creates a new drone tracking service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:     db,
		hub:    hub,
		drones: make(map[string]*Drone),
	}
}

// GetAll returns all tracked drones.
func (s *Service) GetAll() []*Drone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Drone, 0, len(s.drones))
	for _, d := range s.drones {
		result = append(result, d)
	}
	return result
}

// GetActive returns only active drones.
func (s *Service) GetActive() []*Drone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Drone
	for _, d := range s.drones {
		if d.Status == StatusUnknown {
			result = append(result, d)
		}
	}
	return result
}

// GetByKey returns a drone by its key (MAC, serial, or UAS ID).
func (s *Service) GetByKey(key string) *Drone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drones[key]
}

// UpdateStatus changes a drone's status.
func (s *Service) UpdateStatus(key string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	drone, exists := s.drones[key]
	if !exists {
		return ErrDroneNotFound
	}

	drone.Status = status
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventDroneStatus,
		Payload: drone,
	})

	go s.persistDrone(drone)
	return nil
}

// HandleDetection processes a new drone detection from the mesh.
func (s *Service) HandleDetection(detection *DroneDetection) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := detection.Key()
	drone, exists := s.drones[key]

	if !exists {
		drone = &Drone{
			MAC:          detection.MAC,
			SerialNumber: detection.SerialNumber,
			UASID:        detection.UASID,
			OperatorID:   detection.OperatorID,
			UAType:       detection.UAType,
			Status:       StatusUnknown,
			Source:        detection.Source,
			FirstSeen:    time.Now(),
		}
		s.drones[key] = drone
		slog.Info("new drone detected",
			"mac", detection.MAC,
			"serial", detection.SerialNumber,
			"source", detection.Source)
	}

	// Update telemetry
	drone.Latitude = detection.Latitude
	drone.Longitude = detection.Longitude
	drone.Altitude = detection.Altitude
	drone.Speed = detection.Speed
	drone.Heading = detection.Heading
	drone.VerticalSpeed = detection.VerticalSpeed
	drone.PilotLatitude = detection.PilotLatitude
	drone.PilotLongitude = detection.PilotLongitude
	drone.RSSI = detection.RSSI
	drone.LastSeen = time.Now()

	if detection.Manufacturer != "" {
		drone.Manufacturer = detection.Manufacturer
	}
	if detection.Model != "" {
		drone.Model = detection.Model
	}

	// Broadcast telemetry
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventDroneTelemetry,
		Payload: drone,
	})

	go s.persistDrone(drone)
	go s.persistDetection(drone, detection)
}

// HandleDroneDetection implements DroneHandler for the dispatcher.
func (s *Service) HandleDroneDetection(from uint32, payload []byte) {
	detection, err := ParseDetectionPayload(payload)
	if err != nil {
		slog.Warn("failed to parse drone detection", "from", from, "error", err)
		return
	}
	detection.Source = "mesh"
	s.HandleDetection(detection)
}

// Remove deletes a drone from tracking and broadcasts removal.
func (s *Service) Remove(key string) {
	s.mu.Lock()
	drone, exists := s.drones[key]
	if !exists {
		s.mu.Unlock()
		return
	}
	delete(s.drones, key)
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type: ws.EventDroneRemove,
		Payload: map[string]interface{}{
			"droneId": drone.ID,
			"id":      drone.ID,
			"mac":     drone.MAC,
		},
	})
}

func (s *Service) persistDrone(drone *Drone) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var faaJSON []byte
	if drone.FAAData != nil {
		faaJSON, _ = json.Marshal(drone.FAAData)
	}

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO drones (mac, serial_number, uas_id, operator_id, ua_type,
			manufacturer, model, latitude, longitude, altitude, speed, heading,
			vertical_speed, pilot_latitude, pilot_longitude, rssi, status, source,
			faa_data, first_seen, last_seen, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, NOW())
		ON CONFLICT (mac) WHERE mac IS NOT NULL DO UPDATE SET
			latitude = EXCLUDED.latitude,
			longitude = EXCLUDED.longitude,
			altitude = EXCLUDED.altitude,
			speed = EXCLUDED.speed,
			heading = EXCLUDED.heading,
			vertical_speed = EXCLUDED.vertical_speed,
			pilot_latitude = EXCLUDED.pilot_latitude,
			pilot_longitude = EXCLUDED.pilot_longitude,
			rssi = EXCLUDED.rssi,
			last_seen = EXCLUDED.last_seen,
			updated_at = NOW()`,
		drone.MAC, drone.SerialNumber, drone.UASID, drone.OperatorID, drone.UAType,
		drone.Manufacturer, drone.Model,
		drone.Latitude, drone.Longitude, drone.Altitude,
		drone.Speed, drone.Heading, drone.VerticalSpeed,
		drone.PilotLatitude, drone.PilotLongitude,
		drone.RSSI, string(drone.Status), drone.Source,
		faaJSON, drone.FirstSeen, drone.LastSeen,
	)
	if err != nil {
		slog.Error("failed to persist drone", "mac", drone.MAC, "error", err)
	}
}

func (s *Service) persistDetection(drone *Drone, det *DroneDetection) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rawJSON, _ := json.Marshal(det)

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO drone_detections (mac, serial_number, latitude, longitude, altitude,
			speed, heading, rssi, source, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		drone.MAC, drone.SerialNumber,
		det.Latitude, det.Longitude, det.Altitude,
		det.Speed, det.Heading, det.RSSI,
		det.Source, rawJSON,
	)
	if err != nil {
		slog.Error("failed to persist drone detection", "error", err)
	}
}
