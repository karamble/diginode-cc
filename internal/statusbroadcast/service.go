// Package statusbroadcast emits periodic STATUS text-message heartbeats over
// the LoRa mesh so other C2 installs and AntiHunter sensors can see the
// diginode-cc node is alive, where it is, and how charged its battery is.
//
// The frame format matches the AntiHunter sensor's STATUS reply shape plus a
// trailing Batt:XX% field (sensors have no battery hardware, we do):
//
//	{shortName}: STATUS: Mode:C2 Scan:IDLE Hits:N Temp:TC Up:HH:MM:SS [GPS:lat,lon HDOP=h] Batt:P%
//
// GPS is included only when gpsBroadcastEnabled is true AND the local node's
// last-known position is fresh (<10 min old). Batt is fetched on-demand via a
// TELEMETRY_APP want_response query because the firmware floor for passive
// broadcasts is 30 min — too stale for a 5-15 min heartbeat.
package statusbroadcast

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/meshtastic"
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/serial"
)

// Service is the periodic STATUS broadcaster.
type Service struct {
	cfg        *config.AppConfig
	nodes      *nodes.Service
	serialMgr  *serial.Manager
	dispatcher *meshtastic.Dispatcher
	startTime  time.Time

	// Trigger channel fires an immediate broadcast (e.g. from the
	// /api/admin/status-broadcast/trigger endpoint). Buffered cap 1 — if a
	// trigger is already queued the second one is a no-op.
	triggerCh chan struct{}

	// Last battery reading cached as a fallback when RequestDeviceMetrics
	// times out (e.g. radio briefly offline). -1 means "never read".
	lastBatteryPct atomic.Int32
}

// NewService creates a new status broadcaster. Call Start(ctx) to begin.
func NewService(
	cfg *config.AppConfig,
	nodes *nodes.Service,
	serialMgr *serial.Manager,
	dispatcher *meshtastic.Dispatcher,
) *Service {
	s := &Service{
		cfg:        cfg,
		nodes:      nodes,
		serialMgr:  serialMgr,
		dispatcher: dispatcher,
		startTime:  time.Now(),
		triggerCh:  make(chan struct{}, 1),
	}
	s.lastBatteryPct.Store(-1)
	return s
}

// Trigger requests an immediate broadcast. Returns true if the request was
// queued, false if one was already pending.
func (s *Service) Trigger() bool {
	select {
	case s.triggerCh <- struct{}{}:
		return true
	default:
		return false
	}
}

// Start runs the broadcaster loop until the context is cancelled. Reads
// interval + enabled from AppConfig on each iteration so UI changes take
// effect on the next tick without a restart.
func (s *Service) Start(ctx context.Context) {
	slog.Info("starting status broadcaster")

	// Short initial delay so the radio has time to connect and emit its first
	// NodeInfo (which populates LocalNodeNum). A broadcast before that would
	// return early with "local node number unknown".
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.triggerCh:
			s.broadcast(ctx)
			timer.Reset(s.intervalFromConfig())
		case <-timer.C:
			if s.enabledFromConfig() {
				s.broadcast(ctx)
			}
			timer.Reset(s.intervalFromConfig())
		}
	}
}

func (s *Service) enabledFromConfig() bool {
	var enabled bool
	if err := s.cfg.GetTyped("statusBroadcastEnabled", &enabled); err != nil {
		// Default to enabled on any unmarshal error — the key should always
		// exist thanks to EnsureDefaults, so an error here is suspicious.
		return true
	}
	return enabled
}

func (s *Service) gpsEnabledFromConfig() bool {
	var enabled bool
	if err := s.cfg.GetTyped("gpsBroadcastEnabled", &enabled); err != nil {
		return true
	}
	return enabled
}

func (s *Service) intervalFromConfig() time.Duration {
	var secs int
	if err := s.cfg.GetTyped("statusBroadcastIntervalSecs", &secs); err != nil || secs <= 0 {
		secs = 600
	}
	// Safety clamps — the UI enforces the same range, but a hand-edited
	// app_config row shouldn't be able to spam the mesh or effectively
	// disable the heartbeat.
	if secs < 60 {
		secs = 60
	}
	if secs > 3600 {
		secs = 3600
	}
	return time.Duration(secs) * time.Second
}

// broadcast assembles one STATUS frame and sends it as a text broadcast.
// Failures are logged but don't stop the worker — the next tick tries again.
func (s *Service) broadcast(ctx context.Context) {
	localNum := s.nodes.GetLocalNodeNum()
	if localNum == 0 {
		slog.Debug("status broadcast skipped: local node number unknown")
		return
	}
	local := s.nodes.GetByNodeNum(localNum)
	if local == nil {
		slog.Debug("status broadcast skipped: local node not tracked")
		return
	}
	shortName := local.ShortName
	if shortName == "" {
		shortName = fmt.Sprintf("!%08x", localNum)
	}

	// Battery: on-demand query first, fall back to passive telemetry, then
	// to the cached last reading, then to "?".
	batteryStr := s.readBattery(ctx, local)

	// Pi CPU temperature as a coarse operator-visible health signal.
	tempStr := readCPUTemp()

	// Uptime HH:MM:SS.
	uptime := time.Since(s.startTime)
	upStr := formatUptime(uptime)

	// Hits = count of tracked mesh nodes — roughly "how many peers have we
	// heard from." Matches the AntiHunter 'Hits' column semantically: a
	// rough activity gauge rather than a precise metric.
	hitCount := len(s.nodes.GetAll())

	// Assemble the frame. Field order mirrors AntiHunter's STATUS reply
	// exactly through HDOP; Batt is appended at the end.
	var b strings.Builder
	fmt.Fprintf(&b, "%s: STATUS: Mode:C2 Scan:IDLE Hits:%d Temp:%sC Up:%s",
		shortName, hitCount, tempStr, upStr)

	includedGPS := false
	if s.gpsEnabledFromConfig() {
		if local.Latitude != 0 && local.Longitude != 0 {
			fmt.Fprintf(&b, " GPS:%.6f,%.6f", local.Latitude, local.Longitude)
			includedGPS = true
		}
	}

	fmt.Fprintf(&b, " Batt:%s", batteryStr)

	frame := b.String()
	if err := s.serialMgr.SendToRadio(serial.BuildTextMessage(serial.BroadcastAddr, frame)); err != nil {
		slog.Warn("status broadcast send failed", "error", err, "frame", frame)
		return
	}

	slog.Info("status broadcast sent",
		"len", len(frame),
		"gps", includedGPS,
		"batt", batteryStr,
		"hits", hitCount,
	)
}

// readBattery returns a formatted battery string (e.g. "87%" or "?").
// Tries the on-demand query first, then falls back to passive data.
func (s *Service) readBattery(ctx context.Context, local *nodes.Node) string {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if dm, err := s.dispatcher.RequestDeviceMetrics(queryCtx); err == nil && dm != nil && dm.BatteryLevel > 0 {
		pct := int32(dm.BatteryLevel)
		if pct > 100 {
			pct = 100
		}
		s.lastBatteryPct.Store(pct)
		return fmt.Sprintf("%d%%", pct)
	} else if err != nil {
		slog.Debug("on-demand battery query failed", "error", err)
	}

	// Fallback 1: last-known passive telemetry on the local node record.
	if local != nil && local.BatteryLevel > 0 {
		pct := int32(local.BatteryLevel)
		if pct > 100 {
			pct = 100
		}
		s.lastBatteryPct.Store(pct)
		return fmt.Sprintf("%d%%", pct)
	}

	// Fallback 2: our own cache from a previous successful query.
	if cached := s.lastBatteryPct.Load(); cached >= 0 {
		return fmt.Sprintf("%d%%", cached)
	}

	return "?"
}

// readCPUTemp reads /sys/class/thermal/thermal_zone0/temp (millidegrees C)
// and returns "42.1" style. Returns "?" on any failure.
func readCPUTemp() string {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return "?"
	}
	raw := strings.TrimSpace(string(data))
	milli, err := strconv.Atoi(raw)
	if err != nil {
		return "?"
	}
	return fmt.Sprintf("%.1f", float64(milli)/1000.0)
}

// formatUptime renders a duration as HH:MM:SS with zero-padding.
func formatUptime(d time.Duration) string {
	secs := int(d.Seconds())
	h := secs / 3600
	m := (secs / 60) % 60
	s := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

