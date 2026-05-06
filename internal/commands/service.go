package commands

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

// newCommandID returns a fresh UUID v4 in the canonical hyphenated form,
// matching the format used by handlers_commands.go for parent commands.
// The commands.id column is typed UUID in postgres so any non-UUID string
// (e.g. "<parent-id>-raw_ble_off") makes persistCommand fail with SQLSTATE
// 22P02 and the row never lands in the DB.
func newCommandID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CommandStatus represents the lifecycle state of a command.
type CommandStatus string

const (
	StatusPending CommandStatus = "PENDING"
	StatusSent    CommandStatus = "SENT"
	StatusAcked   CommandStatus = "ACKED"
	// StatusRunning is the mid-state for long-running commands (scans,
	// detections) after the firmware acknowledges with "<CMD>_ACK:STARTED"
	// but before the corresponding "<CMD>_DONE:" summary arrives. Keeping
	// such commands in s.pending lets the DONE-ACK synthesis close them out
	// with the summary in Result — otherwise the DONE frame has no pending
	// row to update and the scan summary is lost.
	StatusRunning CommandStatus = "RUNNING"
	StatusOK      CommandStatus = "OK" // CC PRO compat
	StatusFailed  CommandStatus = "FAILED"
	StatusError   CommandStatus = "ERROR" // CC PRO compat
	StatusTimeout CommandStatus = "TIMEOUT"
)

var (
	ErrCommandNotFound = errors.New("command not found")
	ErrRateLimited     = errors.New("rate limited: too many commands to this node")
)

// ScanDetection is one per-device row appended to a running scan command's
// in-memory detection list. The accumulator is finalised into ResultText
// when the scan transitions to terminal state (SCAN_DONE_ACK or STOP_ACK
// fan-out) so the CommandsPage modal can render the device list captured
// during the scan window.
//
// Source distinguishes how the device was observed:
//   - "DEVICE": streaming D:/DEVICE: detection (no raw advertisement)
//   - "BLERAW": classified through the lookupper from a B:/BLERAW: frame
//
// A single MAC can appear with both sources during the same scan (the
// firmware emits D: and B: separately when raw mode is on); RecordScanDetection
// dedups per (MAC, Source) so each pairing contributes at most one row.
type ScanDetection struct {
	MAC           string `json:"mac"`
	RSSI          int    `json:"rssi"`
	Band          string `json:"band,omitempty"`          // "W"/"B"/"U" (DEVICE only)
	Source        string `json:"source"`                  // "DEVICE" or "BLERAW"
	DetectionType string `json:"detectionType,omitempty"` // BLERAW only
	Manufacturer  string `json:"manufacturer,omitempty"`
	LocalName     string `json:"localName,omitempty"`
}

// Command represents a queued command to a mesh node.
type Command struct {
	ID          string                 `json:"id"`
	TargetNode  uint32                 `json:"targetNode"`
	CommandType string                 `json:"commandType"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	Status      CommandStatus          `json:"status"`
	SentAt      *time.Time             `json:"sentAt,omitempty"`
	AckedAt     *time.Time             `json:"ackedAt,omitempty"`
	FinishedAt  *time.Time             `json:"finishedAt,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	RetryCount  int                    `json:"retryCount"`
	MaxRetries  int                    `json:"maxRetries"`
	CreatedAt   time.Time              `json:"createdAt"`

	// Structured command fields (CC PRO parity)
	Target string   `json:"target,omitempty"` // @ALL, @NODE_22, etc.
	Name   string   `json:"name,omitempty"`   // STATUS, SCAN_START, etc.
	Params []string `json:"params,omitempty"` // command parameters
	Line   string   `json:"line,omitempty"`   // formatted mesh text line

	// ACK enrichment
	AckKind    string `json:"ackKind,omitempty"`   // e.g. SCAN_ACK
	AckStatus  string `json:"ackStatus,omitempty"` // e.g. COMPLETE, ERROR
	AckNode    string `json:"ackNode,omitempty"`   // node that sent ACK
	ResultText string `json:"resultText,omitempty"`
	ErrorText  string `json:"errorText,omitempty"`

	// autoRawAttached flags a DEVICE_SCAN_START where the service auto-
	// enqueued a RAW_BLE_ON because the BLE lookupper was available. When
	// the command later transitions to terminal state (STOP_ACK,
	// SCAN_DONE_ACK, or scan-timeout reaper), the service auto-fires
	// RAW_BLE_OFF to the same target. In-memory only — never persisted.
	autoRawAttached bool

	// scanRows accumulates per-device detections observed while a scan-class
	// command is in StatusRunning. Finalised into ResultText on terminal
	// state transition. In-memory only — never persisted as a column.
	scanRows []ScanDetection
}

// Service manages the command queue with rate limiting and ACK tracking.
type Service struct {
	db       *database.DB
	hub      *ws.Hub
	pending  map[string]*Command
	nodeRate map[uint32]time.Time // last command time per node
	mu       sync.Mutex
	sendFn   func(nodeNum uint32, cmdType string, payload []byte) error
	// rawBLEAvailable gates the auto-attach of RAW_BLE_ON/OFF around
	// DEVICE_SCAN_START. Set once at boot from the lookupper's
	// startup-probe result. Never flipped at runtime.
	rawBLEAvailable bool
}

// NewService creates a new command queue service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:       db,
		hub:      hub,
		pending:  make(map[string]*Command),
		nodeRate: make(map[uint32]time.Time),
	}
}

// SetRawBLEAvailable records whether the BLE lookupper was reachable at
// startup. When true, Enqueue auto-attaches a RAW_BLE_ON to every accepted
// DEVICE_SCAN_START targeting an antihunter-class node, and fires
// RAW_BLE_OFF when the scan terminates.
func (s *Service) SetRawBLEAvailable(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rawBLEAvailable = v
}

// SetSendFunc sets the function used to actually transmit commands via serial.
func (s *Service) SetSendFunc(fn func(nodeNum uint32, cmdType string, payload []byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendFn = fn
}

// Enqueue adds a new command to the queue.
func (s *Service) Enqueue(cmd *Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rate limit: 1 command per node per 2 seconds
	if lastSent, ok := s.nodeRate[cmd.TargetNode]; ok {
		if time.Since(lastSent) < 2*time.Second {
			return ErrRateLimited
		}
	}

	cmd.Status = StatusPending
	cmd.CreatedAt = time.Now()
	if cmd.MaxRetries == 0 {
		cmd.MaxRetries = 3
	}

	s.pending[cmd.ID] = cmd

	// Auto-attach for DEVICE_SCAN_START when the BLE lookupper is available.
	// Critical ordering: RAW_BLE_ON must be sent BEFORE the scan start so the
	// firmware has rawBleMode=true on the very first BLE callback. If we sent
	// the parent first, the firmware would be 3-7 s into the scan before
	// rawBleMode flipped, and any BLE devices detected in that pre-RAW window
	// would miss bleRawCache population on first sight. We send RAW_BLE_ON
	// immediately and defer the parent by autoAttachDelay so they don't
	// collide on the Pi-side Heltec SerialModule's UART input window.
	if s.rawBLEAvailable && cmd.Name == "DEVICE_SCAN_START" {
		cmd.autoRawAttached = true
		s.enqueuePrecedingRawBLEOnLocked(cmd)
		return nil
	}

	go s.send(cmd)
	return nil
}

// autoAttachDelay is the gap inserted between the auto-attached RAW_BLE_*
// follow-on and the parent scan command (or vice versa) on the /dev/lora
// wire. Without this delay both writes hit the Pi-side Heltec's Meshtastic
// SerialModule UART input window simultaneously, the SerialModule batches
// them into one TEXTMSG, and one of the two commands gets corrupted (the
// loser of the race silently drops, leaving its row stuck at SENT forever).
// 3000ms matches the firmware's MESH_TX_PACING_MS and the SerialRateLimiter
// REFILL_INTERVAL so the magnitude is consistent across the system.
const autoAttachDelay = 3 * time.Second

// enqueuePrecedingRawBLEOnLocked queues a RAW_BLE_ON to fire FIRST, then the
// parent DEVICE_SCAN_START to fire after autoAttachDelay. The order matters:
// the firmware must have rawBleMode=true on the very first BLE callback of
// the new scan so bleRawCache populates from the start. Sending the parent
// first leaves the first 3-7 seconds of the scan with rawBleMode=false, and
// devices detected in that window won't get a B: frame later (the BLE
// callback's first-sight check excludes re-inserts for already-seen MACs).
//
// Caller must hold s.mu.
func (s *Service) enqueuePrecedingRawBLEOnLocked(parent *Command) {
	if parent == nil {
		return
	}
	rawOn := &Command{
		ID:          newCommandID(),
		TargetNode:  parent.TargetNode,
		CommandType: "RAW_BLE_ON",
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		MaxRetries:  3,
		Target:      parent.Target,
		Name:        "RAW_BLE_ON",
		Line:        parent.Target + " RAW_BLE_ON",
	}
	s.pending[rawOn.ID] = rawOn
	// RAW_BLE_ON goes immediately so the firmware's NVS rawBleMode flips to
	// true before the scan begins. Parent DEVICE_SCAN_START is deferred by
	// autoAttachDelay so the two writes land in separate Heltec SerialModule
	// input windows and the firmware's command parser sees them as distinct
	// LoRa TEXTMSGs.
	go s.send(rawOn)
	go func() {
		time.Sleep(autoAttachDelay)
		s.send(parent)
	}()
}

// enqueueRawBLEFollowonLocked appends a RAW_BLE_OFF command (the close pair
// for an auto-attached RAW_BLE_ON) targeting the same node as the parent.
// Used on SCAN_DONE_ACK and STOP_ACK fan-out — by then the firmware has
// finished emitting all D: + B: frames so timing relative to the ACK matters
// less than for the scan-start case. Bypasses the rate limiter because the
// follow-on pairs with a freshly-completed scan, not a separately-issued
// operator action.
//
// Caller must hold s.mu. The actual on-wire send is deferred by
// autoAttachDelay so the closing ACK frame from the firmware and the
// follow-on RAW_BLE_OFF write don't collide on the Heltec SerialModule's
// input window in the opposite direction.
func (s *Service) enqueueRawBLEFollowonLocked(parent *Command, name string) {
	if parent == nil {
		return
	}
	follow := &Command{
		ID:          newCommandID(),
		TargetNode:  parent.TargetNode,
		CommandType: name,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		MaxRetries:  3,
		Target:      parent.Target,
		Name:        name,
		Line:        parent.Target + " " + name,
	}
	s.pending[follow.ID] = follow
	go func() {
		time.Sleep(autoAttachDelay)
		s.send(follow)
	}()
}

// HandleACK processes an acknowledgment for a pending command.
func (s *Service) HandleACK(cmdID string, result map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd, exists := s.pending[cmdID]
	if !exists {
		return
	}

	now := time.Now()
	cmd.Status = StatusOK
	cmd.AckedAt = &now
	cmd.FinishedAt = &now
	cmd.Result = result
	delete(s.pending, cmdID)

	slog.Info("command acknowledged", "id", cmdID, "targetNode", cmd.TargetNode)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventCommand,
		Payload: cmd,
	})

	go s.persistCommand(cmd)
}

// HandleStructuredACK processes an ACK from a sensor node, matching by ACK type and target.
func (s *Service) HandleStructuredACK(ackKind, ackStatus, ackNode string, result map[string]interface{}) {
	cmdName, ok := ACKMap[ackKind]
	if !ok {
		slog.Debug("unknown ACK type", "ackKind", ackKind)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Some ACK frames cover multiple commands:
	//   - CONFIG_ACK is generic — firmware emits it for every CONFIG_*
	//     command with the specific kind embedded in the status field.
	//   - HB_ACK fires for both HB_ON and HB_OFF (and HB_INTERVAL).
	//   - SCAN_DONE_ACK is emitted by the firmware for both SCAN_START
	//     and DEVICE_SCAN_START completions (the firmware has no
	//     dedicated DEVICE_SCAN_DONE frame).
	//   - CODE_ACK covers CODE_ADD / CODE_REMOVE / CODE_CLEAR.
	// For all other ACK types the name must equal exactly.
	wantPrefix := ""
	wantSuffix := ""
	switch ackKind {
	case "CONFIG_ACK":
		wantPrefix = "CONFIG_"
	case "HB_ACK":
		wantPrefix = "HB_"
	case "CODE_ACK":
		wantPrefix = "CODE_"
	case "PROBE_ACK":
		// PROBE_ACK covers PROBE_START (STARTED) and PROBE_STOP (STOPPED).
		// The latest-pending tiebreak naturally directs STARTED to the just-
		// sent PROBE_START and STOPPED to the just-sent PROBE_STOP. The
		// follow-up fan-out below also closes any still-RUNNING PROBE_START
		// when a STOPPED ack lands.
		wantPrefix = "PROBE_"
	case "RAW_BLE_ACK":
		// Firmware replies HB55: RAW_BLE_ACK:ON|OFF for both RAW_BLE_ON and
		// RAW_BLE_OFF. The latest-pending tiebreak picks the freshly-sent
		// row for whichever toggle the operator just issued.
		wantPrefix = "RAW_BLE_"
	case "SCAN_DONE_ACK":
		wantSuffix = "SCAN_START" // matches SCAN_START + DEVICE_SCAN_START
	case "STOP_ACK":
		// SCAN_STOP / DEVICE_SCAN_STOP wire-emit STOP via WireName, so all
		// three commands end up triggering a STOP_ACK from the firmware.
		// Match by suffix so any of the three pending rows can close —
		// PROBE_STOP also has-suffix STOP but is closed via PROBE_ACK:STOPPED
		// and never produces a STOP_ACK from this firmware in practice.
		wantSuffix = "STOP"
	}

	// Find the latest PENDING/SENT/RUNNING command matching name + target
	var match *Command
	var matchKey string
	for id, cmd := range s.pending {
		name := cmd.Name
		if name == "" {
			name = cmd.CommandType
		}
		nameMatches := name == cmdName
		if wantPrefix != "" && strings.HasPrefix(name, wantPrefix) {
			nameMatches = true
		}
		if wantSuffix != "" && strings.HasSuffix(name, wantSuffix) {
			nameMatches = true
		}
		if !nameMatches {
			continue
		}
		if cmd.Status != StatusPending && cmd.Status != StatusSent && cmd.Status != StatusRunning {
			continue
		}
		if match == nil || cmd.CreatedAt.After(match.CreatedAt) {
			match = cmd
			matchKey = id
		}
	}

	if match == nil {
		slog.Debug("no pending command for ACK", "ackKind", ackKind, "cmdName", cmdName)
		return
	}

	now := time.Now()
	match.AckKind = ackKind
	match.AckStatus = ackStatus
	match.AckNode = ackNode

	// Derive final status from ACK status. The firmware uses a wider set of
	// tokens than CC PRO's original switch assumed:
	//   - "STARTED"   (SCAN/DEVICE_SCAN/DRONE/DEAUTH/RANDOMIZATION/BASELINE/
	//                  BATTERY_SAVER_ACK) — long-runner entering its scan
	//                  loop. Mid-state: keep in pending so the matching
	//                  *_DONE frame can close it with the scan summary.
	//   - "ENABLED"/"DISABLED"/"INTERVAL Nmin" (HB_ACK, AUTOERASE_ACK)
	//                  "CANCELLED" (ERASE_ACK), "" (TRI_START_ACK) —
	//                  single-shot toggle/config commands; treat as
	//                  terminal OK.
	//   - "UPDATED"/"EXISTS" (gate-sensor CODE_ACK on CODE_ADD where the
	//                  target code was already registered — UPDATED=name
	//                  changed, EXISTS=no-op idempotent success). Both are
	//                  terminal success: the requested end state is reached.
	//   - "INVALID_*" (CONFIG_ACK:NODE_ID/RSSI on bad params) — terminal
	//                  error; token stays in ErrorText for display.
	//   - "FULL"      (gate-sensor CODE_ACK when all 16 slots are used) —
	//                  capacity-exhausted terminal error.
	//   - "NOT_FOUND" (gate-sensor CODE_ACK on CODE_REMOVE for an absent
	//                  code) — operation can't proceed, terminal error.
	upper := strings.ToUpper(ackStatus)
	isRunning := upper == "STARTED"
	// TARGET_INTERVAL_ACK echoes the applied seconds value as the payload
	// (e.g. "TARGET_INTERVAL_ACK:120"). Any non-INVALID numeric token
	// confirms the firmware accepted the interval, so we treat it as
	// terminal-OK on the TARGET_INTERVAL command. Without this carve-out
	// the seconds value falls through to the default branch and the
	// command gets stuck at StatusSent forever — same shape as the
	// CONFIG_ACK:TARGETS_BLE:OK lifecycle bug fixed earlier.
	isTargetIntervalOK := ackKind == "TARGET_INTERVAL_ACK" &&
		!strings.HasPrefix(upper, "INVALID") && upper != ""
	isTerminalOK := !isRunning && (upper == "" ||
		upper == "OK" || upper == "COMPLETE" || upper == "COMPLETED" ||
		upper == "FINISHED" || upper == "SUCCESS" ||
		upper == "STOPPED" ||
		upper == "ENABLED" || upper == "DISABLED" ||
		// RAW_BLE_ACK uses "ON"/"OFF" tokens to confirm the requested
		// raw-advertisement-forwarding state was applied. The auto-
		// attached RAW_BLE_ON / RAW_BLE_OFF rows that pair with every
		// DEVICE_SCAN_START close on these tokens; without them the
		// follow-on commands would stay stuck at StatusSent forever even
		// though the firmware did acknowledge.
		upper == "ON" || upper == "OFF" ||
		upper == "CANCELLED" || upper == "CANCELED" ||
		upper == "UPDATED" || upper == "EXISTS" ||
		strings.HasPrefix(upper, "INTERVAL") ||
		isTargetIntervalOK)
	isTerminalErr := upper == "ERROR" || upper == "FAILED" || upper == "TIMEOUT" ||
		upper == "FULL" || upper == "NOT_FOUND" ||
		strings.HasPrefix(upper, "INVALID")

	switch {
	case isRunning:
		match.Status = StatusRunning
	case isTerminalErr:
		match.Status = StatusError
		match.ErrorText = ackStatus
	case isTerminalOK:
		match.Status = StatusOK
	default:
		match.Status = StatusSent // unrecognized token — keep as sent
	}

	match.AckedAt = &now
	match.Result = result
	// Only mark the command as finished and drop it from the pending map
	// when we've reached a terminal state. Running long-runners stay in
	// pending so their *_DONE frame can find them later.
	if match.Status != StatusRunning {
		match.FinishedAt = &now
		// Finalise scan-class detections so the CommandsPage modal can
		// render the per-device list captured while the scan was running.
		// Two parallel representations:
		//   * ResultText — human-readable multi-line summary (legacy
		//     fallback, rendered as <pre> if no structured data).
		//   * Result["detections"] — JSON array of ScanDetection rows that
		//     the frontend renders as a real <table> with columns for
		//     Source/MAC/Band/RSSI/Type/Manufacturer/Name. Persists into
		//     the commands.result jsonb column alongside the existing
		//     ack envelope so a modal opened minutes after scan close can
		//     still render the rich table.
		if match.Name == "DEVICE_SCAN_START" || match.Name == "SCAN_START" {
			if len(match.scanRows) > 0 {
				merged := mergeScanRows(match.scanRows)
				match.ResultText = formatScanRows(merged)
				if match.Result == nil {
					match.Result = map[string]interface{}{}
				}
				match.Result["detections"] = merged
			}
		}
		delete(s.pending, matchKey)
		// If this DEVICE_SCAN_START had an auto-attached RAW_BLE_ON,
		// fire RAW_BLE_OFF now that the scan ended (SCAN_DONE_ACK
		// path). The STOP_ACK path below handles operator-issued
		// stops; this branch covers natural completion.
		if match.autoRawAttached && match.Name == "DEVICE_SCAN_START" {
			s.enqueueRawBLEFollowonLocked(match, "RAW_BLE_OFF")
		}
	}

	slog.Info("structured ACK matched", "ackKind", ackKind, "cmdName", cmdName, "status", match.Status)

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventCommand,
		Payload: match,
	})

	go s.persistCommand(match)

	// PROBE_ACK:STOPPED is also implicitly the close signal for the still-
	// RUNNING PROBE_START it terminates. The matcher above only updates the
	// latest matching pending command (which after a PROBE_STOP send is the
	// PROBE_STOP itself), so fan the STOPPED out to close PROBE_START too.
	// We don't filter on ackNode here — cmd.AckNode is whatever raw string
	// the dispatcher passed when STARTED arrived (often Meshtastic hex like
	// "!02ed5f04") while incoming ackNode may be the canonical AH short ID
	// from main.go's normalization, so the formats wouldn't match. Across a
	// single mesh there's normally just one running PROBE_START at a time,
	// and the explicit name/status filters above are sufficient.
	if ackKind == "PROBE_ACK" && strings.ToUpper(ackStatus) == "STOPPED" {
		for id, cmd := range s.pending {
			name := cmd.Name
			if name == "" {
				name = cmd.CommandType
			}
			if name != "PROBE_START" || cmd.Status != StatusRunning {
				continue
			}
			cmd.Status = StatusOK
			cmd.FinishedAt = &now
			delete(s.pending, id)
			s.hub.Broadcast(ws.Event{Type: ws.EventCommand, Payload: cmd})
			go s.persistCommand(cmd)
		}
	}

	// STOP_ACK fan-out: when the operator's STOP / SCAN_STOP / DEVICE_SCAN_STOP
	// row closes via STOP_ACK, also close any RUNNING scan-class command at
	// the same target. The firmware halts whatever job is active immediately
	// on STOP, but its *_DONE summary is gated behind !stopRequested upstream
	// in scanner.cpp / baseline.cpp / drone_detector.cpp / randomization.cpp,
	// so the originating *_START never receives its closing *_DONE_ACK.
	// Detections captured before the stop are already in the inventory DB via
	// the streaming DEVICE: frames the scanner emits every 3s — the only
	// missing piece is the lifecycle close.
	//
	// Strict target match: a node-targeted STOP closes only that node's RUNNING
	// scans. An @ALL STOP closes every RUNNING scan-class row regardless of
	// its target — the broadcast hit every sensor and they're all halting.
	if ackKind == "STOP_ACK" {
		scanClass := map[string]bool{
			"SCAN_START":          true,
			"DEVICE_SCAN_START":   true,
			"BASELINE_START":      true,
			"DRONE_START":         true,
			"DEAUTH_START":        true,
			"RANDOMIZATION_START": true,
			"PROBE_START":         true,
		}
		for id, cmd := range s.pending {
			name := cmd.Name
			if name == "" {
				name = cmd.CommandType
			}
			if !scanClass[name] || cmd.Status != StatusRunning {
				continue
			}
			if match.Target != "@ALL" && cmd.Target != match.Target {
				continue
			}
			cmd.Status = StatusOK
			cmd.FinishedAt = &now
			// Same finalisation as the SCAN_DONE_ACK path — flush any
			// accumulated scan-row detections into ResultText AND into
			// Result["detections"] for the modal table.
			if cmd.Name == "DEVICE_SCAN_START" || cmd.Name == "SCAN_START" {
				if len(cmd.scanRows) > 0 {
					merged := mergeScanRows(cmd.scanRows)
					cmd.ResultText = formatScanRows(merged)
					if cmd.Result == nil {
						cmd.Result = map[string]interface{}{}
					}
					cmd.Result["detections"] = merged
				}
			}
			delete(s.pending, id)
			s.hub.Broadcast(ws.Event{Type: ws.EventCommand, Payload: cmd})
			go s.persistCommand(cmd)
			// If a DEVICE_SCAN_START closed by this STOP fan-out had an
			// auto-attached RAW_BLE_ON, fire RAW_BLE_OFF for the same
			// target now that raw mode is no longer needed.
			if cmd.autoRawAttached && cmd.Name == "DEVICE_SCAN_START" {
				s.enqueueRawBLEFollowonLocked(cmd, "RAW_BLE_OFF")
			}
		}
	}
}

// RecordScanDetection appends a per-device row to every RUNNING scan-class
// command targeting the originating node (or @ALL). Called from the serial
// target-detected callback (Source="DEVICE") and the bleclassify
// classified-detection callback (Source="BLERAW") so each row of the
// CommandsPage modal can show what was actually seen during the scan,
// tagged with whether the firmware emitted a streaming D: frame for it
// or whether the lookupper classified its raw advertisement.
//
// Dedup is per (MAC, Source) so a device that arrived as both D: and B:
// during the same scan contributes two rows, while repeated emissions
// of the same kind across scan cycles contribute only one.
func (s *Service) RecordScanDetection(nodeID string, det ScanDetection) {
	if det.MAC == "" || det.Source == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Two formats arrive here depending on the source path:
	//   * binary Meshtastic protocol path: nodeID is the sender's Meshtastic
	//     node-num formatted as "!02ed5f04" (8 hex chars, ! prefix).
	//   * text-debug path (now blocked for target-detected): nodeID was the
	//     firmware short name like "HB55".
	// Operator commands target by short name ("@HB55"), but the matching ACK
	// landed via the binary path so cmd.AckNode is the !hex form. Match
	// against either to handle both legacy and current dispatch paths.
	targetWithAt := "@" + nodeID
	for _, cmd := range s.pending {
		name := cmd.Name
		if name == "" {
			name = cmd.CommandType
		}
		isScanCmd := name == "DEVICE_SCAN_START" || name == "SCAN_START"
		if !isScanCmd || cmd.Status != StatusRunning {
			continue
		}
		targetMatches := cmd.Target == "@ALL" ||
			cmd.Target == targetWithAt ||
			cmd.AckNode == nodeID
		if !targetMatches {
			continue
		}
		dup := false
		for _, r := range cmd.scanRows {
			if r.MAC == det.MAC && r.Source == det.Source {
				dup = true
				break
			}
		}
		if !dup {
			cmd.scanRows = append(cmd.scanRows, det)
		}
	}
}

// formatScanRows serialises an accumulated scan-detection list into the
// human-readable result_text shown in the CommandsPage modal. One row per
// line, prefixed by [DEVICE] or [BLERAW] so operators can see at a glance
// which devices were classified via the raw-advertisement pipeline vs.
// just observed as streaming detections.
func formatScanRows(rows []ScanDetection) string {
	if len(rows) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Devices: ")
	sb.WriteString(strconv.Itoa(len(rows)))
	sb.WriteString("\n")
	for _, r := range rows {
		sb.WriteString("[")
		sb.WriteString(r.Source)
		sb.WriteString("] ")
		sb.WriteString(r.MAC)
		if r.Band != "" {
			sb.WriteString(" ")
			sb.WriteString(r.Band)
		}
		sb.WriteString(" RSSI=")
		sb.WriteString(strconv.Itoa(r.RSSI))
		if r.DetectionType != "" {
			sb.WriteString(" type=")
			sb.WriteString(r.DetectionType)
		}
		if r.Manufacturer != "" {
			sb.WriteString(" mfr=")
			sb.WriteString(strconv.Quote(r.Manufacturer))
		}
		if r.LocalName != "" {
			sb.WriteString(" name=")
			sb.WriteString(strconv.Quote(r.LocalName))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// mergeScanRows collapses a per-(MAC, Source) row list into one row per MAC.
// Each BLE device produces two ScanDetection rows during a scan (a [DEVICE]
// row from the streaming D: emit and a [BLERAW] row from the raw-
// advertisement classifier callback) and the modal table rendered them as
// two separate entries. The BLERAW row carries the enriched fields
// (DetectionType, Manufacturer) and the DEVICE row carries the Band ("WiFi"
// or "BLE") that the firmware reported.
//
// Merge rule per MAC:
//   - Source is "BLERAW" if any contributing row was BLERAW (operators care
//     that the device went through the classifier, not that it also emitted
//     D:); otherwise "DEVICE".
//   - Band falls back to the DEVICE row's Band when BLERAW didn't set one
//     (which it never does).
//   - DetectionType, Manufacturer, LocalName take the first non-empty value
//     across rows — typically BLERAW provides the rich values and DEVICE
//     might fill LocalName from the firmware's "N:Name" suffix.
//   - RSSI takes the most recently observed value (last row in source order;
//     the BLERAW row arrives after its paired DEVICE row).
//
// Order is preserved by first-sight: the first row's MAC determines the
// position in the merged slice, regardless of which Source produced it.
func mergeScanRows(rows []ScanDetection) []ScanDetection {
	if len(rows) == 0 {
		return rows
	}
	type entry struct {
		idx int
		row ScanDetection
	}
	byMAC := make(map[string]*entry, len(rows))
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if e, ok := byMAC[r.MAC]; ok {
			if r.Source == "BLERAW" {
				e.row.Source = "BLERAW"
			}
			if e.row.Band == "" && r.Band != "" {
				e.row.Band = r.Band
			}
			if e.row.DetectionType == "" && r.DetectionType != "" {
				e.row.DetectionType = r.DetectionType
			}
			if e.row.Manufacturer == "" && r.Manufacturer != "" {
				e.row.Manufacturer = r.Manufacturer
			}
			if e.row.LocalName == "" && r.LocalName != "" {
				e.row.LocalName = r.LocalName
			}
			e.row.RSSI = r.RSSI
			continue
		}
		byMAC[r.MAC] = &entry{idx: len(order), row: r}
		order = append(order, r.MAC)
	}
	out := make([]ScanDetection, 0, len(order))
	for _, mac := range order {
		out = append(out, byMAC[mac].row)
	}
	return out
}

// RecordProbeHit increments the probeHits counter on the latest RUNNING
// PROBE_START command. Called from main.go's alert callback whenever a
// PROBE_HIT alert is dispatched. We don't filter on ackNode — cmd.AckNode
// is whatever raw string the dispatcher passed when the STARTED ACK
// landed (often Meshtastic hex like "!02ed5f04") while incoming ackNode
// is the canonical AH short ID from main.go's normalization, so the
// formats wouldn't reliably match. Across a single mesh there's normally
// just one running PROBE_START at a time; the latest-CreatedAt tiebreak
// is sufficient when there happen to be more.
func (s *Service) RecordProbeHit(ackNode string) {
	_ = ackNode // accepted for API symmetry with potential future filtering
	s.mu.Lock()
	defer s.mu.Unlock()
	var match *Command
	for _, cmd := range s.pending {
		name := cmd.Name
		if name == "" {
			name = cmd.CommandType
		}
		if name != "PROBE_START" || cmd.Status != StatusRunning {
			continue
		}
		if match == nil || cmd.CreatedAt.After(match.CreatedAt) {
			match = cmd
		}
	}
	if match == nil {
		return
	}
	if match.Result == nil {
		match.Result = map[string]interface{}{}
	}
	// JSON-decoded numbers come back as float64; freshly created counters
	// are int. Handle both so the increment doesn't reset the counter when
	// the command was reloaded from DB.
	switch v := match.Result["probeHits"].(type) {
	case float64:
		match.Result["probeHits"] = v + 1
	case int:
		match.Result["probeHits"] = v + 1
	default:
		match.Result["probeHits"] = 1
	}
	s.hub.Broadcast(ws.Event{Type: ws.EventCommand, Payload: match})
	go s.persistCommand(match)
}

func (s *Service) send(cmd *Command) {
	s.mu.Lock()
	sendFn := s.sendFn
	s.mu.Unlock()

	if sendFn == nil {
		slog.Warn("no send function configured, command queued but not sent", "id", cmd.ID)
		return
	}

	// Structured commands (built via Build()) already hold the on-wire text
	// in cmd.Line — e.g. "@ALL SCAN_START:2:60:1,6,11". Prefer that over the
	// legacy JSON payload path which predates the AntiHunter wire format.
	var payload []byte
	if cmd.Line != "" {
		payload = []byte(cmd.Line)
	} else {
		payload, _ = json.Marshal(cmd.Payload)
	}

	err := sendFn(cmd.TargetNode, cmd.CommandType, payload)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		slog.Error("failed to send command", "id", cmd.ID, "error", err)
		cmd.RetryCount++
		if cmd.RetryCount >= cmd.MaxRetries {
			cmd.Status = StatusError
			delete(s.pending, cmd.ID)
		}
	} else {
		now := time.Now()
		cmd.Status = StatusSent
		cmd.SentAt = &now
		s.nodeRate[cmd.TargetNode] = now
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventCommand,
		Payload: cmd,
	})

	go s.persistCommand(cmd)
}

// GetByID returns a command by ID. Checks pending first, then DB.
func (s *Service) GetByID(ctx context.Context, id string) (*Command, error) {
	s.mu.Lock()
	if cmd, ok := s.pending[id]; ok {
		s.mu.Unlock()
		return cmd, nil
	}
	s.mu.Unlock()

	// Fall back to DB. Select every column List() does so the modal-rendered
	// fields (ack_kind, ack_status, result_text, error_text, target, name,
	// line, params, finished_at) survive the round-trip from in-memory
	// command → DB persist → API read after the command left s.pending. The
	// previous narrow SELECT silently dropped result_text on terminated scans.
	var cmd Command
	var payloadJSON, resultJSON []byte
	var target, name, line, ackKind, ackStatus, ackNode, resultText, errorText sql.NullString
	var params []string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, target_node, command_type, payload, status,
			sent_at, acked_at, finished_at, result, retry_count, max_retries, created_at,
			target, name, params, line, ack_kind, ack_status, ack_node, result_text, error_text
		FROM commands WHERE id = $1`, id).Scan(
		&cmd.ID, &cmd.TargetNode, &cmd.CommandType, &payloadJSON,
		&cmd.Status, &cmd.SentAt, &cmd.AckedAt, &cmd.FinishedAt, &resultJSON,
		&cmd.RetryCount, &cmd.MaxRetries, &cmd.CreatedAt,
		&target, &name, &params, &line, &ackKind, &ackStatus, &ackNode,
		&resultText, &errorText)
	if err != nil {
		return nil, ErrCommandNotFound
	}
	if payloadJSON != nil {
		json.Unmarshal(payloadJSON, &cmd.Payload)
	}
	if resultJSON != nil {
		json.Unmarshal(resultJSON, &cmd.Result)
	}
	cmd.Target = target.String
	cmd.Name = name.String
	cmd.Line = line.String
	cmd.AckKind = ackKind.String
	cmd.AckStatus = ackStatus.String
	cmd.AckNode = ackNode.String
	cmd.ResultText = resultText.String
	cmd.ErrorText = errorText.String
	cmd.Params = params
	return &cmd, nil
}

// Delete removes a command from the pending queue and database.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM commands WHERE id = $1`, id)
	return err
}

// List returns recent commands from the database.
func (s *Service) List(ctx context.Context, limit int) ([]*Command, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, target_node, command_type, payload, status,
			sent_at, acked_at, finished_at, result, retry_count, max_retries, created_at,
			target, name, params, line, ack_kind, ack_status, ack_node, result_text, error_text
		FROM commands ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cmds []*Command
	for rows.Next() {
		var cmd Command
		var payloadJSON, resultJSON []byte
		var target, name, line, ackKind, ackStatus, ackNode, resultText, errorText sql.NullString
		var params []string
		if err := rows.Scan(&cmd.ID, &cmd.TargetNode, &cmd.CommandType, &payloadJSON,
			&cmd.Status, &cmd.SentAt, &cmd.AckedAt, &cmd.FinishedAt, &resultJSON,
			&cmd.RetryCount, &cmd.MaxRetries, &cmd.CreatedAt,
			&target, &name, &params, &line, &ackKind, &ackStatus, &ackNode,
			&resultText, &errorText); err != nil {
			continue
		}
		if payloadJSON != nil {
			json.Unmarshal(payloadJSON, &cmd.Payload)
		}
		if resultJSON != nil {
			json.Unmarshal(resultJSON, &cmd.Result)
		}
		cmd.Target = target.String
		cmd.Name = name.String
		cmd.Line = line.String
		cmd.AckKind = ackKind.String
		cmd.AckStatus = ackStatus.String
		cmd.AckNode = ackNode.String
		cmd.ResultText = resultText.String
		cmd.ErrorText = errorText.String
		cmd.Params = params
		cmds = append(cmds, &cmd)
	}
	return cmds, nil
}

// EnforceScanTimeouts walks the pending map and closes any RUNNING
// PROBE_START whose explicit duration window plus grace period has
// elapsed. The firmware emits no mesh signal on natural duration end of
// a probe scan — only PROBE_ACK:STOPPED in response to PROBE_STOP — so
// without this watchdog the row stays RUNNING forever after a limited
// scan completes. Skipped when params contain "FOREVER" (intentionally
// unbounded). Other long-running scans (SCAN_START, BASELINE_START etc.)
// have *_DONE frames that close them through the regular ACK path, so
// they don't need this safety net — keeping the scope narrow avoids
// closing rows that genuinely got stuck due to ack loss elsewhere.
func (s *Service) EnforceScanTimeouts(grace time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	closed := 0
	for id, cmd := range s.pending {
		name := cmd.Name
		if name == "" {
			name = cmd.CommandType
		}
		if name != "PROBE_START" || cmd.Status != StatusRunning {
			continue
		}
		if cmd.SentAt == nil {
			continue
		}
		// FOREVER scans intentionally never time out
		forever := false
		for _, p := range cmd.Params {
			if strings.EqualFold(strings.TrimSpace(p), "FOREVER") {
				forever = true
				break
			}
		}
		if forever {
			continue
		}
		// Duration is the second positional param: mode:duration[:FOREVER][:+ALL]
		if len(cmd.Params) < 2 {
			continue
		}
		durationSec, err := strconv.Atoi(strings.TrimSpace(cmd.Params[1]))
		if err != nil || durationSec <= 0 {
			continue
		}
		if now.Sub(*cmd.SentAt) < time.Duration(durationSec)*time.Second+grace {
			continue
		}

		cmd.Status = StatusOK
		finished := now
		cmd.FinishedAt = &finished
		if cmd.Result == nil {
			cmd.Result = map[string]interface{}{}
		}
		cmd.Result["closeReason"] = "scan-window-elapsed"
		delete(s.pending, id)
		s.hub.Broadcast(ws.Event{Type: ws.EventCommand, Payload: cmd})
		go s.persistCommand(cmd)
		closed++
	}

	if closed > 0 {
		slog.Info("commands: closed scan(s) on duration timeout", "count", closed)
	}
	return closed
}

// PruneOldCommands removes commands older than the retention period.
func (s *Service) PruneOldCommands(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 180
	}
	result, err := s.db.Pool.Exec(ctx, `
		DELETE FROM commands WHERE created_at < NOW() - $1 * INTERVAL '1 day'`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *Service) persistCommand(cmd *Command) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payloadJSON, _ := json.Marshal(cmd.Payload)
	resultJSON, _ := json.Marshal(cmd.Result)

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO commands (id, target_node, command_type, payload, status,
			sent_at, acked_at, result, retry_count, max_retries, created_at, updated_at,
			target, name, params, line, finished_at, ack_kind, ack_status, ack_node,
			result_text, error_text)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(),
			$12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			sent_at = EXCLUDED.sent_at,
			acked_at = EXCLUDED.acked_at,
			finished_at = EXCLUDED.finished_at,
			result = EXCLUDED.result,
			retry_count = EXCLUDED.retry_count,
			ack_kind = EXCLUDED.ack_kind,
			ack_status = EXCLUDED.ack_status,
			ack_node = EXCLUDED.ack_node,
			result_text = EXCLUDED.result_text,
			error_text = EXCLUDED.error_text,
			updated_at = NOW()`,
		cmd.ID, cmd.TargetNode, cmd.CommandType, payloadJSON,
		string(cmd.Status), cmd.SentAt, cmd.AckedAt, resultJSON,
		cmd.RetryCount, cmd.MaxRetries, cmd.CreatedAt,
		nilStr(cmd.Target), nilStr(cmd.Name), cmd.Params, nilStr(cmd.Line),
		cmd.FinishedAt, nilStr(cmd.AckKind), nilStr(cmd.AckStatus), nilStr(cmd.AckNode),
		nilStr(cmd.ResultText), nilStr(cmd.ErrorText),
	)
	if err != nil {
		slog.Error("failed to persist command", "id", cmd.ID, "error", err)
	}
}

func nilStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
