package serial

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	cfg              *config.Config
	hub              *ws.Hub
	port             serial.Port
	mu               sync.Mutex
	connected        bool
	stopCh           chan struct{}
	handlers         []PacketHandler
	textMessages     []TextMessage
	textSeq          int64
	textMu           sync.RWMutex
	deviceTime       time.Time
	deviceTimeMu     sync.RWMutex
	protocol         string        // "binary" or "text" (default "text")
	textParser       *TextParser   // text-mode line parser
	syntheticID      atomic.Uint32 // monotonic counter for synthetic packet IDs (text-mode fallback)
	onTargetDetected func(mac, ssid, deviceType string, rssi, channel int, lat, lon float64, nodeID string)
	onTriData        func(mac, nodeID string, rssi int, lat, lon float64)
	onTriFinal       func(mac string, lat, lon, confidence, uncertainty float64)
	onTriComplete    func(mac string, nodes int)
	onMeshTelemetry  func(from uint32, lat, lon float64, data map[string]interface{})
	onCommandAck     func(ackKind, ackStatus, ackNode string, data map[string]interface{})
	// Stored radio config sections (populated during wantConfig dump)
	radioConfig   map[string]*ConfigPayload
	radioConfigMu sync.RWMutex
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

// SetTargetDetectedCallback sets the handler for target/device detection events from mesh sensors.
func (m *Manager) SetTargetDetectedCallback(fn func(mac, ssid, deviceType string, rssi, channel int, lat, lon float64, nodeID string)) {
	m.onTargetDetected = fn
}

// SetMeshTelemetryCallback sets the handler for node-telemetry events extracted from
// remote AntiHunter TEXTMSG payloads (heartbeats, STATUS lines with embedded GPS).
// The "from" argument is the authoritative Meshtastic node number of the sender.
func (m *Manager) SetMeshTelemetryCallback(fn func(from uint32, lat, lon float64, data map[string]interface{})) {
	m.onMeshTelemetry = fn
}

// SetCommandAckCallback sets the handler for command-ack events parsed out of
// TEXTMSG lines like "AH01: SCAN_ACK:STARTED". The callback is responsible for
// matching the ACK against a pending command and updating its lifecycle.
func (m *Manager) SetCommandAckCallback(fn func(ackKind, ackStatus, ackNode string, data map[string]interface{})) {
	m.onCommandAck = fn
}

// SetTriangulationCallbacks sets handlers for T_D/T_F/T_C triangulation protocol events.
func (m *Manager) SetTriangulationCallbacks(
	onData func(mac, nodeID string, rssi int, lat, lon float64),
	onFinal func(mac string, lat, lon, confidence, uncertainty float64),
	onComplete func(mac string, nodes int),
) {
	m.onTriData = onData
	m.onTriFinal = onFinal
	m.onTriComplete = onComplete
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

	baseDelay := time.Duration(m.cfg.SerialReconnectBaseMS) * time.Millisecond
	maxDelay := time.Duration(m.cfg.SerialReconnectMaxMS) * time.Millisecond
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
				// Add jitter
				jitter := time.Duration(float64(delay) * m.cfg.SerialReconnectJitter * (2*float64(time.Now().UnixNano()%100)/100 - 1))
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
		// Use the destination from the parsed event if available (extracted from
		// preceding Lora RX debug line). Falls back to broadcast if unknown.
		toAddr := evt.ToNode
		if toAddr == 0 {
			toAddr = BroadcastAddr
		}
		pkt := &FromRadioPacket{
			Type: FromRadioMeshPacket,
			MeshPacket: &MeshPacketData{
				PortNum: PortNumTextMessage,
				Payload: []byte(text),
				From:    parseNodeNum(evt.NodeID),
				To:      toAddr,
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
				PortNum: 10, // DETECTION_SENSOR_APP (PortNumDetectionSensor)
				Payload: jsonData,
				From:    parseNodeNum(evt.NodeID),
				ID:      dronePktID,
			},
		}
		for _, h := range handlers {
			h(pkt)
		}

	case "target-detected":
		mac, _ := evt.Data["mac"].(string)
		if mac == "" {
			break
		}
		rssi, _ := evt.Data["rssi"].(int)
		ssid, _ := evt.Data["name"].(string)
		devType, _ := evt.Data["type"].(string)
		channel, _ := evt.Data["channel"].(int)
		lat, _ := evt.Data["lat"].(float64)
		lon, _ := evt.Data["lon"].(float64)

		if m.onTargetDetected != nil {
			m.onTargetDetected(mac, ssid, devType, rssi, channel, lat, lon, evt.NodeID)
		}

	case "tri-data":
		mac, _ := evt.Data["mac"].(string)
		rssi, _ := evt.Data["rssi"].(int)
		lat, _ := evt.Data["nodeLat"].(float64)
		lon, _ := evt.Data["nodeLon"].(float64)
		if m.onTriData != nil && mac != "" {
			m.onTriData(mac, evt.NodeID, rssi, lat, lon)
		}

	case "tri-final":
		mac, _ := evt.Data["mac"].(string)
		lat, _ := evt.Data["lat"].(float64)
		lon, _ := evt.Data["lon"].(float64)
		conf, _ := evt.Data["confidence"].(float64)
		unc, _ := evt.Data["uncertainty"].(float64)
		if m.onTriFinal != nil && mac != "" {
			m.onTriFinal(mac, lat, lon, conf, unc)
		}

	case "tri-complete":
		mac, _ := evt.Data["mac"].(string)
		nodes, _ := evt.Data["nodes"].(int)
		if m.onTriComplete != nil {
			m.onTriComplete(mac, nodes)
		}

	case "node-telemetry":
		// Emitted by AntiHunter NODE_HB lines (normal + battery-saver variants) and
		// STATUS frames. Forward lat/lon + temperature to the node handler so the
		// sender's position and OLED temp reading are refreshed even though the
		// AntiHunter firmware never emits a protobuf Position or Telemetry packet.
		// Fire whenever ANY useful field is set — plain "Time:X Temp:Y" heartbeats
		// without GPS would otherwise silently drop the temperature update.
		lat, _ := evt.Data["lat"].(float64)
		lon, _ := evt.Data["lon"].(float64)
		tempC, _ := evt.Data["temperatureC"].(float64)
		from := parseNodeNum(evt.NodeID)
		if m.onMeshTelemetry != nil && from != 0 && (lat != 0 || lon != 0 || tempC != 0) {
			m.onMeshTelemetry(from, lat, lon, evt.Data)
		}

	case "command-ack":
		// AntiHunter replies to commands with lines like "AH01: SCAN_ACK:STARTED".
		// Hand the kind/status/node off to the commands service so it can match
		// the ACK against a pending command and move it to OK/ERROR.
		if m.onCommandAck != nil {
			ackKind, _ := evt.Data["ackType"].(string)
			ackStatus, _ := evt.Data["status"].(string)
			m.onCommandAck(ackKind, ackStatus, evt.NodeID, evt.Data)
		}

	default:
		// Other event types (alert, command-ack, raw) are logged for debug;
		// downstream consumers can be added later.
		if evt.Kind != "raw" {
			slog.Debug("text event", "kind", evt.Kind, "nodeId", evt.NodeID,
				"category", evt.Category, "level", evt.Level)
		}
	}
}

// ProcessMeshText feeds a Meshtastic TEXTMSG payload received from a remote node
// through the AntiHunter text parser. Any structured events embedded in the text
// (heartbeats with GPS, target detections, drone telemetry, triangulation frames)
// are routed into the same dispatch pipeline as locally-parsed console lines. The
// Meshtastic source node number overrides whatever NodeID prefix the sensor used,
// since the mesh identity is authoritative.
func (m *Manager) ProcessMeshText(from, to, channel uint32, text string) {
	if m.textParser == nil || text == "" {
		return
	}
	meshNodeID := fmt.Sprintf("!%08x", from)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		events := m.textParser.ParseLine(line)
		for _, evt := range events {
			if evt.Kind == "raw" || evt.Kind == "text-message" {
				continue
			}
			evt.NodeID = meshNodeID
			if evt.ToNode == 0 {
				evt.ToNode = to
			}
			m.dispatchTextEvent(evt)
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

// WakeDevice toggles DTR/RTS to hardware-reset the ESP32-S3 out of deep sleep.
// It closes the current port, opens a raw connection to toggle the control lines,
// then lets the reconnect loop in Start() re-establish the Meshtastic session.
func (m *Manager) WakeDevice() error {
	device := m.cfg.SerialDevice

	// Close the existing connection so we can toggle control lines directly.
	m.mu.Lock()
	if m.port != nil {
		m.port.Close()
		m.port = nil
	}
	m.connected = false
	m.mu.Unlock()
	m.broadcastSerialState(false)

	slog.Info("waking device via DTR/RTS reset", "device", device)

	// Open a temporary connection just to toggle the lines.
	port, err := serial.Open(device, &serial.Mode{BaudRate: 115200})
	if err != nil {
		return err
	}

	// Pulse RTS high (pulls EN low on ESP32-S3 Heltec V3)
	port.SetDTR(false)
	port.SetRTS(true)
	time.Sleep(100 * time.Millisecond)

	// Release — EN goes high, ESP32 boots
	port.SetDTR(false)
	port.SetRTS(false)
	time.Sleep(100 * time.Millisecond)

	port.Close()

	slog.Info("DTR/RTS reset complete, serial reconnect loop will pick up the device")
	// The Start() reconnect loop will detect the device and re-establish the session.
	return nil
}

// StoreConfig stores a parsed config section received during the wantConfig dump.
func (m *Manager) StoreConfig(cfg *ConfigPayload) {
	if cfg == nil || cfg.Section == "" {
		return
	}
	m.radioConfigMu.Lock()
	defer m.radioConfigMu.Unlock()
	if m.radioConfig == nil {
		m.radioConfig = make(map[string]*ConfigPayload)
	}
	m.radioConfig[cfg.Section] = cfg
}

// GetRadioConfig returns all stored radio config sections.
func (m *Manager) GetRadioConfig() map[string]*ConfigPayload {
	m.radioConfigMu.RLock()
	defer m.radioConfigMu.RUnlock()
	out := make(map[string]*ConfigPayload, len(m.radioConfig))
	for k, v := range m.radioConfig {
		out[k] = v
	}
	return out
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

// SimulateLines feeds raw text lines through the text parser and dispatcher,
// as if they arrived on the serial port. Used by the drone simulator and dev tools.
// Returns the number of lines that produced non-raw events.
func (m *Manager) SimulateLines(lines []string) int {
	if m.textParser == nil {
		return 0
	}
	processed := 0
	for _, line := range lines {
		events := m.textParser.ParseLine(line)
		for _, evt := range events {
			if evt.Kind != "raw" {
				m.dispatchTextEvent(evt)
				processed++
			}
		}
	}
	return processed
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
