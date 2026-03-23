package faa

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// RegistryEntry represents an FAA aircraft registration record.
type RegistryEntry struct {
	SerialNumber    string                 `json:"serialNumber"`
	Registration    string                 `json:"registration"`    // N-number
	Manufacturer    string                 `json:"manufacturer"`
	Model           string                 `json:"model"`
	RegistrantName  string                 `json:"registrantName"`
	RegistrantCity  string                 `json:"registrantCity"`
	RegistrantState string                 `json:"registrantState"`
	ModeSCodeHex    string                 `json:"modeSCodeHex,omitempty"`
	FccIdentifier   string                 `json:"fccIdentifier,omitempty"`
	Data            map[string]interface{} `json:"data,omitempty"`
}

const (
	faaOnlineBaseURL = "https://uasdoc.faa.gov"
	faaOnlineHome    = "/listdocs"
	faaOnlineAPI     = "/api/v1/serialNumbers"
)

// cacheEntry holds a cached FAA lookup result with timestamp.
type cacheEntry struct {
	entry       *RegistryEntry
	lastAttempt time.Time
}

// Service manages FAA registry lookups with offline DB + online API.
type Service struct {
	db    *database.DB
	cache map[string]*cacheEntry
	mu    sync.RWMutex

	// Online session (cookie-based, no API key)
	OnlineLookupEnabled bool
	OnlineCooldown      time.Duration
	httpClient          *http.Client
	cookie              string
	cookieFetchedAt     time.Time
	cookieTTL           time.Duration
	cookieMu            sync.Mutex
}

// NewService creates a new FAA registry service.
func NewService(db *database.DB) *Service {
	return &Service{
		db:                  db,
		cache:               make(map[string]*cacheEntry),
		OnlineLookupEnabled: true,
		OnlineCooldown:      10 * time.Minute,
		cookieTTL:           10 * time.Minute,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			// Don't follow redirects automatically — we need to capture cookies
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// nNumberRegex matches FAA N-number format: N followed by 1-5 alphanumeric chars.
var nNumberRegex = regexp.MustCompile(`^N[A-Z0-9]{1,5}$`)

// LookupMultiKey searches the FAA registry using multiple strategies:
// 1. Direct serial number match
// 2. Registration (N-number) match from droneId
// 3. Mode-S code extracted from MAC address
// 4. Online FAA API (uasdoc.faa.gov) if enabled
func (s *Service) LookupMultiKey(ctx context.Context, droneID, mac, serialNumber string) (*RegistryEntry, error) {
	// Build a cache key from all available identifiers
	cacheKey := strings.ToUpper(strings.Join([]string{droneID, mac, serialNumber}, "|"))

	// Check cache (with cooldown)
	s.mu.RLock()
	if cached, ok := s.cache[cacheKey]; ok {
		if time.Since(cached.lastAttempt) < s.OnlineCooldown {
			s.mu.RUnlock()
			if cached.entry == nil {
				return nil, nil // cached miss
			}
			return cached.entry, nil
		}
	}
	s.mu.RUnlock()

	// 1. Try serial number lookup (direct DB match)
	if serialNumber != "" {
		entry, err := s.Lookup(ctx, serialNumber)
		if err == nil && entry != nil {
			s.setCache(cacheKey, entry)
			return entry, nil
		}
	}

	// 2. Try droneId as serial number (RID UAS ID often IS the serial)
	if droneID != "" && droneID != serialNumber {
		entry, err := s.Lookup(ctx, droneID)
		if err == nil && entry != nil {
			s.setCache(cacheKey, entry)
			return entry, nil
		}
	}

	// 3. Try N-number match from droneId
	if droneID != "" {
		normalized := strings.ToUpper(strings.TrimSpace(droneID))
		if nNumberRegex.MatchString(normalized) {
			entry, err := s.lookupByRegistration(ctx, normalized)
			if err == nil && entry != nil {
				s.setCache(cacheKey, entry)
				return entry, nil
			}
		}
	}

	// 3. Try Mode-S code from MAC (last 6 hex chars)
	if mac != "" {
		modeS := extractModeSFromMAC(mac)
		if modeS != "" {
			entry, err := s.lookupByModeS(ctx, modeS)
			if err == nil && entry != nil {
				s.setCache(cacheKey, entry)
				return entry, nil
			}
		}
	}

	// 4. Try online FAA API
	if s.OnlineLookupEnabled {
		candidates := buildOnlineCandidates(droneID, serialNumber)
		for _, candidate := range candidates {
			entry, err := s.lookupOnline(ctx, candidate)
			if err == nil && entry != nil {
				s.setCache(cacheKey, entry)
				return entry, nil
			}
		}
	}

	// Cache the miss
	s.setCache(cacheKey, nil)
	return nil, nil
}

// Lookup searches the FAA registry by serial number (offline DB).
func (s *Service) Lookup(ctx context.Context, serialNumber string) (*RegistryEntry, error) {
	var entry RegistryEntry
	var dataJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT serial_number, registration, manufacturer, model,
			registrant_name, registrant_city, registrant_state,
			COALESCE(mode_s_code_hex, ''), COALESCE(fcc_identifier, ''), data
		FROM faa_registry WHERE serial_number = $1`, serialNumber,
	).Scan(&entry.SerialNumber, &entry.Registration, &entry.Manufacturer,
		&entry.Model, &entry.RegistrantName, &entry.RegistrantCity,
		&entry.RegistrantState, &entry.ModeSCodeHex, &entry.FccIdentifier, &dataJSON)
	if err != nil {
		return nil, err
	}

	if dataJSON != nil {
		json.Unmarshal(dataJSON, &entry.Data)
	}

	return &entry, nil
}

// lookupByRegistration searches by N-number (registration).
func (s *Service) lookupByRegistration(ctx context.Context, nNumber string) (*RegistryEntry, error) {
	var entry RegistryEntry
	var dataJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT serial_number, registration, manufacturer, model,
			registrant_name, registrant_city, registrant_state,
			COALESCE(mode_s_code_hex, ''), COALESCE(fcc_identifier, ''), data
		FROM faa_registry WHERE registration = $1`, nNumber,
	).Scan(&entry.SerialNumber, &entry.Registration, &entry.Manufacturer,
		&entry.Model, &entry.RegistrantName, &entry.RegistrantCity,
		&entry.RegistrantState, &entry.ModeSCodeHex, &entry.FccIdentifier, &dataJSON)
	if err != nil {
		return nil, err
	}

	if dataJSON != nil {
		json.Unmarshal(dataJSON, &entry.Data)
	}
	return &entry, nil
}

// lookupByModeS searches by Mode-S transponder code.
func (s *Service) lookupByModeS(ctx context.Context, modeS string) (*RegistryEntry, error) {
	var entry RegistryEntry
	var dataJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT serial_number, registration, manufacturer, model,
			registrant_name, registrant_city, registrant_state,
			COALESCE(mode_s_code_hex, ''), COALESCE(fcc_identifier, ''), data
		FROM faa_registry WHERE mode_s_code_hex = $1`, modeS,
	).Scan(&entry.SerialNumber, &entry.Registration, &entry.Manufacturer,
		&entry.Model, &entry.RegistrantName, &entry.RegistrantCity,
		&entry.RegistrantState, &entry.ModeSCodeHex, &entry.FccIdentifier, &dataJSON)
	if err != nil {
		return nil, err
	}

	if dataJSON != nil {
		json.Unmarshal(dataJSON, &entry.Data)
	}
	return &entry, nil
}

// ensureCookie fetches a session cookie from the FAA website if needed.
func (s *Service) ensureCookie(ctx context.Context, force bool) error {
	s.cookieMu.Lock()
	defer s.cookieMu.Unlock()

	if !force && s.cookie != "" && time.Since(s.cookieFetchedAt) < s.cookieTTL {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", faaOnlineBaseURL+faaOnlineHome, nil)
	if err != nil {
		return err
	}
	s.setOnlineHeaders(req)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("FAA cookie fetch: %w", err)
	}
	defer resp.Body.Close()

	var cookies []string
	for _, c := range resp.Cookies() {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	if len(cookies) == 0 {
		return fmt.Errorf("FAA cookie fetch: no cookies returned (status %d)", resp.StatusCode)
	}

	s.cookie = strings.Join(cookies, "; ")
	s.cookieFetchedAt = time.Now()
	slog.Debug("FAA session cookie refreshed", "cookies", len(cookies))
	return nil
}

// setOnlineHeaders applies browser-like headers matching CC PRO's approach.
func (s *Service) setOnlineHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:137.0) Gecko/20100101 Firefox/137.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", faaOnlineBaseURL+faaOnlineHome)
	req.Header.Set("Client", "external")
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
	}
}

// lookupOnline queries the FAA UAS document API using session cookies.
func (s *Service) lookupOnline(ctx context.Context, id string) (*RegistryEntry, error) {
	if id == "" {
		return nil, nil
	}

	maxAttempts := 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Get/refresh session cookie
		if err := s.ensureCookie(ctx, attempt > 0); err != nil {
			slog.Debug("FAA cookie error", "error", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		// Build URL with query params matching CC PRO
		u := fmt.Sprintf("%s%s?itemsPerPage=8&pageIndex=0&orderBy[0]=updatedAt&orderBy[1]=DESC&findBy=serialNumber&serialNumber=%s",
			faaOnlineBaseURL, faaOnlineAPI, id)

		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		s.setOnlineHeaders(req)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Debug("FAA online lookup network error", "id", id, "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 502 {
			slog.Debug("FAA online lookup 502, retrying", "attempt", attempt+1)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			slog.Debug("FAA online lookup non-200", "id", id, "status", resp.StatusCode)
			return nil, nil
		}

		// Parse response: { data: { items: [...] } }
		var payload struct {
			Data struct {
				Items          []map[string]interface{} `json:"items"`
				FormattedItems []map[string]interface{} `json:"formattedItems"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, nil
		}

		var record map[string]interface{}
		if len(payload.Data.Items) > 0 {
			record = payload.Data.Items[0]
		} else if len(payload.Data.FormattedItems) > 0 {
			record = payload.Data.FormattedItems[0]
		}

		if record == nil {
			return nil, nil
		}

		entry := mapOnlineResult(record, id)
		if entry != nil {
			slog.Info("FAA online lookup match", "id", id, "registrant", entry.RegistrantName)
		}
		return entry, nil
	}

	return nil, nil
}

// mapOnlineResult converts FAA API JSON response to RegistryEntry.
func mapOnlineResult(data map[string]interface{}, _ string) *RegistryEntry {
	entry := &RegistryEntry{}

	entry.Registration = extractStr(data, "nNumber", "registrationNumber")
	entry.RegistrantName = extractStr(data, "name", "operatorName", "ownerName", "registrantName")
	entry.Manufacturer = extractStr(data, "makeName", "manufacturer")
	entry.Model = extractStr(data, "modelName", "model")
	entry.SerialNumber = extractStr(data, "serialNumber")
	entry.FccIdentifier = extractStr(data, "fccIdentifier", "fccId")
	entry.ModeSCodeHex = extractStr(data, "modeSCodeHex", "modeS")
	entry.RegistrantCity = extractStr(data, "city")
	entry.RegistrantState = extractStr(data, "state")

	// If we got nothing meaningful, return nil
	if entry.Registration == "" && entry.RegistrantName == "" && entry.Manufacturer == "" {
		return nil
	}

	// Store full response as extra data
	entry.Data = data

	return entry
}

// extractStr tries multiple keys and returns the first non-empty string value.
func extractStr(data map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// extractModeSFromMAC extracts a Mode-S transponder code from a MAC address.
// Uses last 6 hex characters of the normalized MAC.
func extractModeSFromMAC(mac string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	normalized = strings.ReplaceAll(normalized, "-", "")
	if len(normalized) < 6 {
		return ""
	}
	return normalized[len(normalized)-6:]
}

// buildOnlineCandidates generates candidate IDs for online FAA lookup.
func buildOnlineCandidates(droneID, serialNumber string) []string {
	var candidates []string
	seen := make(map[string]bool)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			candidates = append(candidates, s)
		}
	}

	if serialNumber != "" {
		add(serialNumber)
	}
	if droneID != "" {
		add(droneID)
		// Try without common prefixes
		for _, prefix := range []string{"UAS-", "RID-", "RID"} {
			if strings.HasPrefix(strings.ToUpper(droneID), prefix) {
				add(droneID[len(prefix):])
			}
		}
	}

	return candidates
}

func (s *Service) setCache(key string, entry *RegistryEntry) {
	s.mu.Lock()
	s.cache[key] = &cacheEntry{entry: entry, lastAttempt: time.Now()}
	s.mu.Unlock()
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
		modeS := getField(record, colIdx, "MODE S CODE HEX")
		fcc := getField(record, colIdx, "FCCI")

		_, err = s.db.Pool.Exec(ctx, `
			INSERT INTO faa_registry (serial_number, registration, manufacturer, model,
				registrant_name, registrant_city, registrant_state,
				mode_s_code_hex, fcc_identifier, imported_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (serial_number) DO UPDATE SET
				registration = EXCLUDED.registration,
				manufacturer = EXCLUDED.manufacturer,
				model = EXCLUDED.model,
				registrant_name = EXCLUDED.registrant_name,
				mode_s_code_hex = EXCLUDED.mode_s_code_hex,
				fcc_identifier = EXCLUDED.fcc_identifier,
				imported_at = EXCLUDED.imported_at`,
			serial, reg, mfg, model, name, city, state, nilIfEmpty(modeS), nilIfEmpty(fcc), time.Now(),
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

// Status represents the current state of the FAA registry.
type Status struct {
	TotalRecords int        `json:"totalRecords"`
	LastImported *time.Time `json:"lastImported,omitempty"`
}

// GetStatus returns the FAA registry record count and last import time.
func (s *Service) GetStatus(ctx context.Context) (*Status, error) {
	var st Status
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*), MAX(imported_at)
		FROM faa_registry`).Scan(&st.TotalRecords, &st.LastImported)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func getField(record []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(record) {
		return strings.TrimSpace(record[i])
	}
	return ""
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
