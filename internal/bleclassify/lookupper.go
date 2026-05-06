// Package bleclassify forwards raw BLE advertisements received from Halberd
// sensors to a localhost lookupper service for identification, then persists
// the classified result.
//
// The lookupper is a localhost HTTP endpoint that runs the BLE classification
// cascade (manufacturer ID, SIG appearance, FindMy fingerprint, surveillance
// OUI, AirTag/Tile/SmartTag pattern match). Diginode-cc only knows the
// endpoint exists and how to call it — the cascade itself stays inside the
// upstream service. If the lookupper isn't reachable at startup, raw-BLE
// classification is disabled for the rest of the session and the BLERAW:
// wire frames are persisted with classification fields null.
package bleclassify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// lookupperURLOverride is consulted first; if empty the runtime URL is the
// localhost default. Tests use the override; operators have no env var or
// config switch — the binary contains no string identifying the upstream
// service. To rebind for a custom deployment, change the default below.
var lookupperURLOverride string

func lookupperURL() string {
	if lookupperURLOverride != "" {
		return lookupperURLOverride
	}
	return "http://localhost:8000/api/ble/lookupper"
}

const (
	// probeTimeout caps the one-shot existence check on startup. Any failure
	// (timeout, network error, non-200) marks the lookupper unavailable for
	// the lifetime of the process.
	probeTimeout = 2 * time.Second

	// classifyTimeout caps a single classification call. BLE detections arrive
	// at high cadence during scans; a stalled lookupper must not back-pressure
	// the serial dispatch path.
	classifyTimeout = 3 * time.Second
)

// ErrLookupperFailed is returned by Classify when the lookupper request fails
// for any reason (unreachable, timeout, non-200 status, malformed JSON). The
// caller logs a generic "BLE lookup failed" message and persists the raw
// advertisement bytes with classification fields null.
var ErrLookupperFailed = errors.New("ble lookup failed")

// Lookupper holds the in-memory available flag (set once at boot) and a
// dedicated HTTP client with classification-call timeouts.
type Lookupper struct {
	available bool
	client    *http.Client
}

// NewLookupper performs the one-shot existence check and returns a Lookupper
// reflecting whatever it found. Available() returns true only when the probe
// succeeded with HTTP 200. There are no retries — operators restart
// diginode-cc to pick up a lookupper that came up after boot.
func NewLookupper(ctx context.Context) *Lookupper {
	l := &Lookupper{
		client: &http.Client{Timeout: classifyTimeout},
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, lookupperURL(), nil)
	if err != nil {
		slog.Info("BLE lookup unavailable")
		return l
	}
	resp, err := l.client.Do(req)
	if err != nil {
		slog.Info("BLE lookup unavailable")
		return l
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		l.available = true
		slog.Info("BLE lookup ready")
	} else {
		slog.Info("BLE lookup unavailable")
	}
	return l
}

// Available reports whether the startup probe succeeded. Callers gate
// raw-BLE features on this so we don't queue forwards we can't service.
func (l *Lookupper) Available() bool {
	return l != nil && l.available
}

// classifyRequest mirrors the gotailme lookupper's request shape. Encoded as
// JSON in the POST body. raw_adv_b64 is the AD-structures payload (everything
// after the BLE Link Layer header) base64-encoded.
type classifyRequest struct {
	MAC          string `json:"mac"`
	RSSI         int    `json:"rssi"`
	Channel      int    `json:"channel"`
	RawAdvB64    string `json:"raw_adv_b64"`
	IsRandomAddr bool   `json:"is_random_addr,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
}

// ClassifyResult is the fields the caller persists. JSON tags match the
// upstream lookupper's response shape so the wire format is the contract.
type ClassifyResult struct {
	MAC             string            `json:"mac"`
	DetectionType   string            `json:"detection_type"`
	Manufacturer    string            `json:"manufacturer,omitempty"`
	ManufacturerID  uint16            `json:"manufacturer_id,omitempty"`
	Identifier      string            `json:"identifier,omitempty"`
	LocalName       string            `json:"local_name,omitempty"`
	Appearance      uint16            `json:"appearance,omitempty"`
	HasAppearance   bool              `json:"has_appearance"`
	ServiceUUIDs16  []uint16          `json:"service_uuids_16,omitempty"`
	ServiceUUIDs128 []string          `json:"service_uuids_128,omitempty"`
	TxPower         *int8             `json:"tx_power,omitempty"`
	IsRandomAddr    bool              `json:"is_random_addr"`
	IBeaconUUID     string            `json:"ibeacon_uuid,omitempty"`
	Extra           string            `json:"extra,omitempty"`
	ServiceData16   map[string]string `json:"service_data_16,omitempty"`
	SurveillanceHit *struct {
		Vendor     string   `json:"vendor"`
		Confidence string   `json:"confidence"`
		Signals    []string `json:"signals"`
	} `json:"surveillance_hit,omitempty"`
}

// Classify sends a single advertisement to the lookupper and returns the
// parsed classification. Returns ErrLookupperFailed on any failure mode
// (timeout, non-200, malformed JSON). The caller logs a generic message —
// no upstream-identifying detail leaks into operator-visible output.
func (l *Lookupper) Classify(ctx context.Context, nodeID, mac string, rssi, channel int, advBytes []byte, isRandomAddr bool) (*ClassifyResult, error) {
	if !l.Available() {
		return nil, ErrLookupperFailed
	}

	reqBody := classifyRequest{
		MAC:          mac,
		RSSI:         rssi,
		Channel:      channel,
		RawAdvB64:    base64.StdEncoding.EncodeToString(advBytes),
		IsRandomAddr: isRandomAddr,
		NodeID:       nodeID,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrLookupperFailed, err)
	}

	postCtx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, lookupperURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, ErrLookupperFailed
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, ErrLookupperFailed
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrLookupperFailed
	}

	var result ClassifyResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, ErrLookupperFailed
	}
	return &result, nil
}
