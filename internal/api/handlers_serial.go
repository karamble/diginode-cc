package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/karamble/diginode-cc/internal/serial"
	goserial "go.bug.st/serial"
)

// localNodeNum returns the local Heltec node number, or writes a 503 error and returns 0.
func (s *Server) localNodeNum(w http.ResponseWriter) uint32 {
	num := s.svc.Nodes.GetLocalNodeNum()
	if num == 0 {
		writeError(w, http.StatusServiceUnavailable, "local node number not yet known")
	}
	return num
}

// handleGetTextMessages returns text messages since a given sequence number.
// This is polled by gotailme for inter-system messaging (CC PRO compat).
func (s *Server) handleGetTextMessages(w http.ResponseWriter, r *http.Request) {
	sinceSeq := int64(0)
	if q := r.URL.Query().Get("sinceSeq"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			sinceSeq = v
		}
	}

	messages := s.serialMgr.GetTextMessages(sinceSeq)
	if messages == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleGetDeviceTime(w http.ResponseWriter, r *http.Request) {
	deviceTime, hasTime := s.serialMgr.GetDeviceTime()
	var ageSeconds float64
	var deviceTimeUnix int64
	if hasTime {
		ageSeconds = time.Since(deviceTime).Seconds()
		deviceTimeUnix = deviceTime.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hasTime":    hasTime,
		"deviceTime": deviceTimeUnix,
		"ageSeconds": ageSeconds,
	})
}

func (s *Server) handleGetSerialConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"devicePath": s.cfg.SerialDevice,
		"baud":       s.cfg.SerialBaud,
		"enabled":    s.cfg.SerialDevice != "",
	})
}

func (s *Server) handleUpdateSerialConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DevicePath string `json:"devicePath"`
		Baud       int    `json:"baud"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.DevicePath != "" {
		s.cfg.SerialDevice = req.DevicePath
	}
	if req.Baud > 0 {
		s.cfg.SerialBaud = req.Baud
	}

	slog.Info("serial config updated (takes effect on next restart)",
		"device", s.cfg.SerialDevice, "baud", s.cfg.SerialBaud)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"devicePath": s.cfg.SerialDevice,
		"baud":       s.cfg.SerialBaud,
		"message":    "config updated, restart serial connection to apply",
	})
}

func (s *Server) handleSerialConnect(w http.ResponseWriter, r *http.Request) {
	if s.serialMgr.IsConnected() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already connected"})
		return
	}

	go func() {
		if err := s.serialMgr.Start(); err != nil {
			slog.Error("serial start failed", "error", err)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "connecting"})
}

func (s *Server) handleSerialDisconnect(w http.ResponseWriter, r *http.Request) {
	s.serialMgr.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

func (s *Server) handleRefreshNodes(w http.ResponseWriter, r *http.Request) {
	if err := s.serialMgr.RefreshConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "refresh failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSendSerialTextMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string  `json:"message"`
		To      *uint32 `json:"to,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	to := uint32(serial.BroadcastAddr)
	if req.To != nil {
		to = *req.To
	}

	data := serial.BuildTextMessage(to, req.Message)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	// Persist to chat DB, broadcast via WebSocket, and store in ring buffer
	s.serialMgr.AddTextMessage("local", req.Message, "")
	s.svc.Chat.PersistAndBroadcast(0, to, 0, req.Message)

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) handleSendSerialTextAlert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	data := serial.BuildTextMessage(serial.BroadcastAddr, req.Message)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	// Persist to chat DB, broadcast via WebSocket, and store in ring buffer
	s.serialMgr.AddTextMessage("local", req.Message, "")
	s.svc.Chat.PersistAndBroadcast(0, serial.BroadcastAddr, 0, req.Message)

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) handleSendSerialPosition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Latitude  float64  `json:"latitude"`
		Longitude float64  `json:"longitude"`
		Altitude  *float64 `json:"altitude,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	nodeNum := s.localNodeNum(w)
	if nodeNum == 0 {
		return
	}

	latI := int32(req.Latitude * 1e7)
	lonI := int32(req.Longitude * 1e7)
	alt := int32(0)
	if req.Altitude != nil {
		alt = int32(*req.Altitude)
	}
	timestamp := uint32(time.Now().Unix())

	data := serial.BuildAdminSetFixedPosition(nodeNum, latI, lonI, alt, timestamp)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) handleSendSerialDeviceMetrics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatteryLevel uint32   `json:"batteryLevel"`
		Voltage      *float64 `json:"voltage,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	voltage := float32(0)
	if req.Voltage != nil {
		voltage = float32(*req.Voltage)
	}

	data := serial.BuildDeviceMetrics(req.BatteryLevel, voltage)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) handleSendSerialDisplayConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScreenOnSecs uint32 `json:"screenOnSecs"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.ScreenOnSecs == 0 {
		req.ScreenOnSecs = 60 // default 60 seconds
	}

	nodeNum := s.localNodeNum(w)
	if nodeNum == 0 {
		return
	}

	data := serial.BuildAdminDisplayConfig(nodeNum, req.ScreenOnSecs)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "display config sent"})
}

func (s *Server) handleSendSerialNodedbReset(w http.ResponseWriter, r *http.Request) {
	nodeNum := s.localNodeNum(w)
	if nodeNum == 0 {
		return
	}

	data := serial.BuildAdminNodedbReset(nodeNum)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "nodedb reset sent"})
}

func (s *Server) handleSendSerialShutdown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds         uint32 `json:"seconds"`
		ShutdownSeconds uint32 `json:"shutdownSeconds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	secs := req.Seconds
	if req.ShutdownSeconds > 0 {
		secs = req.ShutdownSeconds
	}
	if secs == 0 {
		secs = 5 // default 5-second delay
	}

	nodeNum := s.localNodeNum(w)
	if nodeNum == 0 {
		return
	}

	data := serial.BuildAdminShutdown(nodeNum, secs)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "shutdown sent"})
}

func (s *Server) handleSerialSimulate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Lines mode (CC PRO compatible): raw text lines fed through the text parser.
		// Used by drone simulators and dev tools.
		Lines []string `json:"lines,omitempty"`

		// Packet mode: build a synthetic FromRadio packet.
		Type    string  `json:"type,omitempty"` // "text", "position"
		From    uint32  `json:"from"`
		To      uint32  `json:"to"`
		Text    string  `json:"text,omitempty"`
		Lat     float64 `json:"lat,omitempty"`
		Lon     float64 `json:"lon,omitempty"`
		Alt     int32   `json:"alt,omitempty"`
		Battery uint32  `json:"battery,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Lines mode: feed raw text through the text parser (CC PRO compatible)
	if len(req.Lines) > 0 {
		processed := s.serialMgr.SimulateLines(req.Lines)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "simulated",
			"processed": processed,
		})
		return
	}

	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required (text, position) or provide lines[]")
		return
	}

	// Build a fake FromRadio packet and dispatch it through the serial manager's handlers
	var pkt serial.FromRadioPacket
	switch req.Type {
	case "text":
		pkt.Type = serial.FromRadioMeshPacket
		pkt.MeshPacket = &serial.MeshPacketData{
			From:    req.From,
			To:      req.To,
			PortNum: 1, // TEXT_MESSAGE_APP
			Payload: []byte(req.Text),
		}
	case "position":
		pkt.Type = serial.FromRadioMeshPacket
		pkt.MeshPacket = &serial.MeshPacketData{
			From:    req.From,
			To:      req.To,
			PortNum: 3, // POSITION_APP
		}
		// Build a position protobuf payload
		var posData []byte
		posData = append(posData, serial.EncodeSFixed32Field(1, int32(req.Lat*1e7))...)
		posData = append(posData, serial.EncodeSFixed32Field(2, int32(req.Lon*1e7))...)
		if req.Alt != 0 {
			posData = append(posData, serial.EncodeVarintField(3, uint64(req.Alt))...)
		}
		pkt.MeshPacket.Payload = posData
	default:
		writeError(w, http.StatusBadRequest, "unsupported type: "+req.Type+", supported: text, position")
		return
	}

	s.serialMgr.SimulatePacket(&pkt)

	writeJSON(w, http.StatusOK, map[string]string{"status": "simulated"})
}

// handleListSerialProtocols returns the supported serial protocol names.
func (s *Server) handleListSerialProtocols(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []string{"meshtastic-binary", "meshtastic-rewrite", "raw-lines"})
}

// handleListSerialPorts returns the available serial ports on the system.
func (s *Server) handleListSerialPorts(w http.ResponseWriter, r *http.Request) {
	ports, err := goserial.GetPortsList()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list ports: "+err.Error())
		return
	}
	if ports == nil {
		ports = []string{}
	}
	writeJSON(w, http.StatusOK, ports)
}

// handleResetSerialConfig resets serial configuration to defaults.
func (s *Server) handleResetSerialConfig(w http.ResponseWriter, r *http.Request) {
	s.cfg.SerialDevice = ""
	s.cfg.SerialBaud = 115200

	slog.Info("serial config reset to defaults")

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "serial config reset to defaults",
	})
}

func (s *Server) handleSendSerialBluetoothConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Mode     uint32 `json:"mode"`
		FixedPin uint32 `json:"fixedPin"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	nodeNum := s.localNodeNum(w)
	if nodeNum == 0 {
		return
	}

	data := serial.BuildAdminBluetoothConfig(nodeNum, req.Enabled, req.Mode, req.FixedPin)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	// BLE config changes require a device reboot to take effect.
	// Send a reboot with a short delay so the config write completes first.
	rebootData := serial.BuildAdminReboot(nodeNum, 5)
	if err := s.serialMgr.SendToRadio(rebootData); err != nil {
		slog.Warn("bluetooth config sent but reboot failed", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "bluetooth config sent, reboot in 5s"})
}

// handleGetRadioConfig returns the stored Meshtastic radio config sections.
func (s *Server) handleGetRadioConfig(w http.ResponseWriter, r *http.Request) {
	configs := s.serialMgr.GetRadioConfig()
	result := map[string]any{}
	for section, cfg := range configs {
		switch section {
		case "bluetooth":
			if cfg.Bluetooth != nil {
				result[section] = cfg.Bluetooth
			}
		case "position":
			if cfg.Position != nil {
				result[section] = cfg.Position
			}
		case "display":
			if cfg.Display != nil {
				result[section] = cfg.Display
			}
		case "lora":
			if cfg.LoRa != nil {
				result[section] = cfg.LoRa
			}
		case "power":
			if cfg.Power != nil {
				result[section] = cfg.Power
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// handleWakeDevice toggles DTR/RTS on the serial port to hardware-reset the
// Heltec out of deep sleep (after an admin shutdown command).
func (s *Server) handleWakeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.serialMgr.WakeDevice(); err != nil {
		writeError(w, http.StatusInternalServerError, "wake failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "wake signal sent"})
}
