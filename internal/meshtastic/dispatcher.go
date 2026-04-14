package meshtastic

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// ahShortIDRe pulls the 2–5 char AntiHunter CONFIG_NODEID prefix out of the
// leading "AH01:" segment of a TEXTMSG body. The firmware emits this on every
// frame it transmits (heartbeats, STATUS, detections, ACKs). Intentionally
// strict: uppercase alphanumeric only, rejects names that collide with
// AntiHunter reserved keywords (ALL, DRONE, SCAN, etc.) so a message like
// "DRONE: ..." without a node-id prefix doesn't get misread as node "DRONE".
var ahShortIDRe = regexp.MustCompile(`^([A-Z0-9]{2,5}):\s`)

// ahShortIDReservedKeywords are AntiHunter message-type words that could
// syntactically match the prefix regex but aren't node ids.
var ahShortIDReservedKeywords = map[string]bool{
	"ALL":     true,
	"STATUS":  true,
	"DRONE":   true,
	"SCAN":    true,
	"TARGET":  true,
	"DEVICE":  true,
	"ATTACK":  true,
	"GPS":     true,
	"CAM":     true,
	"TAMPER":  true,
	"SETUP":   true,
	"ERASE":   true,
	"RTC":     true,
	"CONFIG":  true,
	"STOP":    true,
	"REBOOT":  true,
	"HB":      true,
	"BASE":    true,
	"RANDOM":  true,
	"DEAUTH":  true,
	"TRI":     true,
	"BATT":    true,
	"VIBR":    true,
	"ANOM":    true,
	"STARTUP": true,
	"NODE":    true,
}

func extractAHShortID(payload string) string {
	m := ahShortIDRe.FindStringSubmatch(strings.TrimLeft(payload, " \t"))
	if m == nil {
		return ""
	}
	if ahShortIDReservedKeywords[m[1]] {
		return ""
	}
	return m[1]
}

// isSensorData returns true if the text looks like AntiHunter DigiNode sensor output.
// The keyword set mirrors every message shape the AntiHunter firmware can emit over
// TEXTMSG — detections, heartbeats (normal + battery-saver), ACKs, triangulation,
// startup, tamper, anomaly, camera events, and the boot self-test ping. Without
// this breadth, a sensor that only emitted a heartbeat would get classified as a
// generic gotailme node until it happened to fire a detection.
func isSensorData(text string) bool {
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "STATUS:") ||
		strings.Contains(upper, "DRONE:") ||
		strings.Contains(upper, "TARGET:") ||
		strings.Contains(upper, "DEVICE:") ||
		strings.Contains(upper, "ATTACK:") ||
		strings.Contains(upper, "ANOMALY-") ||
		strings.Contains(upper, "VIBRATION:") ||
		strings.Contains(upper, "BASELINE_STATUS:") ||
		strings.Contains(upper, "TRIANGULATE_") ||
		strings.Contains(upper, "CAM:") ||
		strings.Contains(upper, "CAM_FACE:") ||
		strings.Contains(upper, "CAM_PLATE:") ||
		strings.Contains(upper, "HEARTBEAT:") ||
		strings.Contains(upper, "NODE_HB") ||
		strings.Contains(upper, "STARTUP:") ||
		strings.Contains(upper, "TAMPER_") ||
		strings.Contains(upper, "_ACK:") ||
		strings.Contains(upper, "_ACK ") ||
		strings.Contains(upper, "ANTIHUNTER") ||
		strings.Contains(upper, "T_D:") ||
		strings.Contains(upper, "T_F:") ||
		strings.Contains(upper, "T_C:") ||
		strings.Contains(upper, "_DONE:") ||
		strings.Contains(upper, "ERASE_") ||
		strings.Contains(upper, "IDENTITY:") ||
		strings.Contains(upper, "SETUP_MODE:") ||
		strings.Contains(upper, "BATTERY_SAVER") ||
		strings.Contains(upper, "RTC_SYNC:") ||
		strings.HasPrefix(upper, "@ALL ") ||
		// Normal heartbeat: "nodeId: Time:X Temp:Y GPS:lat,lon" — no single unique
		// keyword, but the Temp+GPS combo only appears on AntiHunter frames.
		(strings.Contains(upper, "TEMP:") && strings.Contains(upper, "GPS:"))
}

// Dispatcher routes decoded Meshtastic packets to domain handlers.
type Dispatcher struct {
	hub           *ws.Hub
	nodeHandler   NodeHandler
	droneHandler  DroneHandler
	chatHandler   ChatHandler
	posHandler    PositionHandler
	onDeviceTime  func(t time.Time)
	onAlertEval   func(ctx context.Context, evt alerts.DetectionEvent)
	onWebhookFire func(eventType string, payload interface{})
	dedup         map[uint64]time.Time // packet hash → last seen
	dedupMu       sync.Mutex
	localNodeSeen bool   // true after first NodeInfo from wantConfig
	localNodeNum  uint32 // our local Heltec's mesh node number
	serialMgr     *serial.Manager
}

// NodeHandler processes node info and telemetry updates.
type NodeHandler interface {
	HandleNodeInfo(info *serial.NodeInfoLite)
	HandleTelemetry(from uint32, metrics *serial.DeviceMetrics)
	HandlePosition(from uint32, pos *serial.PositionData)
	HandleEnvironment(from uint32, env *serial.EnvironmentMetrics)
	// TouchNode ensures a node entry exists for the given mesh number.
	// Called on every incoming mesh packet so remote nodes appear in the list.
	TouchNode(nodeNum uint32, rxSNR float32, rxRSSI int32)
	// ClassifyNode tags a node as "gotailme" (C2 gateway) or "antihunter" (sensor).
	ClassifyNode(nodeNum uint32, nodeType string)
	// MarkLocal flags a node as the local C2 gateway.
	MarkLocal(nodeNum uint32)
	// SetLastMessage stores the most recent sensor TEXTMSG body so the UI can
	// show what that node last reported without polling alerts separately.
	SetLastMessage(nodeNum uint32, msg string)
	// SetAHShortID records the AntiHunter CONFIG_NODEID short id extracted
	// from the leading "AH01:" prefix in sensor TEXTMSG frames. Commands must
	// target this string (@AH01) — Meshtastic short-names are ignored by the
	// sensor dispatcher.
	SetAHShortID(nodeNum uint32, shortID string)
}

// DroneHandler processes drone detection events.
type DroneHandler interface {
	HandleDroneDetection(from uint32, payload []byte)
}

// ChatHandler processes text messages.
type ChatHandler interface {
	HandleTextMessage(from, to uint32, channel uint32, text string)
}

// PositionHandler processes position updates.
type PositionHandler interface {
	HandlePosition(from uint32, pos *serial.PositionData)
}

// NewDispatcher creates a new packet dispatcher.
func NewDispatcher(hub *ws.Hub) *Dispatcher {
	return &Dispatcher{
		hub:   hub,
		dedup: make(map[uint64]time.Time),
	}
}

// SetSerialManager sets the serial manager for config storage.
func (d *Dispatcher) SetSerialManager(m *serial.Manager) { d.serialMgr = m }

// SetNodeHandler sets the node handler.
func (d *Dispatcher) SetNodeHandler(h NodeHandler) { d.nodeHandler = h }

// SetDroneHandler sets the drone handler.
func (d *Dispatcher) SetDroneHandler(h DroneHandler) { d.droneHandler = h }

// SetChatHandler sets the chat handler.
func (d *Dispatcher) SetChatHandler(h ChatHandler) { d.chatHandler = h }

// SetDeviceTimeCallback sets a callback invoked when a device time is received.
func (d *Dispatcher) SetDeviceTimeCallback(fn func(t time.Time)) { d.onDeviceTime = fn }

// SetAlertCallback sets a callback invoked to evaluate detection events against alert rules.
func (d *Dispatcher) SetAlertCallback(fn func(ctx context.Context, evt alerts.DetectionEvent)) {
	d.onAlertEval = fn
}

// SetWebhookCallback sets a callback invoked to dispatch webhook events.
func (d *Dispatcher) SetWebhookCallback(fn func(eventType string, payload interface{})) {
	d.onWebhookFire = fn
}

// HandlePacket is the main entry point, called by the serial manager for each FromRadio.
func (d *Dispatcher) HandlePacket(pkt *serial.FromRadioPacket) {
	switch pkt.Type {
	case serial.FromRadioMyInfo:
		if pkt.MyInfo != nil {
			slog.Info("radio connected",
				"nodeNum", pkt.MyInfo.MyNodeNum,
				"maxChannels", pkt.MyInfo.MaxChannels)
			// The radio just responded, so "now" is a valid device time
			if d.onDeviceTime != nil {
				d.onDeviceTime(time.Now())
			}
			// Mark the local node as a gotailme C2 gateway
			if d.nodeHandler != nil && pkt.MyInfo.MyNodeNum != 0 {
				d.nodeHandler.TouchNode(pkt.MyInfo.MyNodeNum, 0, 0)
				d.nodeHandler.ClassifyNode(pkt.MyInfo.MyNodeNum, "gotailme")
			}
		}

	case serial.FromRadioNodeInfo:
		if pkt.NodeInfo != nil && d.nodeHandler != nil {
			d.nodeHandler.HandleNodeInfo(pkt.NodeInfo)
			// The first NodeInfo in the wantConfig dump is the local node.
			// Mark it as our own gotailme C2 gateway.
			if !d.localNodeSeen && pkt.NodeInfo.Num != 0 {
				d.localNodeSeen = true
				d.localNodeNum = pkt.NodeInfo.Num
				d.nodeHandler.MarkLocal(pkt.NodeInfo.Num)
			}
		}

	case serial.FromRadioMeshPacket:
		if pkt.MeshPacket != nil {
			d.handleMeshPacket(pkt.MeshPacket)
		}

	case serial.FromRadioConfig:
		if pkt.Config != nil && d.serialMgr != nil {
			d.serialMgr.StoreConfig(pkt.Config)
		}

	case serial.FromRadioConfigComplete:
		slog.Info("radio config complete")

	case serial.FromRadioRebooted:
		slog.Warn("radio rebooted")

	case serial.FromRadioMetadata:
		if pkt.Metadata != nil {
			slog.Info("radio metadata",
				"firmware", pkt.Metadata.FirmwareVersion,
				"bluetooth", pkt.Metadata.HasBluetooth)
		}
	}
}

// isDuplicate returns true if this packet was already seen within the dedup window.
// Meshtastic mesh networks rebroadcast packets, so duplicates must be filtered.
func (d *Dispatcher) isDuplicate(mp *serial.MeshPacketData) bool {
	// Hash based on from + id (packet ID is unique per sender)
	key := uint64(mp.From)<<32 | uint64(mp.ID)

	d.dedupMu.Lock()
	defer d.dedupMu.Unlock()

	now := time.Now()
	if last, ok := d.dedup[key]; ok && now.Sub(last) < 15*time.Second {
		return true // duplicate within 15s window
	}
	d.dedup[key] = now

	// Prune old entries (keep max 512)
	if len(d.dedup) > 512 {
		cutoff := now.Add(-15 * time.Second)
		for k, t := range d.dedup {
			if t.Before(cutoff) {
				delete(d.dedup, k)
			}
		}
	}

	return false
}

func (d *Dispatcher) handleMeshPacket(mp *serial.MeshPacketData) {
	if d.isDuplicate(mp) {
		slog.Debug("duplicate packet filtered", "from", mp.From, "id", mp.ID)
		return
	}

	portNum := PortNum(mp.PortNum)

	slog.Debug("mesh packet",
		"from", mp.From,
		"to", mp.To,
		"port", portNum.String(),
		"payloadLen", len(mp.Payload))

	// Register / touch the sending node so it appears in the node list.
	// Every mesh packet tells us a node exists, even if we don't have its full info yet.
	if d.nodeHandler != nil && mp.From != 0 {
		d.nodeHandler.TouchNode(mp.From, mp.RxSNR, mp.RxRSSI)

		// Classify node type based on message content.
		// AntiHunter sensor nodes send STATUS:/DRONE:/TARGET:/DEVICE:/ATTACK: lines.
		// Other C2 gateways (gotailme) send plain text messages or relay commands.
		if portNum == PortNumTextMessage && len(mp.Payload) > 0 {
			text := string(mp.Payload)
			if isSensorData(text) {
				d.nodeHandler.ClassifyNode(mp.From, "antihunter")
			} else {
				d.nodeHandler.ClassifyNode(mp.From, "gotailme")
			}
		} else if portNum == PortNumDetectionSensor {
			d.nodeHandler.ClassifyNode(mp.From, "antihunter")
		}
	}

	switch portNum {
	case PortNumTextMessage:
		if d.chatHandler != nil && len(mp.Payload) > 0 {
			d.chatHandler.HandleTextMessage(mp.From, mp.To, mp.Channel, string(mp.Payload))
		}
		if d.onWebhookFire != nil && len(mp.Payload) > 0 {
			d.onWebhookFire("mesh.text_message", map[string]interface{}{
				"from":    mp.From,
				"to":      mp.To,
				"channel": mp.Channel,
				"text":    string(mp.Payload),
			})
		}
		// AntiHunter sensors transmit their detection and heartbeat frames as plain
		// ASCII inside TEXTMSG packets (they have no protobuf Position/Telemetry
		// stack). Run the payload through the serial text parser so embedded GPS
		// fixes, target hits, drone telemetry, and triangulation frames reach the
		// same pipeline as locally-parsed console output.
		if d.serialMgr != nil && len(mp.Payload) > 0 {
			payload := string(mp.Payload)
			d.serialMgr.ProcessMeshText(mp.From, mp.To, mp.Channel, payload)
			// Stash the last sensor line on the node so the expanded UI row shows
			// what the node is currently doing. Only do this for recognizably
			// AntiHunter output to avoid overwriting a node's row with arbitrary
			// user chat.
			if d.nodeHandler != nil && isSensorData(payload) {
				d.nodeHandler.SetLastMessage(mp.From, payload)
				// Extract the AntiHunter CONFIG_NODEID prefix ("AH01:" in
				// "AH01: STATUS: ..."). This is what the remote dispatcher
				// matches against the @TARGET in outbound commands, so it
				// must be surfaced on the node record for the UI to target.
				if shortID := extractAHShortID(payload); shortID != "" {
					d.nodeHandler.SetAHShortID(mp.From, shortID)
				}
			}
		}

	case PortNumPosition:
		if d.nodeHandler != nil && len(mp.Payload) > 0 {
			pos := decodePositionPayload(mp.Payload)
			if pos != nil {
				d.nodeHandler.HandlePosition(mp.From, pos)
				// Update device time from GPS-synced position
				if pos.Time > 0 && d.onDeviceTime != nil {
					d.onDeviceTime(time.Unix(int64(pos.Time), 0))
				}
				if d.onWebhookFire != nil {
					d.onWebhookFire("mesh.position", map[string]interface{}{
						"from":      mp.From,
						"latitude":  pos.LatitudeI,
						"longitude": pos.LongitudeI,
						"altitude":  pos.Altitude,
						"time":      pos.Time,
					})
				}
			}
		}

	case PortNumTelemetry:
		if d.nodeHandler != nil && len(mp.Payload) > 0 {
			dm, em := decodeTelemetryPayload(mp.Payload)
			if dm != nil {
				d.nodeHandler.HandleTelemetry(mp.From, dm)
			}
			if em != nil {
				d.nodeHandler.HandleEnvironment(mp.From, em)
			}
		}

	case PortNumNodeInfo:
		// Node info in mesh packet (user info update)
		slog.Debug("nodeinfo mesh packet", "from", mp.From)

	case PortNumDetectionSensor:
		if d.droneHandler != nil {
			d.droneHandler.HandleDroneDetection(mp.From, mp.Payload)
		}
		if d.onAlertEval != nil {
			d.onAlertEval(context.Background(), alerts.DetectionEvent{
				NodeID: fmt.Sprintf("!%08x", mp.From),
			})
		}
		if d.onWebhookFire != nil {
			d.onWebhookFire("mesh.drone_detection", map[string]interface{}{
				"from":       mp.From,
				"payloadLen": len(mp.Payload),
			})
		}

	case PortNumRouting:
		// Routing ACKs, error codes
		slog.Debug("routing packet", "from", mp.From)

	case PortNumTraceroute:
		slog.Debug("traceroute packet", "from", mp.From)

	default:
		slog.Debug("unhandled port", "port", portNum.String(), "from", mp.From)
	}
}

// decodePositionPayload decodes a Position protobuf payload.
func decodePositionPayload(data []byte) *serial.PositionData {
	pos := &serial.PositionData{}
	p := 0
	for p < len(data) {
		tag, n := decodeVarint(data[p:])
		if n == 0 {
			break
		}
		p += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 5 { // fixed32
			if p+4 > len(data) {
				break
			}
			val := int32(binary.LittleEndian.Uint32(data[p : p+4]))
			p += 4
			switch fieldNum {
			case 1:
				pos.LatitudeI = val
			case 2:
				pos.LongitudeI = val
			case 3:
				pos.Altitude = val
			}
		} else if wireType == 0 {
			val, n := decodeVarint(data[p:])
			if n == 0 {
				break
			}
			p += n
			switch fieldNum {
			case 4:
				pos.Time = uint32(val)
			case 10:
				pos.Sats = uint32(val)
			}
		} else {
			p = skipField(data, p, wireType)
			if p < 0 {
				break
			}
		}
	}
	return pos
}

// decodeTelemetryPayload decodes a Telemetry protobuf payload.
// Returns device_metrics (field 2) and environment_metrics (field 3).
func decodeTelemetryPayload(data []byte) (*serial.DeviceMetrics, *serial.EnvironmentMetrics) {
	var dm *serial.DeviceMetrics
	var em *serial.EnvironmentMetrics
	p := 0
	for p < len(data) {
		tag, n := decodeVarint(data[p:])
		if n == 0 {
			break
		}
		p += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 2 { // length-delimited
			length, n := decodeVarint(data[p:])
			if n == 0 {
				break
			}
			p += n
			if p+int(length) > len(data) {
				break
			}
			subData := data[p : p+int(length)]
			p += int(length)

			if fieldNum == 2 { // device_metrics
				dm = decodeDeviceMetricsPayload(subData)
			} else if fieldNum == 3 { // environment_metrics
				em = decodeEnvironmentMetricsPayload(subData)
			}
		} else {
			p = skipField(data, p, wireType)
			if p < 0 {
				break
			}
		}
	}
	return dm, em
}

func decodeDeviceMetricsPayload(data []byte) *serial.DeviceMetrics {
	dm := &serial.DeviceMetrics{}
	p := 0
	for p < len(data) {
		tag, n := decodeVarint(data[p:])
		if n == 0 {
			break
		}
		p += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 0 {
			val, n := decodeVarint(data[p:])
			if n == 0 {
				break
			}
			p += n
			switch fieldNum {
			case 1:
				dm.BatteryLevel = uint32(val)
			case 5:
				dm.UptimeSeconds = uint32(val)
			}
		} else if wireType == 5 {
			if p+4 > len(data) {
				break
			}
			bits := binary.LittleEndian.Uint32(data[p : p+4])
			f := math.Float32frombits(bits)
			p += 4
			switch fieldNum {
			case 2:
				dm.Voltage = f
			case 3:
				dm.ChannelUtilization = f
			case 4:
				dm.AirUtilTx = f
			}
		} else {
			p = skipField(data, p, wireType)
			if p < 0 {
				break
			}
		}
	}
	return dm
}

func decodeEnvironmentMetricsPayload(data []byte) *serial.EnvironmentMetrics {
	em := &serial.EnvironmentMetrics{}
	p := 0
	for p < len(data) {
		tag, n := decodeVarint(data[p:])
		if n == 0 {
			break
		}
		p += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 5 { // fixed32 (float)
			if p+4 > len(data) {
				break
			}
			bits := binary.LittleEndian.Uint32(data[p : p+4])
			f := math.Float32frombits(bits)
			p += 4
			switch fieldNum {
			case 1:
				em.Temperature = f
			case 2:
				em.RelativeHumidity = f
			case 3:
				em.BarometricPressure = f
			}
		} else {
			p = skipField(data, p, wireType)
			if p < 0 {
				break
			}
		}
	}
	return em
}

// Protobuf helpers (duplicated from serial for package independence)
func decodeVarint(data []byte) (uint64, int) {
	var val uint64
	var shift uint
	for i, b := range data {
		if i >= 10 {
			return 0, 0
		}
		val |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return val, i + 1
		}
		shift += 7
	}
	return 0, 0
}

func skipField(data []byte, pos int, wireType uint64) int {
	switch wireType {
	case 0:
		for pos < len(data) {
			if data[pos]&0x80 == 0 {
				return pos + 1
			}
			pos++
		}
		return -1
	case 1:
		return pos + 8
	case 2:
		length, n := decodeVarint(data[pos:])
		if n == 0 {
			return -1
		}
		return pos + n + int(length)
	case 5:
		return pos + 4
	default:
		return -1
	}
}
