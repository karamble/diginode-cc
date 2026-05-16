// Package bleclassify forwards raw BLE advertisements received from Halberd
// sensors to a localhost lookupper service for identification, then persists
// the classified result.
//
// The lookupper is a localhost HTTP endpoint that runs the BLE classification
// cascade (manufacturer ID, SIG appearance, FindMy fingerprint, surveillance
// OUI, AirTag/Tile/SmartTag pattern match). Diginode-cc only knows the
// endpoint exists and how to call it — the cascade itself stays inside the
// upstream service. Each Classify call is independent: a per-call timeout
// bounds the cost when the lookupper is slow or unreachable, and any
// failure persists the BLERAW: wire frame with classification fields null.
package bleclassify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// classifyTimeout caps a single classification call. BLE detections arrive
// at high cadence during scans; a stalled lookupper must not back-pressure
// the serial dispatch path. This is the only guard against an unreachable
// or slow lookupper; there is no boot-time probe or cached availability
// flag, so a lookupper that comes online mid-session starts producing
// classifications on the very next BLE advertisement.
const classifyTimeout = 3 * time.Second

// ErrLookupperFailed is returned by Classify when the lookupper request fails
// for any reason (unreachable, timeout, non-200 status, malformed JSON). The
// caller logs a generic "BLE lookup failed" message and persists the raw
// advertisement bytes with classification fields null.
var ErrLookupperFailed = errors.New("ble lookup failed")

// Lookupper holds the HTTP client used for classification calls.
type Lookupper struct {
	client *http.Client
}

// NewLookupper returns a Lookupper ready to issue classification calls. There
// is no boot-time probe: every Classify call attempts the round-trip and is
// bounded by classifyTimeout. This is intentional so a lookupper that
// restarts, deploys, or arrives late doesn't require a diginode-cc restart
// to be picked up.
func NewLookupper() *Lookupper {
	return &Lookupper{
		client: &http.Client{Timeout: classifyTimeout},
	}
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
