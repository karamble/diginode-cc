package serial

import (
	"log/slog"
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
}

// PacketHandler processes decoded Meshtastic packets.
type PacketHandler func(packet *FromRadioPacket)

// NewManager creates a new serial port manager.
func NewManager(cfg *config.Config, hub *ws.Hub) *Manager {
	return &Manager{
		cfg:    cfg,
		hub:    hub,
		stopCh: make(chan struct{}),
	}
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

	for {
		select {
		case <-m.stopCh:
			return nil
		default:
		}

		err := m.connect()
		if err != nil {
			slog.Warn("serial connection failed, retrying in 5s", "error", err)
			select {
			case <-time.After(5 * time.Second):
				continue
			case <-m.stopCh:
				return nil
			}
		}

		m.readLoop()

		// If we get here, connection was lost
		m.mu.Lock()
		m.connected = false
		if m.port != nil {
			m.port.Close()
			m.port = nil
		}
		m.mu.Unlock()

		slog.Warn("serial connection lost, reconnecting in 3s")
		select {
		case <-time.After(3 * time.Second):
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

	slog.Info("serial port connected", "device", m.cfg.SerialDevice)

	// Send initial config request to radio
	if err := m.sendWantConfig(); err != nil {
		slog.Warn("failed to send initial config request", "error", err)
	}

	return nil
}

func (m *Manager) readLoop() {
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
