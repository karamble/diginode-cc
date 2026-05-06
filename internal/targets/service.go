package targets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

var ErrTargetNotFound = errors.New("target not found")

// Target represents a tracked entity. WiFi/MAC/OUI/SSID targets keep the
// classic shape; BLE fingerprint targets additionally populate the BLE*
// fields and carry a non-empty BLEShortID ("T-B-####"). The two shapes
// share the same row so the targets page can render a mixed list with a
// discriminator badge.
type Target struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	TargetType            string    `json:"targetType,omitempty"`
	MAC                   string    `json:"mac,omitempty"`
	Latitude              float64   `json:"latitude,omitempty"`
	Longitude             float64   `json:"longitude,omitempty"`
	Status                string    `json:"status"`
	URL                   string    `json:"url,omitempty"`
	Tags                  []string  `json:"tags,omitempty"`
	Notes                 string    `json:"notes,omitempty"`
	CreatedBy             string    `json:"createdBy,omitempty"`
	FirstNodeID           string    `json:"firstNodeId,omitempty"`
	TrackingConfidence    *float64  `json:"trackingConfidence,omitempty"`
	TrackingUncertainty   *float64  `json:"trackingUncertainty,omitempty"`
	TriangulationMethod   string    `json:"triangulationMethod,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`

	// BLE fingerprint fields — populated only when this is a BLE target.
	// Non-empty BLEShortID is the discriminator: it's the firmware-bound
	// "T-B-####" identifier the operator (and the firmware) uses to refer
	// to this target without going through the rotating MAC.
	BLEShortID         string   `json:"bleShortId,omitempty"`
	BLEManufacturerID  *int     `json:"bleManufacturerId,omitempty"`
	BLEServiceUUIDs16  []int    `json:"bleServiceUuids16,omitempty"`
	BLEServiceUUIDs128 []string `json:"bleServiceUuids128,omitempty"`
	BLELocalNameGlob   string   `json:"bleLocalNameGlob,omitempty"`
	BLEAppearanceMin   *int     `json:"bleAppearanceMin,omitempty"`
	BLEAppearanceMax   *int     `json:"bleAppearanceMax,omitempty"`
	BLETxPowerMin      *int     `json:"bleTxPowerMin,omitempty"`
	BLETxPowerMax      *int     `json:"bleTxPowerMax,omitempty"`
	BLEMatchMode       string   `json:"bleMatchMode,omitempty"`

	// Last-hit position fallback. Populated from the most recent
	// target_hits row that has GPS data. Used by the TargetsPage
	// position column when triangulation hasn't produced a fix yet.
	LastHitLatitude  *float64 `json:"lastHitLatitude,omitempty"`
	LastHitLongitude *float64 `json:"lastHitLongitude,omitempty"`
}

// Hit is one observation of a target — a single Target: frame the
// firmware emitted. Hits accumulate independently of triangulation
// fixes and survive MAC rotation for BLE fingerprint targets because
// they're keyed by target_id, not MAC.
type Hit struct {
	ID             string    `json:"id"`
	TargetID       string    `json:"targetId"`
	TargetShortID  string    `json:"targetShortId,omitempty"`
	ObservedMAC    string    `json:"observedMac"`
	ObservedName   string    `json:"observedName,omitempty"`
	RSSI           *int16    `json:"rssi,omitempty"`
	Latitude       *float64  `json:"latitude,omitempty"`
	Longitude      *float64  `json:"longitude,omitempty"`
	NodeID         string    `json:"nodeId,omitempty"`
	RawFrame       string    `json:"rawFrame,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// BLEFingerprint is the operator's input shape for "Mark as target" on a
// BLE Detections row. Empty fields stay unset (treated as wildcards by the
// firmware matcher); MatchMode defaults to "ALL".
type BLEFingerprint struct {
	ManufacturerID  *int     `json:"manufacturerId,omitempty"`
	ServiceUUIDs16  []int    `json:"serviceUuids16,omitempty"`
	ServiceUUIDs128 []string `json:"serviceUuids128,omitempty"`
	LocalNameGlob   string   `json:"localNameGlob,omitempty"`
	AppearanceMin   *int     `json:"appearanceMin,omitempty"`
	AppearanceMax   *int     `json:"appearanceMax,omitempty"`
	TxPowerMin      *int     `json:"txPowerMin,omitempty"`
	TxPowerMax      *int     `json:"txPowerMax,omitempty"`
	MatchMode       string   `json:"matchMode,omitempty"`
}

// PositionFix represents a triangulated position.
type PositionFix struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	AccuracyM float64   `json:"accuracyM"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// Service manages target tracking and triangulation.
type Service struct {
	db      *database.DB
	hub     *ws.Hub
	targets map[string]*Target
	mu      sync.RWMutex
}

// NewService creates a new target tracking service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:      db,
		hub:     hub,
		targets: make(map[string]*Target),
	}
}

// Load populates the in-memory cache from the database on startup.
// The LATERAL join pulls each target's most recent geocoded hit so the
// targets page can render a fallback position without a second query.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT t.id, t.name, COALESCE(t.description,''), COALESCE(t.target_type,''), COALESCE(t.mac,''),
			COALESCE(t.latitude,0), COALESCE(t.longitude,0), COALESCE(t.status,'active'),
			COALESCE(t.url,''), t.tags, COALESCE(t.notes,''), COALESCE(t.created_by,''),
			COALESCE(t.first_node_id,''), t.tracking_confidence, t.tracking_uncertainty,
			COALESCE(t.triangulation_method,''),
			t.created_at, t.updated_at,
			COALESCE(t.ble_short_id, ''),
			t.ble_manufacturer_id, t.ble_service_uuids_16, t.ble_service_uuids_128,
			COALESCE(t.ble_local_name_glob, ''),
			t.ble_appearance_min, t.ble_appearance_max,
			t.ble_tx_power_min, t.ble_tx_power_max,
			COALESCE(t.ble_match_mode, 'ALL'),
			lh.latitude, lh.longitude
		FROM targets t
		LEFT JOIN LATERAL (
			SELECT latitude, longitude
			FROM target_hits
			WHERE target_id = t.id AND latitude IS NOT NULL AND longitude IS NOT NULL
			ORDER BY created_at DESC
			LIMIT 1
		) lh ON TRUE`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for rows.Next() {
		var t Target
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.TargetType, &t.MAC,
			&t.Latitude, &t.Longitude, &t.Status,
			&t.URL, &t.Tags, &t.Notes, &t.CreatedBy,
			&t.FirstNodeID, &t.TrackingConfidence, &t.TrackingUncertainty,
			&t.TriangulationMethod,
			&t.CreatedAt, &t.UpdatedAt,
			&t.BLEShortID,
			&t.BLEManufacturerID, &t.BLEServiceUUIDs16, &t.BLEServiceUUIDs128,
			&t.BLELocalNameGlob,
			&t.BLEAppearanceMin, &t.BLEAppearanceMax,
			&t.BLETxPowerMin, &t.BLETxPowerMax,
			&t.BLEMatchMode,
			&t.LastHitLatitude, &t.LastHitLongitude,
		); err != nil {
			slog.Warn("failed to scan target", "error", err)
			continue
		}
		s.targets[t.ID] = &t
		count++
	}

	slog.Info("loaded targets", "count", count)
	return nil
}

// GetAll returns all targets.
func (s *Service) GetAll() []*Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Target, 0, len(s.targets))
	for _, t := range s.targets {
		result = append(result, t)
	}
	return result
}

// Create adds a new target.
func (s *Service) Create(ctx context.Context, t *Target) error {
	if t.Status == "" {
		t.Status = "active"
	}
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO targets (name, description, target_type, mac, latitude, longitude, status,
			url, tags, notes, created_by, first_node_id, tracking_confidence, tracking_uncertainty, triangulation_method)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`,
		t.Name, t.Description, t.TargetType, t.MAC,
		t.Latitude, t.Longitude, t.Status,
		nilStr(t.URL), t.Tags, nilStr(t.Notes), nilStr(t.CreatedBy),
		nilStr(t.FirstNodeID), t.TrackingConfidence, t.TrackingUncertainty, nilStr(t.TriangulationMethod),
	).Scan(&t.ID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.targets[t.ID] = t
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: t,
	})

	return nil
}

// Delete removes a target.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM targets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.targets, id)
	s.mu.Unlock()
	return nil
}

// Update modifies an existing target. For BLE fingerprint rows
// (existing.BLEShortID is non-empty) the BLE fingerprint columns are also
// written so the targets-page edit form can tweak the manufacturer / UUIDs /
// glob / range without losing or having to round-trip the rest of the row.
// BLEShortID itself is never updated — the firmware ID stays bound to the
// row for its lifetime, allocated only by CreateBLETarget.
func (s *Service) Update(ctx context.Context, id string, t *Target) error {
	s.mu.RLock()
	existing, hasExisting := s.targets[id]
	isBLE := hasExisting && existing.BLEShortID != ""
	s.mu.RUnlock()

	if isBLE {
		matchMode := strings.ToUpper(strings.TrimSpace(t.BLEMatchMode))
		if matchMode != "ANY" {
			matchMode = "ALL"
		}
		_, err := s.db.Pool.Exec(ctx, `
			UPDATE targets SET name = $2, description = $3, target_type = $4,
				mac = $5, status = $6, url = $7, tags = $8, notes = $9,
				ble_manufacturer_id = $10, ble_service_uuids_16 = $11,
				ble_service_uuids_128 = $12, ble_local_name_glob = $13,
				ble_appearance_min = $14, ble_appearance_max = $15,
				ble_tx_power_min = $16, ble_tx_power_max = $17,
				ble_match_mode = $18,
				updated_at = NOW()
			WHERE id = $1`,
			id, t.Name, t.Description, t.TargetType, t.MAC, t.Status,
			nilStr(t.URL), t.Tags, nilStr(t.Notes),
			t.BLEManufacturerID, t.BLEServiceUUIDs16, t.BLEServiceUUIDs128,
			nilStr(t.BLELocalNameGlob),
			t.BLEAppearanceMin, t.BLEAppearanceMax,
			t.BLETxPowerMin, t.BLETxPowerMax, matchMode)
		if err != nil {
			return err
		}
		s.mu.Lock()
		if existing, ok := s.targets[id]; ok {
			existing.Name = t.Name
			existing.Description = t.Description
			existing.TargetType = t.TargetType
			existing.MAC = t.MAC
			existing.Status = t.Status
			existing.URL = t.URL
			existing.Tags = t.Tags
			existing.Notes = t.Notes
			existing.BLEManufacturerID = t.BLEManufacturerID
			existing.BLEServiceUUIDs16 = t.BLEServiceUUIDs16
			existing.BLEServiceUUIDs128 = t.BLEServiceUUIDs128
			existing.BLELocalNameGlob = t.BLELocalNameGlob
			existing.BLEAppearanceMin = t.BLEAppearanceMin
			existing.BLEAppearanceMax = t.BLEAppearanceMax
			existing.BLETxPowerMin = t.BLETxPowerMin
			existing.BLETxPowerMax = t.BLETxPowerMax
			existing.BLEMatchMode = matchMode
		}
		s.mu.Unlock()
		return nil
	}

	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET name = $2, description = $3, target_type = $4,
			mac = $5, status = $6, url = $7, tags = $8, notes = $9,
			updated_at = NOW()
		WHERE id = $1`,
		id, t.Name, t.Description, t.TargetType, t.MAC, t.Status,
		nilStr(t.URL), t.Tags, nilStr(t.Notes))
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing, ok := s.targets[id]; ok {
		existing.Name = t.Name
		existing.Description = t.Description
		existing.TargetType = t.TargetType
		existing.MAC = t.MAC
		existing.Status = t.Status
		existing.URL = t.URL
		existing.Tags = t.Tags
		existing.Notes = t.Notes
	}
	s.mu.Unlock()
	return nil
}

// ApplyTrackingEstimate updates a target's position and tracking confidence.
// Called by the tracking service when a new position estimate is computed.
func (s *Service) ApplyTrackingEstimate(ctx context.Context, mac string, lat, lon, confidence, uncertainty float64, method string) error {
	s.mu.Lock()
	// Find target by MAC
	var target *Target
	for _, t := range s.targets {
		if t.MAC == mac {
			target = t
			break
		}
	}

	if target == nil {
		s.mu.Unlock()
		return ErrTargetNotFound
	}

	target.Latitude = lat
	target.Longitude = lon
	target.TrackingConfidence = &confidence
	target.TrackingUncertainty = &uncertainty
	if method != "" {
		target.TriangulationMethod = method
	}
	if confidence > 0.5 {
		target.Status = "active"
	}
	target.UpdatedAt = time.Now()
	id := target.ID
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET latitude = $2, longitude = $3,
			tracking_confidence = $4, tracking_uncertainty = $5,
			triangulation_method = $6, status = $7, updated_at = NOW()
		WHERE id = $1`,
		id, lat, lon, confidence, uncertainty, nilStr(method), target.Status)
	if err != nil {
		return err
	}

	// Persist position history
	s.db.Pool.Exec(ctx, `
		INSERT INTO target_positions (target_id, latitude, longitude, accuracy_m, source)
		VALUES ($1, $2, $3, $4, $5)`,
		id, lat, lon, uncertainty, method)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: target,
	})

	return nil
}

// EnsureTargetExists creates a placeholder target for a MAC if one doesn't exist.
// Used during T_D processing when triangulation detects a new MAC.
func (s *Service) EnsureTargetExists(ctx context.Context, mac, nodeID string) *Target {
	s.mu.RLock()
	for _, t := range s.targets {
		if t.MAC == mac {
			s.mu.RUnlock()
			return t
		}
	}
	s.mu.RUnlock()

	// Create placeholder
	t := &Target{
		Name:        "Auto: " + mac,
		MAC:         mac,
		TargetType:  "wifi", // default; will be refined by detection data
		Status:      "triangulating",
		FirstNodeID: nodeID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.Create(ctx, t); err != nil {
		slog.Warn("failed to auto-create target", "mac", mac, "error", err)
		return nil
	}
	slog.Info("auto-created target for triangulation", "mac", mac, "id", t.ID)
	return t
}

// ClearAll removes all targets from memory and the database.
func (s *Service) ClearAll(ctx context.Context) error {
	s.mu.Lock()
	s.targets = make(map[string]*Target)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM targets`)
	return err
}

// Resolve marks a target as resolved.
func (s *Service) Resolve(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET status = 'resolved', updated_at = NOW()
		WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing, ok := s.targets[id]; ok {
		existing.Status = "resolved"
		existing.UpdatedAt = time.Now()
	}
	s.mu.Unlock()
	return nil
}

// Reactivate flips a resolved target back to active. Mirror of Resolve
// so the operator has an explicit one-click way to undo a Resolve
// without going through the full Edit / Save form. Idempotent — calling
// it on an already-active row is a no-op write.
func (s *Service) Reactivate(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE targets SET status = 'active', updated_at = NOW()
		WHERE id = $1`, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing, ok := s.targets[id]; ok {
		existing.Status = "active"
		existing.UpdatedAt = time.Now()
	}
	s.mu.Unlock()
	return nil
}

// UpdatePosition records a new position fix for a target.
func (s *Service) UpdatePosition(ctx context.Context, targetID string, fix *PositionFix) error {
	s.mu.Lock()
	t, exists := s.targets[targetID]
	if !exists {
		s.mu.Unlock()
		return ErrTargetNotFound
	}
	t.Latitude = fix.Latitude
	t.Longitude = fix.Longitude
	t.UpdatedAt = time.Now()
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO target_positions (target_id, latitude, longitude, accuracy_m, source)
		VALUES ($1, $2, $3, $4, $5)`,
		targetID, fix.Latitude, fix.Longitude, fix.AccuracyM, fix.Source,
	)
	if err != nil {
		slog.Error("failed to persist target position", "error", err)
		return err
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: t,
	})

	return nil
}

// Triangulate estimates a target position from multiple signal observations.
// Uses weighted centroid based on signal strength / distance estimates.
func Triangulate(observations []Observation) *PositionFix {
	if len(observations) < 2 {
		return nil
	}

	var totalWeight float64
	var weightedLat, weightedLng float64

	for _, obs := range observations {
		// Weight by inverse square of estimated distance
		dist := estimateDistance(obs.RSSI)
		if dist <= 0 {
			dist = 1
		}
		weight := 1.0 / (dist * dist)

		weightedLat += obs.Latitude * weight
		weightedLng += obs.Longitude * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return nil
	}

	return &PositionFix{
		Latitude:  weightedLat / totalWeight,
		Longitude: weightedLng / totalWeight,
		AccuracyM: estimateAccuracy(observations),
		Source:    "triangulation",
		Timestamp: time.Now(),
	}
}

// Observation is a single signal measurement from a known location.
type Observation struct {
	Latitude  float64
	Longitude float64
	RSSI      int
}

// estimateDistance estimates distance in meters from RSSI (free-space path loss).
func estimateDistance(rssi int) float64 {
	// Log-distance path loss model
	// RSSI = -10 * n * log10(d) + A
	// where n=2 (free space), A=-30 (reference at 1m)
	n := 2.0
	a := -30.0
	return math.Pow(10, (a-float64(rssi))/(10*n))
}

func estimateAccuracy(observations []Observation) float64 {
	if len(observations) < 2 {
		return 1000
	}
	// Rough accuracy estimate based on number of observations
	return math.Max(10, 500/float64(len(observations)))
}

func nilStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ---- BLE fingerprint targets --------------------------------------------

// CreateBLETarget allocates a new T-B-#### identifier from the postgres
// sequence and inserts a target row carrying the BLE fingerprint shape.
// MAC stays empty (the device will rotate); operators correlate hits to
// this row via the short ID emitted in the firmware's TID: field.
//
// name typically pre-fills from the BLE row's local_name + manufacturer
// in the dialog. fingerprint must have at least one field set or the
// firmware will treat it as a wildcard target (which would match every
// BLE advertisement and is almost always a configuration mistake).
func (s *Service) CreateBLETarget(ctx context.Context, name, description string, fingerprint *BLEFingerprint, createdBy string) (*Target, error) {
	if fingerprint == nil {
		return nil, errors.New("fingerprint required")
	}
	if !fingerprintHasField(fingerprint) {
		return nil, errors.New("fingerprint must set at least one field")
	}
	matchMode := fingerprint.MatchMode
	if matchMode == "" {
		matchMode = "ALL"
	}
	if matchMode != "ALL" && matchMode != "ANY" {
		return nil, errors.New("matchMode must be ALL or ANY")
	}

	// Allocate next T-B-#### from the sequence.
	var seqVal int64
	if err := s.db.Pool.QueryRow(ctx, `SELECT nextval('ble_target_short_id_seq')`).Scan(&seqVal); err != nil {
		return nil, err
	}
	shortID := fmt.Sprintf("T-B-%d", seqVal)

	t := &Target{
		Name:               name,
		Description:        description,
		TargetType:         "BLE_FINGERPRINT",
		Status:             "active",
		CreatedBy:          createdBy,
		BLEShortID:         shortID,
		BLEManufacturerID:  fingerprint.ManufacturerID,
		BLEServiceUUIDs16:  fingerprint.ServiceUUIDs16,
		BLEServiceUUIDs128: fingerprint.ServiceUUIDs128,
		BLELocalNameGlob:   fingerprint.LocalNameGlob,
		BLEAppearanceMin:   fingerprint.AppearanceMin,
		BLEAppearanceMax:   fingerprint.AppearanceMax,
		BLETxPowerMin:      fingerprint.TxPowerMin,
		BLETxPowerMax:      fingerprint.TxPowerMax,
		BLEMatchMode:       matchMode,
	}

	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO targets (
			name, description, target_type, status, created_by,
			ble_short_id, ble_manufacturer_id, ble_service_uuids_16,
			ble_service_uuids_128, ble_local_name_glob,
			ble_appearance_min, ble_appearance_max,
			ble_tx_power_min, ble_tx_power_max, ble_match_mode
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, created_at, updated_at`,
		t.Name, nilStr(t.Description), t.TargetType, t.Status, nilStr(t.CreatedBy),
		t.BLEShortID, t.BLEManufacturerID, t.BLEServiceUUIDs16,
		t.BLEServiceUUIDs128, nilStr(t.BLELocalNameGlob),
		t.BLEAppearanceMin, t.BLEAppearanceMax,
		t.BLETxPowerMin, t.BLETxPowerMax, t.BLEMatchMode,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.targets[t.ID] = t
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventTarget,
		Payload: t,
	})
	return t, nil
}

// ListBLETargets returns only rows where ble_short_id is set (i.e. BLE
// fingerprint targets). Used by the CommandsPage multi-select for
// CONFIG_TARGETS_BLE and by the dedicated "BLE only" filter on TargetsPage.
func (s *Service) ListBLETargets() []*Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Target, 0)
	for _, t := range s.targets {
		if t.BLEShortID != "" {
			out = append(out, t)
		}
	}
	return out
}

// FindByBLEShortID returns the target with the given T-B-#### identifier,
// used by the alert callback to look up a BLE target from a firmware-emitted
// TID. Returns nil if no matching target exists.
func (s *Service) FindByBLEShortID(shortID string) *Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.targets {
		if t.BLEShortID == shortID {
			return t
		}
	}
	return nil
}

// BuildConfigTargetsBLEWireFrame serialises the selected BLE targets into
// the newline-separated body of a CONFIG_TARGETS_BLE command. Each line is
// "T-B-####:key=val;key=val..." with only the fields the operator picked
// included (other fields treated as wildcards by the firmware matcher).
//
// Returns an empty string (not an error) if targetIDs is empty, so the
// caller can clear the firmware's BLE target list with an empty body.
func (s *Service) BuildConfigTargetsBLEWireFrame(targetIDs []string) (string, error) {
	if len(targetIDs) == 0 {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	for i, id := range targetIDs {
		t, ok := s.targets[id]
		if !ok || t.BLEShortID == "" {
			return "", fmt.Errorf("target %s not found or not a BLE target", id)
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t.BLEShortID)
		b.WriteString(":")
		first := true
		writeKV := func(k, v string) {
			if !first {
				b.WriteString(";")
			}
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(v)
			first = false
		}
		if t.BLEManufacturerID != nil {
			writeKV("mfr", fmt.Sprintf("%04x", *t.BLEManufacturerID))
		}
		if len(t.BLEServiceUUIDs16) > 0 {
			parts := make([]string, 0, len(t.BLEServiceUUIDs16))
			for _, u := range t.BLEServiceUUIDs16 {
				parts = append(parts, fmt.Sprintf("%04x", u))
			}
			writeKV("uuid", strings.Join(parts, ","))
		}
		if len(t.BLEServiceUUIDs128) > 0 {
			writeKV("uuid128", strings.Join(t.BLEServiceUUIDs128, ","))
		}
		if t.BLELocalNameGlob != "" {
			writeKV("name", t.BLELocalNameGlob)
		}
		if t.BLEAppearanceMin != nil {
			writeKV("appmin", fmt.Sprintf("%d", *t.BLEAppearanceMin))
		}
		if t.BLEAppearanceMax != nil {
			writeKV("appmax", fmt.Sprintf("%d", *t.BLEAppearanceMax))
		}
		if t.BLETxPowerMin != nil {
			writeKV("txmin", fmt.Sprintf("%d", *t.BLETxPowerMin))
		}
		if t.BLETxPowerMax != nil {
			writeKV("txmax", fmt.Sprintf("%d", *t.BLETxPowerMax))
		}
		if t.BLEMatchMode != "" && t.BLEMatchMode != "ALL" {
			writeKV("match", t.BLEMatchMode)
		}
	}
	return b.String(), nil
}

func fingerprintHasField(f *BLEFingerprint) bool {
	if f.ManufacturerID != nil {
		return true
	}
	if len(f.ServiceUUIDs16) > 0 || len(f.ServiceUUIDs128) > 0 {
		return true
	}
	if f.LocalNameGlob != "" {
		return true
	}
	if f.AppearanceMin != nil || f.AppearanceMax != nil {
		return true
	}
	if f.TxPowerMin != nil || f.TxPowerMax != nil {
		return true
	}
	return false
}

// FindByMAC returns the target whose canonical MAC matches macUpper.
// Caller must pass an upper-case "AA:BB:CC:DD:EE:FF" string (the cache
// normalises on insert). BLE fingerprint targets are skipped because
// their MAC field is empty by design — those resolve via FindByBLEShortID.
func (s *Service) FindByMAC(macUpper string) *Target {
	if macUpper == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.targets {
		if t.MAC != "" && strings.EqualFold(t.MAC, macUpper) {
			return t
		}
	}
	return nil
}

// RecordHit inserts one target_hits row and refreshes the cached
// LastHitLatitude / LastHitLongitude on the in-memory target so the
// TargetsPage position fallback updates without a reload. Best-effort:
// the caller already emitted the alert and inventory write before
// this fires, so a DB error here only loses the per-hit history row.
func (s *Service) RecordHit(ctx context.Context, h *Hit) error {
	if h == nil || h.TargetID == "" {
		return errors.New("targets.RecordHit: target id required")
	}
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO target_hits (
			target_id, target_short_id, observed_mac, observed_name,
			rssi, latitude, longitude, node_id, raw_frame
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		h.TargetID, nilStr(h.TargetShortID), h.ObservedMAC, nilStr(h.ObservedName),
		h.RSSI, h.Latitude, h.Longitude, nilStr(h.NodeID), nilStr(h.RawFrame))
	if err != nil {
		return err
	}

	if h.Latitude != nil && h.Longitude != nil {
		s.mu.Lock()
		if existing, ok := s.targets[h.TargetID]; ok {
			lat := *h.Latitude
			lon := *h.Longitude
			existing.LastHitLatitude = &lat
			existing.LastHitLongitude = &lon
		}
		s.mu.Unlock()
	}
	return nil
}

// ListHits returns the most recent hits for a target, newest first.
// limit defaults to 500 when 0; capped at 5000 to keep payloads bounded.
func (s *Service) ListHits(ctx context.Context, targetID string, limit int) ([]Hit, error) {
	if targetID == "" {
		return nil, errors.New("targets.ListHits: target id required")
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, target_id, COALESCE(target_short_id,''), observed_mac,
			COALESCE(observed_name,''), rssi, latitude, longitude,
			COALESCE(node_id,''), COALESCE(raw_frame,''), created_at
		FROM target_hits
		WHERE target_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Hit, 0, limit)
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.TargetID, &h.TargetShortID, &h.ObservedMAC,
			&h.ObservedName, &h.RSSI, &h.Latitude, &h.Longitude,
			&h.NodeID, &h.RawFrame, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
