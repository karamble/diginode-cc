package inventory

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Device represents a WiFi device in the inventory.
type Device struct {
	ID                  string    `json:"id"`
	MAC                 string    `json:"mac"`
	Manufacturer        string    `json:"manufacturer,omitempty"`
	DeviceName          string    `json:"deviceName,omitempty"`
	DeviceType          string    `json:"deviceType,omitempty"`
	RSSI                int       `json:"rssi,omitempty"`
	LastSSID            string    `json:"lastSsid,omitempty"`
	FirstSeen           time.Time `json:"firstSeen"`
	LastSeen            time.Time `json:"lastSeen"`
	IsKnown             bool      `json:"isKnown"`
	Notes               string    `json:"notes,omitempty"`
	Hits                int       `json:"hits"`
	MinRSSI             int       `json:"minRssi,omitempty"`
	MaxRSSI             int       `json:"maxRssi,omitempty"`
	AvgRSSI             float64   `json:"avgRssi,omitempty"`
	LastNodeID          string    `json:"lastNodeId,omitempty"`
	LastLat             float64   `json:"lastLat,omitempty"`
	LastLon             float64   `json:"lastLon,omitempty"`
	Channel             int       `json:"channel,omitempty"`
	LocallyAdministered bool      `json:"locallyAdministered"`
	Multicast           bool      `json:"multicast"`
}

// Service manages WiFi device inventory.
type Service struct {
	db      *database.DB
	hub     *ws.Hub
	devices map[string]*Device
	mu      sync.RWMutex
}

// NewService creates a new inventory service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:      db,
		hub:     hub,
		devices: make(map[string]*Device),
	}
}

// Load populates the in-memory cache from the database on startup.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT mac, manufacturer, device_name, device_type, rssi, last_ssid,
			first_seen, last_seen, is_known, notes, hits,
			rssi_min, rssi_max, rssi_avg,
			last_node_id, last_latitude, last_longitude,
			channel, locally_administered, multicast
		FROM inventory_devices`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for rows.Next() {
		var d Device
		var manufacturer, deviceName, deviceType, lastSSID, notes, lastNodeID sql.NullString
		var lastLat, lastLon sql.NullFloat64
		var rssiMin, rssiMax, channel sql.NullInt32
		var avgRSSI sql.NullFloat64
		var locallyAdmin, multicast sql.NullBool
		if err := rows.Scan(
			&d.MAC, &manufacturer, &deviceName, &deviceType, &d.RSSI, &lastSSID,
			&d.FirstSeen, &d.LastSeen, &d.IsKnown, &notes, &d.Hits,
			&rssiMin, &rssiMax, &avgRSSI,
			&lastNodeID, &lastLat, &lastLon,
			&channel, &locallyAdmin, &multicast,
		); err != nil {
			slog.Warn("failed to scan inventory device", "error", err)
			continue
		}
		d.Manufacturer = manufacturer.String
		d.DeviceName = deviceName.String
		d.DeviceType = deviceType.String
		d.LastSSID = lastSSID.String
		d.Notes = notes.String
		d.LastNodeID = lastNodeID.String
		if lastLat.Valid {
			d.LastLat = lastLat.Float64
		}
		if lastLon.Valid {
			d.LastLon = lastLon.Float64
		}
		if rssiMin.Valid {
			d.MinRSSI = int(rssiMin.Int32)
		}
		if rssiMax.Valid {
			d.MaxRSSI = int(rssiMax.Int32)
		}
		if avgRSSI.Valid {
			d.AvgRSSI = avgRSSI.Float64
		}
		if channel.Valid {
			d.Channel = int(channel.Int32)
		}
		if locallyAdmin.Valid {
			d.LocallyAdministered = locallyAdmin.Bool
		}
		if multicast.Valid {
			d.Multicast = multicast.Bool
		}
		s.devices[d.MAC] = &d
		count++
	}

	slog.Info("loaded inventory devices", "count", count)
	return nil
}

// Track adds or updates a device in the inventory.
func (s *Service) Track(mac, manufacturer, ssid string, rssi int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dev, exists := s.devices[mac]
	if !exists {
		dev = &Device{
			MAC:                 mac,
			FirstSeen:           time.Now(),
			LocallyAdministered: IsLocallyAdministered(mac),
			Multicast:           IsMulticast(mac),
		}
		s.devices[mac] = dev
		slog.Debug("new inventory device", "mac", mac, "manufacturer", manufacturer)
	}

	if manufacturer != "" {
		dev.Manufacturer = manufacturer
	}
	if ssid != "" {
		dev.LastSSID = ssid
	}
	dev.RSSI = rssi
	dev.LastSeen = time.Now()

	// Running RSSI statistics
	dev.Hits++
	if dev.MinRSSI == 0 || rssi < dev.MinRSSI {
		dev.MinRSSI = rssi
	}
	if rssi > dev.MaxRSSI {
		dev.MaxRSSI = rssi
	}
	// Running average
	dev.AvgRSSI = dev.AvgRSSI + (float64(rssi)-dev.AvgRSSI)/float64(dev.Hits)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventInventory,
		Payload: dev,
	})

	go s.persist(dev)
}

// TrackFull adds or updates a device with full detection data from mesh sensors.
func (s *Service) TrackFull(mac, manufacturer, ssid, deviceType string, rssi int, nodeID string, lat, lon float64, channel ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dev, exists := s.devices[mac]
	if !exists {
		dev = &Device{
			MAC:                 mac,
			FirstSeen:           time.Now(),
			LocallyAdministered: IsLocallyAdministered(mac),
			Multicast:           IsMulticast(mac),
		}
		s.devices[mac] = dev
		slog.Info("new inventory device", "mac", mac, "type", deviceType, "nodeId", nodeID)
	}

	if manufacturer != "" {
		dev.Manufacturer = manufacturer
	}
	if ssid != "" {
		dev.LastSSID = ssid
	}
	if deviceType != "" {
		dev.DeviceType = deviceType
	}
	if nodeID != "" {
		dev.LastNodeID = nodeID
	}
	if lat != 0 && lon != 0 {
		dev.LastLat = lat
		dev.LastLon = lon
	}
	if len(channel) > 0 && channel[0] > 0 {
		dev.Channel = channel[0]
	}
	dev.RSSI = rssi
	dev.LastSeen = time.Now()

	// Running RSSI statistics
	dev.Hits++
	if dev.MinRSSI == 0 || rssi < dev.MinRSSI {
		dev.MinRSSI = rssi
	}
	if rssi > dev.MaxRSSI {
		dev.MaxRSSI = rssi
	}
	dev.AvgRSSI = dev.AvgRSSI + (float64(rssi)-dev.AvgRSSI)/float64(dev.Hits)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventInventory,
		Payload: dev,
	})

	go s.persist(dev)
}

// GetAll returns all devices.
func (s *Service) GetAll() []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		result = append(result, d)
	}
	return result
}

// GetByMAC returns a device by its MAC address.
func (s *Service) GetByMAC(mac string) *Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.devices[mac]
}

// ClearAll removes all devices from memory and the database.
func (s *Service) ClearAll(ctx context.Context) error {
	s.mu.Lock()
	s.devices = make(map[string]*Device)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM inventory_devices`)
	return err
}

func (s *Service) persist(dev *Device) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO inventory_devices (mac, manufacturer, rssi, last_ssid, first_seen, last_seen,
			hits, rssi_min, rssi_max, rssi_avg, last_node_id, last_latitude, last_longitude,
			channel, locally_administered, multicast)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (mac) DO UPDATE SET
			manufacturer = COALESCE(EXCLUDED.manufacturer, inventory_devices.manufacturer),
			rssi = EXCLUDED.rssi,
			last_ssid = COALESCE(EXCLUDED.last_ssid, inventory_devices.last_ssid),
			last_seen = EXCLUDED.last_seen,
			hits = EXCLUDED.hits,
			rssi_min = EXCLUDED.rssi_min,
			rssi_max = EXCLUDED.rssi_max,
			rssi_avg = EXCLUDED.rssi_avg,
			last_node_id = COALESCE(EXCLUDED.last_node_id, inventory_devices.last_node_id),
			last_latitude = COALESCE(EXCLUDED.last_latitude, inventory_devices.last_latitude),
			last_longitude = COALESCE(EXCLUDED.last_longitude, inventory_devices.last_longitude),
			channel = COALESCE(EXCLUDED.channel, inventory_devices.channel),
			locally_administered = EXCLUDED.locally_administered,
			multicast = EXCLUDED.multicast`,
		dev.MAC, dev.Manufacturer, dev.RSSI, dev.LastSSID, dev.FirstSeen, dev.LastSeen,
		dev.Hits, dev.MinRSSI, dev.MaxRSSI, dev.AvgRSSI,
		nilIfEmpty(dev.LastNodeID), nilIfZero(dev.LastLat), nilIfZero(dev.LastLon),
		nilIfZero(float64(dev.Channel)), dev.LocallyAdministered, dev.Multicast,
	)
	if err != nil {
		slog.Error("failed to persist inventory device", "mac", dev.MAC, "error", err)
	}
}

// Update modifies user-editable fields of an inventory device.
func (s *Service) Update(ctx context.Context, mac string, deviceName, deviceType, notes string, isKnown bool) error {
	s.mu.Lock()
	dev, exists := s.devices[mac]
	if exists {
		dev.DeviceName = deviceName
		dev.DeviceType = deviceType
		dev.Notes = notes
		dev.IsKnown = isKnown
	}
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `
		UPDATE inventory_devices SET device_name = $2, device_type = $3,
			notes = $4, is_known = $5
		WHERE mac = $1`,
		mac, deviceName, deviceType, notes, isKnown)
	return err
}

// nilIfEmpty returns nil for empty strings (for nullable UUID columns).
func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nilIfZero returns nil for zero float64 values (for nullable coordinate columns).
func nilIfZero(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

