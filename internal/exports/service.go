package exports

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// Service handles data export in CSV and JSON formats.
type Service struct {
	db *database.DB
}

// NewService creates a new export service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// ExportDronesCSV writes drone data as CSV.
func (s *Service) ExportDronesCSV(ctx context.Context, w io.Writer) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT mac, serial_number, uas_id, manufacturer, model,
			latitude, longitude, altitude, status, source,
			first_seen, last_seen
		FROM drones ORDER BY last_seen DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	cw.Write([]string{
		"MAC", "Serial Number", "UAS ID", "Manufacturer", "Model",
		"Latitude", "Longitude", "Altitude", "Status", "Source",
		"First Seen", "Last Seen",
	})

	for rows.Next() {
		var mac, serial, uasID, mfg, model, status, source string
		var lat, lon, alt float64
		var firstSeen, lastSeen time.Time

		if err := rows.Scan(&mac, &serial, &uasID, &mfg, &model,
			&lat, &lon, &alt, &status, &source,
			&firstSeen, &lastSeen); err != nil {
			continue
		}

		cw.Write([]string{
			mac, serial, uasID, mfg, model,
			fmt.Sprintf("%.6f", lat), fmt.Sprintf("%.6f", lon), fmt.Sprintf("%.1f", alt),
			status, source,
			firstSeen.Format(time.RFC3339), lastSeen.Format(time.RFC3339),
		})
	}

	cw.Flush()
	return cw.Error()
}

// ExportNodesJSON writes node data as JSON.
func (s *Service) ExportNodesJSON(ctx context.Context, w io.Writer) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT node_num, node_id, long_name, short_name, hw_model, role,
			latitude, longitude, altitude, battery_level, voltage,
			is_online, last_heard
		FROM nodes ORDER BY last_heard DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type NodeExport struct {
		NodeNum      int64   `json:"nodeNum"`
		NodeID       string  `json:"nodeId"`
		LongName     string  `json:"longName"`
		ShortName    string  `json:"shortName"`
		HWModel      string  `json:"hwModel"`
		Role         string  `json:"role"`
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		Altitude     float64 `json:"altitude"`
		BatteryLevel int     `json:"batteryLevel"`
		Voltage      float64 `json:"voltage"`
		IsOnline     bool    `json:"isOnline"`
		LastHeard    string  `json:"lastHeard"`
	}

	var nodes []NodeExport
	for rows.Next() {
		var n NodeExport
		var lastHeard time.Time
		if err := rows.Scan(&n.NodeNum, &n.NodeID, &n.LongName, &n.ShortName,
			&n.HWModel, &n.Role, &n.Latitude, &n.Longitude, &n.Altitude,
			&n.BatteryLevel, &n.Voltage, &n.IsOnline, &lastHeard); err != nil {
			continue
		}
		n.LastHeard = lastHeard.Format(time.RFC3339)
		nodes = append(nodes, n)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(nodes)
}

// ExportAlertsCSV writes alert events as CSV.
func (s *Service) ExportAlertsCSV(ctx context.Context, w io.Writer) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT severity, title, message, acknowledged, created_at
		FROM alert_events ORDER BY created_at DESC
		LIMIT 10000`)
	if err != nil {
		return err
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	cw.Write([]string{"Severity", "Title", "Message", "Acknowledged", "Created At"})

	for rows.Next() {
		var severity, title, message string
		var acknowledged bool
		var createdAt time.Time

		if err := rows.Scan(&severity, &title, &message, &acknowledged, &createdAt); err != nil {
			continue
		}

		ack := "No"
		if acknowledged {
			ack = "Yes"
		}

		cw.Write([]string{severity, title, message, ack, createdAt.Format(time.RFC3339)})
	}

	cw.Flush()
	return cw.Error()
}
