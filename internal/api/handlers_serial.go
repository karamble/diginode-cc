package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/karamble/diginode-cc/internal/serial"
	goserial "go.bug.st/serial"
)

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

	latI := int32(req.Latitude * 1e7)
	lonI := int32(req.Longitude * 1e7)
	alt := int32(0)
	if req.Altitude != nil {
		alt = int32(*req.Altitude)
	}

	data := serial.BuildPosition(latI, lonI, alt)
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

	data := serial.BuildAdminDisplayConfig(req.ScreenOnSecs)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "display config sent"})
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

	data := serial.BuildAdminShutdown(secs)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "shutdown sent"})
}

func (s *Server) handleSerialSimulate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string  `json:"type"`    // "text", "position", "telemetry", "drone"
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

	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required (text, position, telemetry)")
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

	data := serial.BuildAdminBluetoothConfig(req.Enabled, req.Mode, req.FixedPin)
	if err := s.serialMgr.SendToRadio(data); err != nil {
		writeError(w, http.StatusInternalServerError, "send failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "bluetooth config sent"})
}
