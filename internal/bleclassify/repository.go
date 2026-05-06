package bleclassify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func b64encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// Detection is the row shape returned by the REST handlers. It mirrors the
// classifier output plus the persisted-row metadata (timestamp, node id,
// raw bytes). raw_adv is base64-encoded for JSON transport.
type Detection struct {
	ID             int64           `json:"id"`
	MAC            string          `json:"mac"`
	NodeID         string          `json:"node_id"`
	RSSI           int             `json:"rssi"`
	Channel        int             `json:"channel"`
	Timestamp      time.Time       `json:"timestamp"`
	DetectionType  string          `json:"detection_type,omitempty"`
	Manufacturer   string          `json:"manufacturer,omitempty"`
	ManufacturerID *int            `json:"manufacturer_id,omitempty"`
	LocalName      string          `json:"local_name,omitempty"`
	Appearance     *int            `json:"appearance,omitempty"`
	ServiceUUIDs16 []int           `json:"service_uuids_16,omitempty"`
	ServiceUUIDs128 []string       `json:"service_uuids_128,omitempty"`
	TxPower        *int            `json:"tx_power,omitempty"`
	IsRandomAddr   bool            `json:"is_random_addr"`
	RawAdv         string          `json:"raw_adv"` // base64
	Classification json.RawMessage `json:"classification,omitempty"`
	FindMyScore    *int            `json:"findmy_score,omitempty"`
	CombinedScore  *float32        `json:"combined_score,omitempty"`
}

// ListFilter narrows a List query. Zero values mean "no filter" for the
// corresponding column. Limit defaults to 100 when zero, hard-capped at
// 1000 to keep response sizes bounded.
type ListFilter struct {
	MAC           string
	NodeID        string
	DetectionType string
	Since         time.Time
	Limit         int
}

// List returns recent detections matching filter, newest first.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]*Detection, error) {
	if s == nil || s.db == nil {
		return []*Detection{}, nil
	}

	var (
		clauses []string
		args    []interface{}
	)
	add := func(clause string, val interface{}) {
		args = append(args, val)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if filter.MAC != "" {
		add("mac = $%d", strings.ToUpper(filter.MAC))
	}
	if filter.NodeID != "" {
		add("node_id = $%d", filter.NodeID)
	}
	if filter.DetectionType != "" {
		add("detection_type = $%d", filter.DetectionType)
	}
	if !filter.Since.IsZero() {
		add("timestamp >= $%d", filter.Since)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	q := `
		SELECT id, mac, node_id, rssi, channel, timestamp,
		       detection_type, manufacturer, manufacturer_id, local_name, appearance,
		       service_uuids_16, service_uuids_128, tx_power,
		       is_random_addr, raw_adv, classification, findmy_score, combined_score
		FROM ble_detections`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT %d", limit)

	rows, err := s.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Detection, 0, 32)
	for rows.Next() {
		d := &Detection{}
		var (
			rawAdv         []byte
			detectionType  *string
			manufacturer   *string
			manufacturerID *int
			localName      *string
			appearance     *int
			serviceUUIDs16 []int
			serviceUUIDs128 []string
			txPower        *int
			classification []byte
			findmyScore    *int
			combinedScore  *float32
		)
		if err := rows.Scan(
			&d.ID, &d.MAC, &d.NodeID, &d.RSSI, &d.Channel, &d.Timestamp,
			&detectionType, &manufacturer, &manufacturerID, &localName, &appearance,
			&serviceUUIDs16, &serviceUUIDs128, &txPower,
			&d.IsRandomAddr, &rawAdv, &classification, &findmyScore, &combinedScore,
		); err != nil {
			return nil, err
		}
		if detectionType != nil {
			d.DetectionType = *detectionType
		}
		if manufacturer != nil {
			d.Manufacturer = *manufacturer
		}
		d.ManufacturerID = manufacturerID
		if localName != nil {
			d.LocalName = *localName
		}
		d.Appearance = appearance
		d.ServiceUUIDs16 = serviceUUIDs16
		d.ServiceUUIDs128 = serviceUUIDs128
		d.TxPower = txPower
		d.RawAdv = b64encode(rawAdv)
		if len(classification) > 0 {
			d.Classification = classification
		}
		d.FindMyScore = findmyScore
		d.CombinedScore = combinedScore
		out = append(out, d)
	}
	return out, rows.Err()
}
