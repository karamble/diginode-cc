package drones

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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

// GeofenceHit represents a triggered geofence with its alarm metadata.
type GeofenceHit struct {
	ID             string
	Name           string
	AlarmLevel     string
	AlarmMessage   string
	NotifyWebhook  bool
}

// GeofenceChecker tests a coordinate against active geofences and returns hits.
// The drone service calls this on every telemetry update.
type GeofenceChecker func(lat, lon float64, entityType string) []GeofenceHit

// GeofenceNotifier broadcasts a geofence violation event.
type GeofenceNotifier func(geofenceID, geofenceName, entityType, entityID string, lat, lon float64, alarmLevel, message string, notifyWebhook bool)

// Service manages drone detection and tracking.
type Service struct {
	db          *database.DB
	hub         *ws.Hub
	drones      map[string]*Drone // keyed by MAC or serial
	mu          sync.RWMutex
	nodeLookup  func(nodeNum uint32) (nodeID, siteID string) // resolve mesh node → nodeID + siteID
	onInventory func(mac, manufacturer, ssid string, rssi int)
	faaLookup   func(ctx context.Context, droneID, mac, serial string) (map[string]interface{}, error)

	// Geofence integration
	geofenceCheck  GeofenceChecker
	geofenceNotify GeofenceNotifier
	geofenceState  map[string]map[string]bool // droneKey → geofenceID → wasInside

	// Persistence debouncing
	persistQueue map[string]struct{} // keys pending persist
	persistMu    sync.Mutex
	persistTimer *time.Timer
}

// SetNodeLookup sets the function to resolve mesh node numbers to node IDs and site IDs.
func (s *Service) SetNodeLookup(fn func(nodeNum uint32) (nodeID, siteID string)) {
	s.nodeLookup = fn
}

// SetInventoryCallback sets a callback to record detected drones in the device inventory.
func (s *Service) SetInventoryCallback(fn func(mac, manufacturer, ssid string, rssi int)) {
	s.onInventory = fn
}

// SetFAALookup sets the function used to asynchronously enrich drones with FAA registry data.
// The lookup function receives droneID, MAC, and serial number for multi-key resolution.
func (s *Service) SetFAALookup(fn func(ctx context.Context, droneID, mac, serial string) (map[string]interface{}, error)) {
	s.faaLookup = fn
}

// SetGeofenceChecker sets the function to check drone coordinates against geofences.
func (s *Service) SetGeofenceChecker(fn GeofenceChecker) {
	s.geofenceCheck = fn
}

// SetGeofenceNotifier sets the function to broadcast geofence violation events.
func (s *Service) SetGeofenceNotifier(fn GeofenceNotifier) {
	s.geofenceNotify = fn
}

// NewService creates a new drone tracking service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:            db,
		hub:           hub,
		drones:        make(map[string]*Drone),
		geofenceState: make(map[string]map[string]bool),
	}
}

// loadPersistedFAA checks if a drone MAC already has FAA data stored in the DB
// from a previous detection. Returns the FAA data if found, avoiding redundant lookups.
func (s *Service) loadPersistedFAA(ctx context.Context, mac string) map[string]interface{} {
	if mac == "" {
		return nil
	}
	var faaJSON []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT faa_data FROM drones
		WHERE mac = $1 AND faa_data IS NOT NULL
		LIMIT 1`, mac).Scan(&faaJSON)
	if err != nil || len(faaJSON) == 0 {
		return nil
	}
	var data map[string]interface{}
	if json.Unmarshal(faaJSON, &data) != nil {
		return nil
	}
	slog.Debug("loaded persisted FAA data for drone", "mac", mac)
	return data
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

// findByID looks up a drone by its UUID (iterates the map).
// Caller must hold at least s.mu.RLock.
func (s *Service) findByID(id string) (*Drone, string) {
	for key, d := range s.drones {
		if d.ID == id {
			return d, key
		}
	}
	return nil, ""
}

// GetByID looks up a drone by its UUID.
func (s *Service) GetByID(id string) *Drone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, _ := s.findByID(id)
	return d
}

// UpdateStatus changes a drone's status. Accepts either the map key (MAC/serial) or UUID.
func (s *Service) UpdateStatus(key string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	drone, exists := s.drones[key]
	if !exists {
		// Try lookup by UUID
		drone, _ = s.findByID(key)
		exists = drone != nil
	}
	if !exists {
		return ErrDroneNotFound
	}

	drone.Status = status
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventDroneStatus,
		Payload: drone,
	})

	s.enqueuePersist(key)
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
			ID:           generateUUID(),
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

	// Resolve detecting node's ID and site
	if s.nodeLookup != nil && detection.NodeNum > 0 {
		nodeID, siteID := s.nodeLookup(detection.NodeNum)
		if nodeID != "" {
			drone.NodeRefID = nodeID
		}
		if siteID != "" {
			drone.SiteID = siteID
		}
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

	// Track in inventory
	if s.onInventory != nil && drone.MAC != "" {
		s.onInventory(drone.MAC, drone.Manufacturer, "", drone.RSSI)
	}

	// Broadcast telemetry
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventDroneTelemetry,
		Payload: drone,
	})

	// Geofence evaluation
	if s.geofenceCheck != nil && drone.Latitude != 0 && drone.Longitude != 0 {
		s.evaluateGeofences(key, drone)
	}

	// FAA enrichment (async, multi-key: droneId, MAC, serial)
	hasAnyKey := drone.SerialNumber != "" || drone.MAC != "" || drone.UASID != ""
	if drone.FAAData == nil && hasAnyKey {
		droneID := drone.UASID
		mac := drone.MAC
		serial := drone.SerialNumber
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			// First check if we already have FAA data persisted from a prior detection
			if data := s.loadPersistedFAA(ctx, mac); data != nil {
				s.mu.Lock()
				drone.FAAData = data
				s.mu.Unlock()
				s.hub.Broadcast(ws.Event{
					Type:    ws.EventDroneTelemetry,
					Payload: drone,
				})
				return
			}

			// Otherwise do a fresh lookup (offline DB + online API)
			if s.faaLookup == nil {
				return
			}
			data, err := s.faaLookup(ctx, droneID, mac, serial)
			if err != nil || data == nil {
				return
			}
			s.mu.Lock()
			drone.FAAData = data
			s.mu.Unlock()
			// Re-broadcast with FAA data so frontend gets it immediately
			s.hub.Broadcast(ws.Event{
				Type:    ws.EventDroneTelemetry,
				Payload: drone,
			})
			go s.persistDrone(drone)
		}()
	}

	s.enqueuePersist(key)
	go s.persistDetection(drone, detection)
}

// evaluateGeofences checks if a drone has entered or exited any geofence.
// Tracks state transitions and fires notifications on enter/exit.
// Must be called with s.mu held (Lock).
func (s *Service) evaluateGeofences(droneKey string, drone *Drone) {
	hits := s.geofenceCheck(drone.Latitude, drone.Longitude, "drone")

	// Build set of currently-inside geofence IDs
	nowInside := make(map[string]*GeofenceHit, len(hits))
	for i := range hits {
		nowInside[hits[i].ID] = &hits[i]
	}

	// Get previous state
	prevState := s.geofenceState[droneKey]
	if prevState == nil {
		prevState = make(map[string]bool)
	}

	entityID := drone.UASID
	if entityID == "" {
		entityID = drone.MAC
	}

	// Check for entries (not previously inside, now inside)
	for id, hit := range nowInside {
		if !prevState[id] {
			slog.Info("geofence breach: drone entered",
				"drone", entityID,
				"geofence", hit.Name,
				"lat", drone.Latitude,
				"lon", drone.Longitude)
			if s.geofenceNotify != nil {
				// Format the alarm message template
				msg := strings.Replace(hit.AlarmMessage, "{entity}", "drone/"+entityID, 1)
				msg = strings.Replace(msg, "{geofence}", hit.Name, 1)
				if msg == "" {
					msg = fmt.Sprintf("drone/%s entered geofence %s", entityID, hit.Name)
				}
				s.geofenceNotify(hit.ID, hit.Name, "drone", entityID,
					drone.Latitude, drone.Longitude, hit.AlarmLevel, msg, hit.NotifyWebhook)
			}
		}
	}

	// Check for exits (previously inside, now not inside)
	for id, wasInside := range prevState {
		if wasInside && nowInside[id] == nil {
			slog.Info("geofence: drone exited",
				"drone", entityID,
				"geofenceId", id)
		}
	}

	// Update state
	newState := make(map[string]bool, len(nowInside))
	for id := range nowInside {
		newState[id] = true
	}
	s.geofenceState[droneKey] = newState
}

// HandleDroneDetection implements DroneHandler for the dispatcher.
func (s *Service) HandleDroneDetection(from uint32, payload []byte) {
	detection, err := ParseDetectionPayload(payload)
	if err != nil {
		slog.Warn("failed to parse drone detection", "from", from, "error", err)
		return
	}
	detection.Source = "mesh"
	detection.NodeNum = from
	s.HandleDetection(detection)
}

// ClearAll removes all drones from memory and the database.
func (s *Service) ClearAll(ctx context.Context) error {
	s.mu.Lock()
	s.drones = make(map[string]*Drone)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM drones`)
	return err
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
			faa_data = COALESCE(EXCLUDED.faa_data, drones.faa_data),
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

func (s *Service) enqueuePersist(key string) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	if s.persistQueue == nil {
		s.persistQueue = make(map[string]struct{})
	}
	s.persistQueue[key] = struct{}{}

	if s.persistTimer != nil {
		s.persistTimer.Stop()
	}
	s.persistTimer = time.AfterFunc(200*time.Millisecond, s.flushPersistQueue)
}

func (s *Service) flushPersistQueue() {
	s.persistMu.Lock()
	queue := s.persistQueue
	s.persistQueue = make(map[string]struct{})
	s.persistMu.Unlock()

	s.mu.RLock()
	for key := range queue {
		if drone, ok := s.drones[key]; ok {
			go s.persistDrone(drone)
		}
	}
	s.mu.RUnlock()
}

// DetectionRecord represents a stored drone detection for trail rendering.
type DetectionRecord struct {
	ID            string    `json:"id"`
	MAC           string    `json:"mac,omitempty"`
	SerialNumber  string    `json:"serialNumber,omitempty"`
	Latitude      float64   `json:"lat"`
	Longitude     float64   `json:"lon"`
	Altitude      float64   `json:"altitude,omitempty"`
	Speed         float64   `json:"speed,omitempty"`
	Heading       float64   `json:"heading,omitempty"`
	RSSI          int       `json:"rssi,omitempty"`
	Source        string    `json:"source,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// GetDetections returns recent detection records for a given drone identifier.
// Accepts MAC, serial number, or UUID (resolved to MAC via in-memory lookup).
func (s *Service) GetDetections(ctx context.Context, droneKey string, limit int) ([]DetectionRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}

	// If the key looks like a UUID, resolve to MAC for the query.
	s.mu.RLock()
	if drone, _ := s.findByID(droneKey); drone != nil && drone.MAC != "" {
		droneKey = drone.MAC
	}
	s.mu.RUnlock()

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, mac, serial_number, latitude, longitude, altitude,
			speed, heading, rssi, source, timestamp
		FROM drone_detections
		WHERE mac = $1 OR serial_number = $1
		ORDER BY timestamp DESC
		LIMIT $2`, droneKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DetectionRecord
	for rows.Next() {
		var r DetectionRecord
		var mac, serial, source *string
		var lat, lon, alt, spd, hdg *float64
		var rssi *int
		if err := rows.Scan(&r.ID, &mac, &serial, &lat, &lon, &alt, &spd, &hdg, &rssi, &source, &r.Timestamp); err != nil {
			continue
		}
		if mac != nil { r.MAC = *mac }
		if serial != nil { r.SerialNumber = *serial }
		if lat != nil { r.Latitude = *lat }
		if lon != nil { r.Longitude = *lon }
		if alt != nil { r.Altitude = *alt }
		if spd != nil { r.Speed = *spd }
		if hdg != nil { r.Heading = *hdg }
		if rssi != nil { r.RSSI = *rssi }
		if source != nil { r.Source = *source }
		records = append(records, r)
	}
	return records, nil
}

// PruneDetections removes drone detection records older than the retention period.
func (s *Service) PruneDetections(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	result, err := s.db.Pool.Exec(ctx, `
		DELETE FROM drone_detections WHERE timestamp < NOW() - $1 * INTERVAL '1 day'`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
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
