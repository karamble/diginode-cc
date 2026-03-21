package faa

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// RegistryEntry represents an FAA aircraft registration record.
type RegistryEntry struct {
	SerialNumber   string                 `json:"serialNumber"`
	Registration   string                 `json:"registration"`
	Manufacturer   string                 `json:"manufacturer"`
	Model          string                 `json:"model"`
	RegistrantName string                 `json:"registrantName"`
	RegistrantCity string                 `json:"registrantCity"`
	RegistrantState string               `json:"registrantState"`
	Data           map[string]interface{} `json:"data,omitempty"`
}

// Service manages FAA registry lookups.
type Service struct {
	db *database.DB
}

// NewService creates a new FAA registry service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// Lookup searches the FAA registry by serial number.
func (s *Service) Lookup(ctx context.Context, serialNumber string) (*RegistryEntry, error) {
	var entry RegistryEntry
	var dataJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT serial_number, registration, manufacturer, model,
			registrant_name, registrant_city, registrant_state, data
		FROM faa_registry WHERE serial_number = $1`, serialNumber,
	).Scan(&entry.SerialNumber, &entry.Registration, &entry.Manufacturer,
		&entry.Model, &entry.RegistrantName, &entry.RegistrantCity,
		&entry.RegistrantState, &dataJSON)
	if err != nil {
		return nil, err
	}

	if dataJSON != nil {
		json.Unmarshal(dataJSON, &entry.Data)
	}

	return &entry, nil
}

// ImportCSV imports FAA registry data from a CSV file.
func (s *Service) ImportCSV(ctx context.Context, r io.Reader) (int, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true

	// Skip header
	header, err := reader.Read()
	if err != nil {
		return 0, err
	}

	// Build column index
	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[col] = i
	}

	count := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		serial := getField(record, colIdx, "SERIAL NUMBER")
		if serial == "" {
			continue
		}

		reg := getField(record, colIdx, "N-NUMBER")
		mfg := getField(record, colIdx, "MFR MDL CODE")
		model := getField(record, colIdx, "MODEL")
		name := getField(record, colIdx, "NAME")
		city := getField(record, colIdx, "CITY")
		state := getField(record, colIdx, "STATE")

		_, err = s.db.Pool.Exec(ctx, `
			INSERT INTO faa_registry (serial_number, registration, manufacturer, model,
				registrant_name, registrant_city, registrant_state, imported_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (serial_number) DO UPDATE SET
				registration = EXCLUDED.registration,
				manufacturer = EXCLUDED.manufacturer,
				model = EXCLUDED.model,
				registrant_name = EXCLUDED.registrant_name,
				imported_at = EXCLUDED.imported_at`,
			serial, reg, mfg, model, name, city, state, time.Now(),
		)
		if err != nil {
			slog.Debug("FAA import row error", "serial", serial, "error", err)
			continue
		}
		count++
	}

	slog.Info("FAA registry imported", "records", count)
	return count, nil
}

func getField(record []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(record) {
		return record[i]
	}
	return ""
}
