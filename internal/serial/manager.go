package serial

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
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
}

// PacketHandler processes decoded Meshtastic packets.
type PacketHandler func(packet *FromRadioPacket)

// NewManager creates a new serial port manager.
func NewManager(cfg *config.Config, hub *ws.Hub) *Manager {
	return &Manager{
		cfg:        cfg,
		hub:        hub,
		stopCh:     make(chan struct{}),
		protocol:   "text", // Binary protobuf mode (sends wantConfig, reads FromRadio frames)
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
			slog.Warn("serial connection failed, retrying", "error", err, "delay", delay)
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

		m.readLoop()

		// Connection lost
		m.mu.Lock()
		m.connected = false
		if m.port != nil {
			m.port.Close()
			m.port = nil
		}
		m.mu.Unlock()

		slog.Warn("serial connection lost, reconnecting", "delay", delay)
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

// Stop shuts down the serial manager.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port != nil {
		m.port.Close()
		m.port = nil
	}
	m.connected = false
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

	// Only send binary config request in binary mode
	if m.protocol == "binary" {
		if err := m.sendWantConfig(); err != nil {
			slog.Warn("failed to send initial config request", "error", err)
		}
	}

	return nil
}

func (m *Manager) readLoop() {
	m.mu.Lock()
	proto := m.protocol
	m.mu.Unlock()

	if proto == "text" {
		m.readLoopText()
		return
	}
	m.readLoopBinary()
}

// readLoopBinary is the existing binary protobuf frame reader.
func (m *Manager) readLoopBinary() {
	buf := make([]byte, 4096)
	decoder := NewFrameDecoder()

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

		frames := decoder.Feed(buf[:n])
		for _, frame := range frames {
			packet, err := DecodeFromRadio(frame)
			if err != nil {
				slog.Debug("failed to decode FromRadio", "error", err)
				continue
			}
			m.dispatchPacket(packet)
		}
	}
}

// readLoopText reads newline-delimited text from the Heltec debug console.
func (m *Manager) readLoopText() {
	reader := bufio.NewReader(m.port)
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				slog.Warn("serial read error", "error", err)
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		events := m.textParser.ParseLine(line)
		for _, evt := range events {
			m.dispatchTextEvent(evt)
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

	// Store text messages in ring buffer
	if evt.Kind == "text-message" {
		text, _ := evt.Data["text"].(string)
		m.AddTextMessage(evt.NodeID, text, "")
	}

	// Convert to synthetic FromRadioPacket for the existing dispatcher pipeline
	switch evt.Kind {
	case "text-message":
		text, _ := evt.Data["text"].(string)
		pkt := &FromRadioPacket{
			Type: FromRadioMeshPacket,
			MeshPacket: &MeshPacketData{
				PortNum: PortNumTextMessage,
				Payload: []byte(text),
				From:    parseNodeNum(evt.NodeID),
				To:      BroadcastAddr,
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
		pkt := &FromRadioPacket{
			Type: FromRadioMeshPacket,
			MeshPacket: &MeshPacketData{
				PortNum: 160, // DETECTION_SENSOR_APP
				Payload: jsonData,
				From:    parseNodeNum(evt.NodeID),
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

func (m *Manager) sendWantConfig() error {
	// Build a ToRadio { want_config_id: ... } protobuf
	// This tells the radio to send us its full config
	data := BuildWantConfig()
	return m.SendToRadio(data)
}
