package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/ws"
)

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
}

// Service manages the command queue with rate limiting and ACK tracking.
type Service struct {
	db       *database.DB
	hub      *ws.Hub
	pending  map[string]*Command
	nodeRate map[uint32]time.Time // last command time per node
	mu       sync.Mutex
	sendFn   func(nodeNum uint32, cmdType string, payload []byte) error
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

	go s.send(cmd)
	return nil
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
	case "SCAN_DONE_ACK":
		wantSuffix = "SCAN_START" // matches SCAN_START + DEVICE_SCAN_START
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
	isTerminalOK := !isRunning && (upper == "" ||
		upper == "OK" || upper == "COMPLETE" || upper == "COMPLETED" ||
		upper == "FINISHED" || upper == "SUCCESS" ||
		upper == "STOPPED" ||
		upper == "ENABLED" || upper == "DISABLED" ||
		upper == "CANCELLED" || upper == "CANCELED" ||
		upper == "UPDATED" || upper == "EXISTS" ||
		strings.HasPrefix(upper, "INTERVAL"))
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
		delete(s.pending, matchKey)
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

	// Fall back to DB
	var cmd Command
	var payloadJSON, resultJSON []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, target_node, command_type, payload, status,
			sent_at, acked_at, result, retry_count, max_retries, created_at
		FROM commands WHERE id = $1`, id).Scan(
		&cmd.ID, &cmd.TargetNode, &cmd.CommandType, &payloadJSON,
		&cmd.Status, &cmd.SentAt, &cmd.AckedAt, &resultJSON,
		&cmd.RetryCount, &cmd.MaxRetries, &cmd.CreatedAt)
	if err != nil {
		return nil, ErrCommandNotFound
	}
	if payloadJSON != nil {
		json.Unmarshal(payloadJSON, &cmd.Payload)
	}
	if resultJSON != nil {
		json.Unmarshal(resultJSON, &cmd.Result)
	}
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
