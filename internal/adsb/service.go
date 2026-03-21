package adsb

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/ws"
)

// Aircraft represents a tracked ADS-B aircraft.
type Aircraft struct {
	Hex         string  `json:"hex"`
	Flight      string  `json:"flight,omitempty"`
	AltBaro     float64 `json:"alt_baro,omitempty"`
	AltGeom     float64 `json:"alt_geom,omitempty"`
	GS          float64 `json:"gs,omitempty"`
	Track       float64 `json:"track,omitempty"`
	BaroRate    float64 `json:"baro_rate,omitempty"`
	Squawk      string  `json:"squawk,omitempty"`
	Category    string  `json:"category,omitempty"`
	Lat         float64 `json:"lat,omitempty"`
	Lon         float64 `json:"lon,omitempty"`
	RSSI        float64 `json:"rssi,omitempty"`
	Emergency   string  `json:"emergency,omitempty"`
	NavAltitude float64 `json:"nav_altitude,omitempty"`
	NavHeading  float64 `json:"nav_heading,omitempty"`
	Seen        float64 `json:"seen,omitempty"`
	Messages    int     `json:"messages,omitempty"`
}

// Dump1090Response is the JSON format from dump1090-fa.
type Dump1090Response struct {
	Now      float64    `json:"now"`
	Messages int        `json:"messages"`
	Aircraft []Aircraft `json:"aircraft"`
}

// Service polls ADS-B data from dump1090.
type Service struct {
	hub      *ws.Hub
	url      string
	aircraft map[string]*Aircraft
	mu       sync.RWMutex
	stopCh   chan struct{}
	client   *http.Client
}

// NewService creates a new ADS-B polling service.
func NewService(hub *ws.Hub, url string) *Service {
	return &Service{
		hub:      hub,
		url:      url,
		aircraft: make(map[string]*Aircraft),
		stopCh:   make(chan struct{}),
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Start begins polling the ADS-B feed.
func (s *Service) Start(ctx context.Context) {
	slog.Info("starting ADS-B poller", "url", s.url)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.poll()
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop halts the ADS-B poller.
func (s *Service) Stop() {
	close(s.stopCh)
}

// ClearAircraft removes all tracked aircraft from memory.
func (s *Service) ClearAircraft() {
	s.mu.Lock()
	s.aircraft = make(map[string]*Aircraft)
	s.mu.Unlock()
}

// GetAircraft returns all currently tracked aircraft.
func (s *Service) GetAircraft() []*Aircraft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Aircraft, 0, len(s.aircraft))
	for _, a := range s.aircraft {
		result = append(result, a)
	}
	return result
}

func (s *Service) poll() {
	resp, err := s.client.Get(s.url)
	if err != nil {
		slog.Debug("ADS-B poll failed", "error", err)
		return
	}
	defer resp.Body.Close()

	var data Dump1090Response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		slog.Debug("ADS-B decode failed", "error", err)
		return
	}

	s.mu.Lock()
	// Clear stale entries
	s.aircraft = make(map[string]*Aircraft, len(data.Aircraft))
	for i := range data.Aircraft {
		a := &data.Aircraft[i]
		s.aircraft[a.Hex] = a
	}
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventADSB,
		Payload: data.Aircraft,
	})
}
