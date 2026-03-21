package meshtastic

import (
	"encoding/binary"
	"log/slog"
	"math"
	"time"

	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Dispatcher routes decoded Meshtastic packets to domain handlers.
type Dispatcher struct {
	hub            *ws.Hub
	nodeHandler    NodeHandler
	droneHandler   DroneHandler
	chatHandler    ChatHandler
	posHandler     PositionHandler
	onDeviceTime   func(t time.Time)
}

// NodeHandler processes node info and telemetry updates.
type NodeHandler interface {
	HandleNodeInfo(info *serial.NodeInfoLite)
	HandleTelemetry(from uint32, metrics *serial.DeviceMetrics)
	HandlePosition(from uint32, pos *serial.PositionData)
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
	return &Dispatcher{hub: hub}
}

// SetNodeHandler sets the node handler.
func (d *Dispatcher) SetNodeHandler(h NodeHandler) { d.nodeHandler = h }

// SetDroneHandler sets the drone handler.
func (d *Dispatcher) SetDroneHandler(h DroneHandler) { d.droneHandler = h }

// SetChatHandler sets the chat handler.
func (d *Dispatcher) SetChatHandler(h ChatHandler) { d.chatHandler = h }

// SetDeviceTimeCallback sets a callback invoked when a device time is received.
func (d *Dispatcher) SetDeviceTimeCallback(fn func(t time.Time)) { d.onDeviceTime = fn }

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
		}

	case serial.FromRadioNodeInfo:
		if pkt.NodeInfo != nil && d.nodeHandler != nil {
			d.nodeHandler.HandleNodeInfo(pkt.NodeInfo)
		}

	case serial.FromRadioMeshPacket:
		if pkt.MeshPacket != nil {
			d.handleMeshPacket(pkt.MeshPacket)
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

func (d *Dispatcher) handleMeshPacket(mp *serial.MeshPacketData) {
	portNum := PortNum(mp.PortNum)

	slog.Debug("mesh packet",
		"from", mp.From,
		"to", mp.To,
		"port", portNum.String(),
		"payloadLen", len(mp.Payload))

	switch portNum {
	case PortNumTextMessage:
		if d.chatHandler != nil && len(mp.Payload) > 0 {
			d.chatHandler.HandleTextMessage(mp.From, mp.To, mp.Channel, string(mp.Payload))
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
			}
		}

	case PortNumTelemetry:
		if d.nodeHandler != nil && len(mp.Payload) > 0 {
			metrics := decodeTelemetryPayload(mp.Payload)
			if metrics != nil {
				d.nodeHandler.HandleTelemetry(mp.From, metrics)
			}
		}

	case PortNumNodeInfo:
		// Node info in mesh packet (user info update)
		slog.Debug("nodeinfo mesh packet", "from", mp.From)

	case PortNumDetectionSensor:
		if d.droneHandler != nil {
			d.droneHandler.HandleDroneDetection(mp.From, mp.Payload)
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
func decodeTelemetryPayload(data []byte) *serial.DeviceMetrics {
	// Telemetry is a wrapper: field 2 = device_metrics (sub-message)
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
				return decodeDeviceMetricsPayload(subData)
			}
		} else {
			p = skipField(data, p, wireType)
			if p < 0 {
				break
			}
		}
	}
	return nil
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
