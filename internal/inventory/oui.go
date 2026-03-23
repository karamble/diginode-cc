package inventory

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// ouiDatabase holds the parsed IEEE OUI vendor database.
// Thread-safe for concurrent reads during lookup and writes during reload.
var (
	ouiMu    sync.RWMutex
	ouiDB    = make(map[string]string)
	ouiCount int
)

// LoadOUIFromFile parses the IEEE OUI CSV file and replaces the in-memory database.
// Expected CSV format: Registry,Assignment,Organization Name,Organization Address
// Assignment is a 6-char hex string (e.g. "286FB9") which gets normalized to "28:6F:B9".
func LoadOUIFromFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open OUI file: %w", err)
	}
	defer f.Close()

	return loadOUI(f)
}

func loadOUI(r io.Reader) (int, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Skip header
	if _, err := reader.Read(); err != nil {
		return 0, fmt.Errorf("read OUI header: %w", err)
	}

	newDB := make(map[string]string, 40000)
	count := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Skip malformed lines
			continue
		}
		if len(record) < 3 {
			continue
		}

		// Only process MA-L (24-bit OUI) entries
		registry := strings.TrimSpace(record[0])
		if registry != "MA-L" {
			continue
		}

		assignment := strings.TrimSpace(record[1])
		orgName := strings.TrimSpace(record[2])

		if len(assignment) != 6 || orgName == "" {
			continue
		}

		// Convert "286FB9" → "28:6F:B9"
		prefix := fmt.Sprintf("%s:%s:%s",
			strings.ToUpper(assignment[0:2]),
			strings.ToUpper(assignment[2:4]),
			strings.ToUpper(assignment[4:6]),
		)

		newDB[prefix] = orgName
		count++
	}

	if count == 0 {
		return 0, fmt.Errorf("no valid OUI entries found")
	}

	ouiMu.Lock()
	ouiDB = newDB
	ouiCount = count
	ouiMu.Unlock()

	slog.Info("loaded IEEE OUI database", "entries", count)
	return count, nil
}

// LookupOUI returns the manufacturer for a MAC prefix.
func LookupOUI(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	// OUI prefix is first 3 bytes (XX:XX:XX)
	prefix := strings.ToUpper(mac[:8])

	ouiMu.RLock()
	name := ouiDB[prefix]
	ouiMu.RUnlock()

	return name
}

// GetOUIDB returns a copy of the OUI vendor database.
func GetOUIDB() map[string]string {
	ouiMu.RLock()
	defer ouiMu.RUnlock()
	cp := make(map[string]string, len(ouiDB))
	for k, v := range ouiDB {
		cp[k] = v
	}
	return cp
}

// GetOUICount returns the number of entries in the OUI database.
func GetOUICount() int {
	ouiMu.RLock()
	defer ouiMu.RUnlock()
	return ouiCount
}

// IsLocallyAdministered returns true if the MAC address is locally administered.
// Bit 1 (second-least-significant bit) of the first byte indicates this.
func IsLocallyAdministered(mac string) bool {
	b, err := parseFirstByte(mac)
	if err != nil {
		return false
	}
	return b&0x02 != 0
}

// IsMulticast returns true if the MAC address is a multicast address.
// Bit 0 (least-significant bit) of the first byte indicates this.
func IsMulticast(mac string) bool {
	b, err := parseFirstByte(mac)
	if err != nil {
		return false
	}
	return b&0x01 != 0
}

func parseFirstByte(mac string) (byte, error) {
	if len(mac) < 2 {
		return 0, fmt.Errorf("mac too short")
	}
	// Parse first two hex chars
	var b byte
	for i := 0; i < 2; i++ {
		b <<= 4
		c := mac[i]
		switch {
		case c >= '0' && c <= '9':
			b |= c - '0'
		case c >= 'A' && c <= 'F':
			b |= c - 'A' + 10
		case c >= 'a' && c <= 'f':
			b |= c - 'a' + 10
		default:
			return 0, fmt.Errorf("invalid hex char: %c", c)
		}
	}
	return b, nil
}
