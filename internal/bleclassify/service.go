package bleclassify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Service consumes BLERAW: events from the serial dispatcher, forwards the
// raw advertisement bytes to the localhost BLE lookupper for classification,
// persists the result to ble_detections, and broadcasts on the WS hub.
//
// The service is intentionally light: there is no buffering, no retry, and
// no rate limit. Halberd already dedups per (MAC, scan session) so the
// inbound rate is bounded; the lookupper's classifyTimeout is the only
// back-pressure mechanism. Persistence runs in the same goroutine as the
// callback because pgxpool already serialises by row and the volume is low.
// ClassifiedCallback fires once per HandleRaw call after the row is
// persisted, regardless of whether the lookupper produced a result. result
// is non-nil only when classification succeeded; consumers that want to
// surface the device but tolerate empty classification (e.g. the commands
// service correlating BLE detections to the originating scan) should still
// react when result is nil.
type ClassifiedCallback func(nodeID, mac string, rssi int, result *ClassifyResult)

type Service struct {
	db           *database.DB
	hub          *ws.Hub
	lookupper    *Lookupper
	onClassified ClassifiedCallback
}

// NewService builds the service. HandleRaw always attempts classification
// and persists the row regardless of whether the lookupper produced a
// result, so a row written with null classification fields stays
// recoverable later once the lookupper is reachable again.
// hub may be nil for tests; HandleRaw skips the broadcast in that case.
// db must not be nil at runtime; tests can pass nil to skip persistence.
func NewService(db *database.DB, hub *ws.Hub, lookupper *Lookupper) *Service {
	return &Service{db: db, hub: hub, lookupper: lookupper}
}

// SetClassifiedCallback registers a callback that fires once per HandleRaw
// invocation after persistence + WS broadcast complete. Call before the
// service starts processing detections; not safe to swap concurrently.
func (s *Service) SetClassifiedCallback(fn ClassifiedCallback) {
	s.onClassified = fn
}

// detectionPayload is the WS broadcast shape. It mirrors ClassifyResult plus
// the sensor's node ID and the local timestamp so a frontend client doesn't
// need to round-trip to the DB to render a live row.
type detectionPayload struct {
	NodeID    string         `json:"node_id"`
	Timestamp time.Time      `json:"timestamp"`
	Detection ClassifyResult `json:"detection"`
}

// HandleRaw is the callback registered with serial.Manager.SetBLERawCallback.
// Forwards the advertisement to the lookupper, persists the classified row
// (or a raw-bytes-only row when classification fails), and broadcasts on the
// WS hub. The signature matches serial.Manager.onBLERaw exactly.
func (s *Service) HandleRaw(nodeID, mac string, rssi, channel int, advBytes []byte) {
	if s == nil {
		return
	}

	now := time.Now()
	var result *ClassifyResult
	ctx, cancel := context.WithTimeout(context.Background(), classifyTimeout)
	// is_random_addr is not encoded in the BLERAW: wire frame today
	// (Halberd emits MAC + RSSI + CH + base64 only). Default false; the
	// lookupper classifier can infer randomization from the MAC's locally-
	// administered bit on its own.
	r, err := s.lookupper.Classify(ctx, nodeID, mac, rssi, channel, advBytes, false)
	cancel()
	if err != nil {
		if errors.Is(err, ErrLookupperFailed) {
			slog.Warn("BLE lookup failed", "node", nodeID, "mac", mac)
		}
	} else {
		result = r
	}

	s.persist(nodeID, mac, rssi, channel, advBytes, now, result)

	if s.hub != nil && result != nil {
		s.hub.Broadcast(ws.Event{
			Type: ws.EventBLEDetection,
			Payload: detectionPayload{
				NodeID:    nodeID,
				Timestamp: now,
				Detection: *result,
			},
		})
	}

	if s.onClassified != nil {
		s.onClassified(nodeID, mac, rssi, result)
	}
}

// persist writes the row. result is nil when the lookupper was unreachable
// or returned an error; in that case we still record the raw payload so a
// later replay (with a working lookupper) can fill in classification fields.
// site_id is left NULL in v1 — wiring site assignment for BLE rows can come
// later when a site router pattern emerges.
func (s *Service) persist(nodeID, mac string, rssi, channel int, advBytes []byte, ts time.Time, result *ClassifyResult) {
	if s.db == nil {
		return
	}

	var (
		detectionType  *string
		manufacturer   *string
		manufacturerID *int
		localName      *string
		appearance     *int
		serviceUUIDs16 []int
		serviceUUIDs128 []string
		txPower        *int
		isRandomAddr   bool
		classification []byte
		findmyScore    *int
		combinedScore  *float32
	)

	if result != nil {
		isRandomAddr = result.IsRandomAddr
		if result.DetectionType != "" {
			detectionType = &result.DetectionType
		}
		if result.Manufacturer != "" {
			manufacturer = &result.Manufacturer
		}
		if result.ManufacturerID != 0 {
			id := int(result.ManufacturerID)
			manufacturerID = &id
		}
		if result.LocalName != "" {
			localName = &result.LocalName
		}
		if result.HasAppearance {
			ap := int(result.Appearance)
			appearance = &ap
		}
		if len(result.ServiceUUIDs16) > 0 {
			serviceUUIDs16 = make([]int, 0, len(result.ServiceUUIDs16))
			for _, u := range result.ServiceUUIDs16 {
				serviceUUIDs16 = append(serviceUUIDs16, int(u))
			}
		}
		if len(result.ServiceUUIDs128) > 0 {
			serviceUUIDs128 = result.ServiceUUIDs128
		}
		if result.TxPower != nil {
			tx := int(*result.TxPower)
			txPower = &tx
		}
		if blob, err := json.Marshal(result); err == nil {
			classification = blob
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO ble_detections (
			mac, node_id, rssi, channel, timestamp,
			detection_type, manufacturer, manufacturer_id, local_name, appearance,
			service_uuids_16, service_uuids_128, tx_power,
			is_random_addr, raw_adv, classification, findmy_score, combined_score
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13,
			$14, $15, $16, $17, $18
		)`,
		mac, nodeID, rssi, channel, ts,
		detectionType, manufacturer, manufacturerID, localName, appearance,
		serviceUUIDs16, serviceUUIDs128, txPower,
		isRandomAddr, advBytes, classification, findmyScore, combinedScore,
	)
	if err != nil {
		slog.Error("ble detection persist failed", "mac", mac, "error", err)
	}
}
