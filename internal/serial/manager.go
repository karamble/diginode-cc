package serial

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/ws"
	"go.bug.st/serial"
)

// TextMessage is a mesh text message stored in the ring buffer.
type TextMessage struct {
	Seq       int64  `json:"seq"`
	NodeID    string `json:"nodeId"`
	Message   string `json:"message"`
	SiteID    string `json:"siteId,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Manager handles Meshtastic serial port communication.
type Manager struct {
	cfg          *config.Config
	hub          *ws.Hub
	port         serial.Port
	mu           sync.Mutex
	connected    bool
	stopCh       chan struct{}
	handlers     []PacketHandler
	textMessages []TextMessage
	textSeq      int64
	textMu       sync.RWMutex
	deviceTime   time.Time
	deviceTimeMu sync.RWMutex
	protocol     string      // "binary" or "text" (default "text")
	textParser   *TextParser // text-mode line parser
	syntheticID  atomic.Uint32 // monotonic counter for synthetic packet IDs (text-mode fallback)
}

// PacketHandler processes decoded Meshtastic packets.
type PacketHandler func(packet *FromRadioPacket)

// NewManager creates a new serial port manager.
func NewManager(cfg *config.Config, hub *ws.Hub) *Manager {
	return &Manager{
		cfg:        cfg,
		hub:        hub,
		stopCh:     make(chan struct{}),
		protocol:   "text", // Text mode: reads debug console lines (like CC PRO meshtastic-rewrite)
		textParser: NewTextParser(),
	}
}

// SetProtocol switches the serial protocol mode ("binary" or "text").
func (m *Manager) SetProtocol(proto string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.protocol = proto
}

// GetProtocol returns the current serial protocol mode.
func (m *Manager) GetProtocol() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.protocol
}

// RegisterHandler adds a packet handler callback.
func (m *Manager) RegisterHandler(h PacketHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, h)
}

// IsConnected returns whether the serial port is currently connected.
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// Start opens the serial port and begins reading Meshtastic frames.
func (m *Manager) Start() error {
	slog.Info("starting serial manager", "device", m.cfg.SerialDevice, "baud", m.cfg.SerialBaud)

	// Log once if the device file doesn't exist yet (avoids noisy retries).
	if _, err := os.Stat(m.cfg.SerialDevice); errors.Is(err, os.ErrNotExist) {
		slog.Info("serial device not present, will retry when available", "device", m.cfg.SerialDevice)
	}

	baseDelay := 500 * time.Millisecond
	maxDelay := 15 * time.Second
	delay := baseDelay

	for {
		select {
		case <-m.stopCh:
			return nil
		default:
		}

		err := m.connect()
		if err != nil {
			// Quiet log for missing device file; warn only for unexpected errors.
			if errors.Is(err, os.ErrNotExist) {
				slog.Debug("serial device not present, retrying", "device", m.cfg.SerialDevice, "delay", delay)
			} else {
				slog.Warn("serial connection failed, retrying", "error", err, "delay", delay)
			}
			select {
			case <-time.After(delay):
				// Exponential backoff with jitter
				delay = time.Duration(float64(delay) * 1.5)
				if delay > maxDelay {
					delay = maxDelay
				}
				// Add jitter (±20%)
				jitter := time.Duration(float64(delay) * 0.2 * (2*float64(time.Now().UnixNano()%100)/100 - 1))
				delay += jitter
				continue
			case <-m.stopCh:
				return nil
			}
		}

		// Reset delay on successful connection
		delay = baseDelay

		// Periodically re-send wantConfig to refresh node data (battery, position, etc.)
		// The firmware re-sends all NodeInfo with fresh DeviceMetrics on each wantConfig.
		refreshDone := make(chan struct{})
		go m.periodicConfigRefresh(refreshDone)

		m.readLoop()

		close(refreshDone)

		// Connection lost
		m.mu.Lock()
		m.connected = false
		if m.port != nil {
			m.port.Close()
			m.port = nil
		}
		m.mu.Unlock()

		slog.Warn("serial connection lost, reconnecting", "delay", delay)
		m.broadcastSerialState(false)
		select {
		case <-time.After(delay):
			delay = time.Duration(float64(delay) * 1.5)
			if delay > maxDelay {
				delay = maxDelay
			}
		case <-m.stopCh:
			return nil
		}
	}
}

// Stop shuts down the serial manager. The manager can be restarted via Start().
func (m *Manager) Stop() {
	close(m.stopCh)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port != nil {
		m.port.Close()
		m.port = nil
	}
	m.connected = false
	// Recreate stopCh so Start() can be called again (e.g., via POST /serial/connect).
	m.stopCh = make(chan struct{})
}

// broadcastSerialState sends a health event to all WebSocket clients
// so the frontend can update the serial connection indicator.
func (m *Manager) broadcastSerialState(connected bool) {
	if m.hub == nil {
		return
	}
	m.hub.Broadcast(ws.Event{
		Type: ws.EventHealth,
		Payload: map[string]any{
			"serial": map[string]any{
				"connected": connected,
				"device":    m.cfg.SerialDevice,
			},
		},
	})
}

func (m *Manager) connect() error {
	mode := &serial.Mode{
		BaudRate: m.cfg.SerialBaud,
	}

	port, err := serial.Open(m.cfg.SerialDevice, mode)
	if err != nil {
		return err
	}

	// Set read timeout
	port.SetReadTimeout(100 * time.Millisecond)

	m.mu.Lock()
	m.port = port
	m.connected = true
	m.mu.Unlock()

	slog.Info("serial port connected", "device", m.cfg.SerialDevice, "protocol", m.protocol)

	// Notify frontend of serial connection state change.
	m.broadcastSerialState(true)

	// Send wantConfig to activate the Meshtastic serial API session.
	// Required for the firmware to process ToRadio packets (sending messages).
	if err := m.sendWantConfig(); err != nil {
		slog.Warn("failed to send initial config request", "error", err)
	}

	return nil
}

func (m *Manager) readLoop() {
	// Always use the hybrid reader: handles both binary protobuf frames
	// and text debug console lines. WantConfig is required for the firmware
	// to process ToRadio commands (like sending messages).
	m.readLoopBinary()
}

// readLoopBinary is a hybrid reader: it parses binary protobuf frames (0x94 0xC3)
// AND accumulates non-frame bytes as text lines for the text parser.
// The Heltec outputs both binary FromRadio frames and text debug console lines
// on the same USB serial port.
func (m *Manager) readLoopBinary() {
	buf := make([]byte, 4096)
	decoder := NewFrameDecoder()
	var textBuf []byte // accumulates bytes between binary frames

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		n, err := m.port.Read(buf)
		if err != nil {
			slog.Warn("serial read error", "error", err)
			return
		}
		if n == 0 {
			continue
		}

		// Feed all bytes to the frame decoder for binary frame extraction
		frames := decoder.Feed(buf[:n])
		for _, frame := range frames {
			packet, err := DecodeFromRadio(frame)
			if err != nil {
				slog.Debug("failed to decode FromRadio", "error", err)
				continue
			}
			m.dispatchPacket(packet)
		}

		// Also accumulate bytes for text line parsing.
		// Binary frame bytes (0x94 0xC3 ...) won't form valid text lines,
		// but debug console text lines (terminated by \n) will.
		textBuf = append(textBuf, buf[:n]...)

		// Extract complete text lines from the buffer
		for {
			idx := bytes.IndexByte(textBuf, '\n')
			if idx < 0 {
				break
			}
			line := string(textBuf[:idx])
			textBuf = textBuf[idx+1:]

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip lines that look like binary garbage (contain non-printable chars)
			if !isPrintableText(line) {
				continue
			}

			// Parse text lines for sensor data and message echoes.
			// After wantConfig, the firmware is in API mode but still
			// outputs some text debug lines (especially message echoes).
			if m.textParser != nil {
				events := m.textParser.ParseLine(line)
				for _, evt := range events {
					if evt.Kind != "raw" {
						m.dispatchTextEvent(evt)
					}
				}
			}
		}

		// Prevent text buffer from growing unbounded
		if len(textBuf) > 4096 {
			textBuf = textBuf[len(textBuf)-2048:]
		}
	}
}

// isPrintableText checks if a string is mostly printable ASCII (not binary garbage).
func isPrintableText(s string) bool {
	if len(s) == 0 {
		return false
	}
	printable := 0
	for _, c := range s {
		if c >= 0x20 && c <= 0x7E {
			printable++
		}
	}
	// At least 70% printable characters
	return float64(printable)/float64(len(s)) > 0.7
}

// readLoopText reads newline-delimited text from the Heltec debug console.
// Uses raw Read to handle serial port timeouts gracefully (no reconnect on idle).
func (m *Manager) readLoopText() {
	buf := make([]byte, 4096)
	var lineBuf []byte

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		n, err := m.port.Read(buf)
		if err != nil {
			// Timeout with no data is normal for serial — just keep reading
			if n == 0 {
				continue
			}
			slog.Warn("serial read error", "error", err)
			return
		}
		if n == 0 {
			continue
		}

		// Accumulate bytes and extract complete lines
		lineBuf = append(lineBuf, buf[:n]...)

		for {
			idx := bytes.IndexByte(lineBuf, '\n')
			if idx < 0 {
				break
			}
			line := strings.TrimSpace(string(lineBuf[:idx]))
			lineBuf = lineBuf[idx+1:]

			if line == "" {
				continue
			}

			events := m.textParser.ParseLine(line)
			for _, evt := range events {
				m.dispatchTextEvent(evt)
			}
		}

		// Prevent buffer from growing unbounded
		if len(lineBuf) > 4096 {
			lineBuf = lineBuf[len(lineBuf)-2048:]
		}
	}
}

// dispatchTextEvent converts a ParsedEvent into synthetic FromRadioPackets for
// the existing handler pipeline, and stores text messages in the ring buffer.
func (m *Manager) dispatchTextEvent(evt *ParsedEvent) {
	m.mu.Lock()
	handlers := make([]PacketHandler, len(m.handlers))
	copy(handlers, m.handlers)
	m.mu.Unlock()

	// Text messages are stored in the ring buffer by the chat service's HandleTextMessage
	// callback (via addToBuffer). We don't add here to avoid duplicates.

	// Convert to synthetic FromRadioPacket for the existing dispatcher pipeline
	switch evt.Kind {
	case "text-message":
		text, _ := evt.Data["text"].(string)

		// Skip echoes of locally sent messages (nodeId "!00000000" or "unknown").
		// These are already handled by the API send handler.
		if evt.NodeID == "!00000000" || evt.NodeID == "unknown" {
			break
		}

		// Use extracted Meshtastic packet ID, or generate a unique synthetic one
		// to avoid the dedup filter incorrectly killing messages (all ID=0 from
		// same node would hash to the same dedup key).
		pktID := evt.PacketID
		if pktID == 0 {
			pktID = m.syntheticID.Add(1)
		}
		pkt := &FromRadioPacket{
			Type: FromRadioMeshPacket,
			MeshPacket: &MeshPacketData{
				PortNum: PortNumTextMessage,
				Payload: []byte(text),
				From:    parseNodeNum(evt.NodeID),
				To:      BroadcastAddr,
				ID:      pktID,
			},
		}
		for _, h := range handlers {
			h(pkt)
		}

	case "drone-telemetry":
		jsonData, err := json.Marshal(evt.Data)
		if err != nil {
			slog.Warn("failed to marshal drone telemetry", "error", err)
			return
		}
		dronePktID := evt.PacketID
		if dronePktID == 0 {
			dronePktID = m.syntheticID.Add(1)
		}
		pkt := &FromRadioPacket{
			Type: FromRadioMeshPacket,
			MeshPacket: &MeshPacketData{
				PortNum: 160, // DETECTION_SENSOR_APP
				Payload: jsonData,
				From:    parseNodeNum(evt.NodeID),
				ID:      dronePktID,
			},
		}
		for _, h := range handlers {
			h(pkt)
		}

	default:
		// Other event types (alert, node-telemetry, command-ack, target-detected, raw)
		// are logged for debug; downstream consumers can be added later.
		if evt.Kind != "raw" {
			slog.Debug("text event", "kind", evt.Kind, "nodeId", evt.NodeID,
				"category", evt.Category, "level", evt.Level)
		}
	}
}

func (m *Manager) dispatchPacket(packet *FromRadioPacket) {
	m.mu.Lock()
	handlers := make([]PacketHandler, len(m.handlers))
	copy(handlers, m.handlers)
	m.mu.Unlock()

	for _, h := range handlers {
		h(packet)
	}
}

// SendToRadio encodes and sends a ToRadio packet via the serial port.
func (m *Manager) SendToRadio(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.port == nil {
		return ErrNotConnected
	}

	frame := EncodeFrame(data)
	_, err := m.port.Write(frame)
	return err
}

// AddTextMessage stores a text message in the ring buffer (polled by gotailme).
func (m *Manager) AddTextMessage(nodeID, message, siteID string) {
	m.textMu.Lock()
	defer m.textMu.Unlock()

	m.textSeq++
	msg := TextMessage{
		Seq:       m.textSeq,
		NodeID:    nodeID,
		Message:   message,
		SiteID:    siteID,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	m.textMessages = append(m.textMessages, msg)
	// Keep last 200
	if len(m.textMessages) > 200 {
		m.textMessages = m.textMessages[len(m.textMessages)-200:]
	}
}

// ClearTextMessages removes all messages from the ring buffer.
func (m *Manager) ClearTextMessages() {
	m.textMu.Lock()
	defer m.textMu.Unlock()
	m.textMessages = nil
	m.textSeq = 0
}

// GetTextMessages returns messages with seq > sinceSeq.
func (m *Manager) GetTextMessages(sinceSeq int64) []TextMessage {
	m.textMu.RLock()
	defer m.textMu.RUnlock()

	var result []TextMessage
	for _, msg := range m.textMessages {
		if msg.Seq > sinceSeq {
			result = append(result, msg)
		}
	}
	return result
}

// SetDeviceTime records the last known device time from the radio.
func (m *Manager) SetDeviceTime(t time.Time) {
	m.deviceTimeMu.Lock()
	defer m.deviceTimeMu.Unlock()
	m.deviceTime = t
}

// GetDeviceTime returns the last known device time and whether it's been set.
func (m *Manager) GetDeviceTime() (time.Time, bool) {
	m.deviceTimeMu.RLock()
	defer m.deviceTimeMu.RUnlock()
	if m.deviceTime.IsZero() {
		return time.Time{}, false
	}
	return m.deviceTime, true
}

// SimulatePacket dispatches a fake FromRadio packet through registered handlers.
// Used for testing without a real serial connection.
func (m *Manager) SimulatePacket(pkt *FromRadioPacket) {
	m.dispatchPacket(pkt)
}

// periodicConfigRefresh sends heartbeats every 15 seconds to keep the firmware's
// serial API alive (required for receiving MeshPackets from remote nodes), and
// re-sends wantConfig every 10 minutes to refresh cached node data.
func (m *Manager) periodicConfigRefresh(done chan struct{}) {
	heartbeat := time.NewTicker(15 * time.Second)
	configRefresh := time.NewTicker(10 * time.Minute)
	defer heartbeat.Stop()
	defer configRefresh.Stop()

	for {
		select {
		case <-heartbeat.C:
			data := BuildHeartbeat()
			if err := m.SendToRadio(data); err != nil {
				slog.Debug("heartbeat send failed", "error", err)
			}
		case <-configRefresh.C:
			if err := m.sendWantConfig(); err != nil {
				slog.Warn("periodic config refresh failed", "error", err)
			} else {
				slog.Info("periodic config refresh sent")
			}
		case <-done:
			return
		case <-m.stopCh:
			return
		}
	}
}

// RefreshConfig re-sends wantConfig to get fresh NodeInfo from the firmware.
// Called by the API to trigger an on-demand telemetry refresh.
func (m *Manager) RefreshConfig() error {
	return m.sendWantConfig()
}

func (m *Manager) sendWantConfig() error {
	// Build a ToRadio { want_config_id: ... } protobuf
	// This tells the radio to send us its full config
	data := BuildWantConfig()
	return m.SendToRadio(data)
}
