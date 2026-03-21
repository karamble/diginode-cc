package tak

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// COTEvent represents a Cursor-on-Target event for TAK/ATAK.
type COTEvent struct {
	XMLName xml.Name `xml:"event"`
	Version string   `xml:"version,attr"`
	UID     string   `xml:"uid,attr"`
	Type    string   `xml:"type,attr"`
	How     string   `xml:"how,attr"`
	Time    string   `xml:"time,attr"`
	Start   string   `xml:"start,attr"`
	Stale   string   `xml:"stale,attr"`
	Point   COTPoint `xml:"point"`
	Detail  *COTDetail `xml:"detail,omitempty"`
}

// COTPoint represents a geographic point in COT.
type COTPoint struct {
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
	Hae float64 `xml:"hae,attr"`
	CE  float64 `xml:"ce,attr"`
	LE  float64 `xml:"le,attr"`
}

// COTDetail holds additional COT details.
type COTDetail struct {
	Contact *COTContact `xml:"contact,omitempty"`
	Track   *COTTrack   `xml:"track,omitempty"`
}

// COTContact holds callsign info.
type COTContact struct {
	Callsign string `xml:"callsign,attr"`
}

// COTTrack holds heading/speed info.
type COTTrack struct {
	Course float64 `xml:"course,attr"`
	Speed  float64 `xml:"speed,attr"`
}

// Service sends COT events to TAK servers.
type Service struct {
	addr   string
	conn   net.Conn
	stopCh chan struct{}
}

// NewService creates a new TAK service.
func NewService(addr string) *Service {
	return &Service{
		addr:   addr,
		stopCh: make(chan struct{}),
	}
}

// Start connects to the TAK server.
func (s *Service) Start() error {
	conn, err := net.DialTimeout("tcp", s.addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("TAK connection failed: %w", err)
	}
	s.conn = conn
	slog.Info("TAK connected", "addr", s.addr)
	return nil
}

// Stop disconnects from the TAK server.
func (s *Service) Stop() {
	close(s.stopCh)
	if s.conn != nil {
		s.conn.Close()
	}
}

// SendDrone sends a drone detection as a COT event.
func (s *Service) SendDrone(uid string, lat, lon, alt, heading, speed float64, callsign string) error {
	if s.conn == nil {
		return fmt.Errorf("TAK not connected")
	}

	now := time.Now().UTC()
	evt := COTEvent{
		Version: "2.0",
		UID:     uid,
		Type:    "a-f-A-M-F-Q", // COT type for UAS
		How:     "m-g",          // machine-generated
		Time:    now.Format(time.RFC3339),
		Start:   now.Format(time.RFC3339),
		Stale:   now.Add(30 * time.Second).Format(time.RFC3339),
		Point: COTPoint{
			Lat: lat,
			Lon: lon,
			Hae: alt,
			CE:  10,
			LE:  10,
		},
		Detail: &COTDetail{
			Contact: &COTContact{Callsign: callsign},
			Track:   &COTTrack{Course: heading, Speed: speed},
		},
	}

	data, err := xml.Marshal(evt)
	if err != nil {
		return err
	}

	_, err = s.conn.Write(data)
	return err
}

// SendNode sends a mesh node position as a COT event.
func (s *Service) SendNode(uid string, lat, lon, alt float64, callsign string) error {
	if s.conn == nil {
		return fmt.Errorf("TAK not connected")
	}

	now := time.Now().UTC()
	evt := COTEvent{
		Version: "2.0",
		UID:     uid,
		Type:    "a-f-G-U-C", // Friendly ground unit
		How:     "m-g",
		Time:    now.Format(time.RFC3339),
		Start:   now.Format(time.RFC3339),
		Stale:   now.Add(5 * time.Minute).Format(time.RFC3339),
		Point: COTPoint{
			Lat: lat,
			Lon: lon,
			Hae: alt,
			CE:  50,
			LE:  50,
		},
		Detail: &COTDetail{
			Contact: &COTContact{Callsign: callsign},
		},
	}

	data, err := xml.Marshal(evt)
	if err != nil {
		return err
	}

	_, err = s.conn.Write(data)
	return err
}
