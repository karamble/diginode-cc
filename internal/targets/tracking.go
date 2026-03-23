package targets

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Tracking constants matching CC PRO's target-tracking.service.ts
const (
	DetectionWindowMS       = 45_000   // 45 seconds sliding window
	PersistIntervalMS       = 15_000   // min 15s between persists
	PersistDistanceM        = 8.0      // min 8m movement to persist
	MinBootstrapConfidence  = 0.25     // first persist threshold
	MinConfidenceForPersist = 0.35     // subsequent persist threshold
	SingleNodeConfFloor     = 0.22     // single-node minimum
	MaxHistorySize          = 64       // max detections per MAC
)

// Detection is a single signal observation from a mesh node.
type Detection struct {
	NodeID    string
	Latitude  float64
	Longitude float64
	RSSI      int
	Weight    float64
	Timestamp time.Time
}

// TrackingState holds per-MAC tracking data.
type TrackingState struct {
	Detections   []Detection
	LastLat      float64
	LastLon      float64
	LastConf     float64
	LastPersist  time.Time
	Bootstrapped bool
}

// TrackingService maintains a sliding window of detections per MAC
// and computes weighted RSSI centroids with confidence scoring.
type TrackingService struct {
	targetsSvc *Service
	states     map[string]*TrackingState // keyed by MAC
	mu         sync.Mutex
}

// NewTrackingService creates a tracking service backed by the targets service.
func NewTrackingService(targetsSvc *Service) *TrackingService {
	return &TrackingService{
		targetsSvc: targetsSvc,
		states:     make(map[string]*TrackingState),
	}
}

// AddDetection adds a detection to the sliding window for a MAC and re-computes
// the position estimate. If the estimate meets persistence thresholds, it's
// applied to the target.
func (ts *TrackingService) AddDetection(ctx context.Context, mac, nodeID string, lat, lon float64, rssi int) {
	if lat == 0 && lon == 0 {
		return
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	state, ok := ts.states[mac]
	if !ok {
		state = &TrackingState{}
		ts.states[mac] = state
	}

	now := time.Now()
	weight := computeWeight(rssi, true)

	// Add detection
	state.Detections = append(state.Detections, Detection{
		NodeID:    nodeID,
		Latitude:  lat,
		Longitude: lon,
		RSSI:      rssi,
		Weight:    weight,
		Timestamp: now,
	})

	// Prune old detections (sliding window)
	cutoff := now.Add(-time.Duration(DetectionWindowMS) * time.Millisecond)
	fresh := state.Detections[:0]
	for _, d := range state.Detections {
		if d.Timestamp.After(cutoff) {
			fresh = append(fresh, d)
		}
	}
	// Cap history size
	if len(fresh) > MaxHistorySize {
		fresh = fresh[len(fresh)-MaxHistorySize:]
	}
	state.Detections = fresh

	if len(state.Detections) == 0 {
		return
	}

	// Compute weighted centroid
	var totalWeight float64
	var wLat, wLon float64
	uniqueNodes := make(map[string]bool)

	for _, d := range state.Detections {
		wLat += d.Latitude * d.Weight
		wLon += d.Longitude * d.Weight
		totalWeight += d.Weight
		uniqueNodes[d.NodeID] = true
	}

	if totalWeight == 0 {
		return
	}

	centLat := wLat / totalWeight
	centLon := wLon / totalWeight

	// Compute spread (max distance from centroid)
	var maxSpread float64
	for _, d := range state.Detections {
		dist := haversineM(centLat, centLon, d.Latitude, d.Longitude)
		if dist > maxSpread {
			maxSpread = dist
		}
	}

	// Confidence scoring (CC PRO formula)
	nodeCount := len(uniqueNodes)
	sampleCount := len(state.Detections)
	nodeFactor := math.Min(1, float64(nodeCount)/3.0)
	weightFactor := math.Min(1, totalWeight/(float64(nodeCount)*0.9+0.3))
	spreadFactor := 1.0 / (1.0 + maxSpread/120.0)
	confidence := clamp01(nodeFactor * weightFactor * spreadFactor)

	// Apply exponential smoothing if we have a previous estimate
	if state.Bootstrapped && state.LastConf > 0 {
		baseSmoothing := math.Min(0.85, math.Max(0.25, totalWeight/(float64(sampleCount)+0.35)))
		smoothing := math.Max(baseSmoothing, math.Min(0.9, state.LastConf+0.15))
		centLat = centLat*smoothing + state.LastLat*(1-smoothing)
		centLon = centLon*smoothing + state.LastLon*(1-smoothing)
	}

	uncertainty := maxSpread

	// Persistence check
	shouldPersist := false
	if !state.Bootstrapped {
		if confidence >= MinBootstrapConfidence {
			shouldPersist = true
			state.Bootstrapped = true
		}
	} else {
		timeSince := now.Sub(state.LastPersist).Milliseconds()
		distMoved := haversineM(state.LastLat, state.LastLon, centLat, centLon)

		minDist := PersistDistanceM
		minTime := int64(PersistIntervalMS)

		// Require higher thresholds for low confidence
		if confidence < MinConfidenceForPersist || (nodeCount == 1 && confidence < SingleNodeConfFloor) {
			minDist *= 2
			minTime *= 2
		}

		if distMoved >= minDist || timeSince >= minTime {
			shouldPersist = true
		}
	}

	state.LastLat = centLat
	state.LastLon = centLon
	state.LastConf = confidence

	if !shouldPersist {
		return
	}

	state.LastPersist = now

	slog.Debug("tracking persist",
		"mac", mac,
		"nodes", nodeCount,
		"samples", sampleCount,
		"confidence", confidence,
		"uncertainty", uncertainty,
		"lat", centLat,
		"lon", centLon,
	)

	// Apply to target (async to avoid holding lock)
	go func() {
		ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ts.targetsSvc.ApplyTrackingEstimate(ctx2, mac, centLat, centLon, confidence, uncertainty, "tracking"); err != nil {
			slog.Debug("tracking persist failed (no target for MAC)", "mac", mac)
		}
	}()
}

// computeWeight converts RSSI to a weight using CC PRO's power curve.
func computeWeight(rssi int, favorMeasurement bool) float64 {
	if rssi == 0 {
		if favorMeasurement {
			return 1.2
		}
		return 0.6
	}
	clamped := math.Max(-120, math.Min(-35, float64(rssi)))
	normalized := (clamped + 120) / 85.0 // 0..1
	base := math.Max(0.05, math.Pow(normalized, 2.2))
	if favorMeasurement {
		return base*1.6 + 0.2
	}
	return base + 0.1
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// haversineM returns distance in meters between two lat/lon points.
func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
