package inventory

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Device represents a WiFi device in the inventory.
type Device struct {
	ID           string    `json:"id"`
	MAC          string    `json:"mac"`
	Manufacturer string    `json:"manufacturer,omitempty"`
	DeviceName   string    `json:"deviceName,omitempty"`
	DeviceType   string    `json:"deviceType,omitempty"`
	RSSI         int       `json:"rssi,omitempty"`
	LastSSID     string    `json:"lastSsid,omitempty"`
	FirstSeen    time.Time `json:"firstSeen"`
	LastSeen     time.Time `json:"lastSeen"`
	IsKnown      bool      `json:"isKnown"`
	Notes        string    `json:"notes,omitempty"`
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

// Track adds or updates a device in the inventory.
func (s *Service) Track(mac, manufacturer, ssid string, rssi int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dev, exists := s.devices[mac]
	if !exists {
		dev = &Device{
			MAC:       mac,
			FirstSeen: time.Now(),
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

// LookupOUI returns the manufacturer for a MAC prefix.
func LookupOUI(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	// OUI prefix is first 3 bytes (XX:XX:XX)
	prefix := mac[:8]
	if name, ok := ouiDB[prefix]; ok {
		return name
	}
	return ""
}

func (s *Service) persist(dev *Device) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO inventory_devices (mac, manufacturer, rssi, last_ssid, first_seen, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (mac) DO UPDATE SET
			manufacturer = COALESCE(EXCLUDED.manufacturer, inventory_devices.manufacturer),
			rssi = EXCLUDED.rssi,
			last_ssid = COALESCE(EXCLUDED.last_ssid, inventory_devices.last_ssid),
			last_seen = EXCLUDED.last_seen`,
		dev.MAC, dev.Manufacturer, dev.RSSI, dev.LastSSID, dev.FirstSeen, dev.LastSeen,
	)
	if err != nil {
		slog.Error("failed to persist inventory device", "mac", dev.MAC, "error", err)
	}
}

// Common OUI prefixes (top entries — full DB would be loaded from file)
var ouiDB = map[string]string{
	"00:17:88": "Philips Lighting",
	"AC:23:3F": "Shenzhen Minew",
	"DC:A6:32": "Raspberry Pi",
	"B8:27:EB": "Raspberry Pi",
	"E4:5F:01": "Raspberry Pi",
	"48:1C:B9": "DJI",
	"60:60:1F": "DJI",
	"34:D2:62": "DJI",
}
