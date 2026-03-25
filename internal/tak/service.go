package tak

import (
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"strings"
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

// Config holds TAK connection parameters.
type Config struct {
	Addr     string // host:port
	Protocol string // "tcp" or "udp"
	TLS      bool
	Username string
	Password string
}

// Service sends COT events to TAK servers.
type Service struct {
	cfg    Config
	conn   net.Conn
	stopCh chan struct{}
}

// NewService creates a new TAK service.
func NewService(cfg Config) *Service {
	return &Service{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start connects to the TAK server.
func (s *Service) Start() error {
	proto := strings.ToLower(s.cfg.Protocol)

	switch proto {
	case "udp":
		addr, err := net.ResolveUDPAddr("udp", s.cfg.Addr)
		if err != nil {
			return fmt.Errorf("TAK UDP resolve failed: %w", err)
		}
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			return fmt.Errorf("TAK UDP connection failed: %w", err)
		}
		s.conn = conn
		slog.Info("TAK connected (UDP)", "addr", s.cfg.Addr)

	case "tcp":
		if s.cfg.TLS {
			tlsCfg := &tls.Config{
				InsecureSkipVerify: true, // TAK servers often use self-signed certs
			}
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", s.cfg.Addr, tlsCfg)
			if err != nil {
				return fmt.Errorf("TAK TLS connection failed: %w", err)
			}
			s.conn = conn
			slog.Info("TAK connected (TCP+TLS)", "addr", s.cfg.Addr)
		} else {
			conn, err := net.DialTimeout("tcp", s.cfg.Addr, 10*time.Second)
			if err != nil {
				return fmt.Errorf("TAK TCP connection failed: %w", err)
			}
			s.conn = conn
			slog.Info("TAK connected (TCP)", "addr", s.cfg.Addr)
		}

	default:
		return fmt.Errorf("TAK unsupported protocol: %s", proto)
	}

	// Send auth if credentials provided
	if s.cfg.Username != "" {
		if err := s.sendAuth(); err != nil {
			s.conn.Close()
			s.conn = nil
			return fmt.Errorf("TAK authentication failed: %w", err)
		}
	}

	return nil
}

// sendAuth sends a COT auth event with credentials.
func (s *Service) sendAuth() error {
	// TAK auth is typically done via a COT event with auth details
	now := time.Now().UTC()
	authXML := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<auth><cot><username>%s</username><password>%s</password>`+
			`<uid>DigiNode-CC</uid><time>%s</time></cot></auth>`,
		s.cfg.Username, s.cfg.Password, now.Format(time.RFC3339))

	_, err := s.conn.Write([]byte(authXML))
	if err != nil {
		return err
	}
	slog.Info("TAK auth sent", "user", s.cfg.Username)
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
