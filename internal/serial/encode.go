package serial

import (
	"encoding/binary"
	"math"
)

// Meshtastic portnum constants (from meshtastic.portnums.proto)
const (
	PortNumTextMessage = 1
	PortNumPosition    = 3
	PortNumAdmin       = 6
	PortNumTelemetry   = 67
)

// Broadcast address for Meshtastic mesh.
const BroadcastAddr = 0xFFFFFFFF

// --- Low-level protobuf encoding helpers ---

// encodeFixed32 encodes a uint32 as 4 little-endian bytes.
func encodeFixed32(val uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, val)
	return b
}

// encodeLengthDelimited encodes a length-delimited protobuf field (wire type 2).
func encodeLengthDelimited(fieldNum uint64, data []byte) []byte {
	tag := encodeVarint((fieldNum << 3) | 2)
	length := encodeVarint(uint64(len(data)))
	out := make([]byte, 0, len(tag)+len(length)+len(data))
	out = append(out, tag...)
	out = append(out, length...)
	out = append(out, data...)
	return out
}

// encodeVarintField encodes a varint protobuf field (wire type 0).
func encodeVarintField(fieldNum, val uint64) []byte {
	tag := encodeVarint((fieldNum << 3) | 0)
	v := encodeVarint(val)
	out := make([]byte, 0, len(tag)+len(v))
	out = append(out, tag...)
	out = append(out, v...)
	return out
}

// encodeFixed32Field encodes a fixed32 protobuf field (wire type 5).
func encodeFixed32Field(fieldNum uint64, val uint32) []byte {
	tag := encodeVarint((fieldNum << 3) | 5)
	b := encodeFixed32(val)
	out := make([]byte, 0, len(tag)+4)
	out = append(out, tag...)
	out = append(out, b...)
	return out
}

// encodeSFixed32Field encodes a signed fixed32 (sfixed32) protobuf field (wire type 5).
func encodeSFixed32Field(fieldNum uint64, val int32) []byte {
	return encodeFixed32Field(fieldNum, uint32(val))
}

// encodeFloat32Field encodes a float protobuf field (wire type 5, IEEE 754).
func encodeFloat32Field(fieldNum uint64, val float32) []byte {
	return encodeFixed32Field(fieldNum, math.Float32bits(val))
}

// --- Protobuf sub-message builders ---

// buildDataMessage builds a MeshPacket.Data sub-message.
//   Data: field 1 = portnum (varint), field 2 = payload (bytes)
func buildDataMessage(portNum uint32, payload []byte) []byte {
	var out []byte
	out = append(out, encodeVarintField(1, uint64(portNum))...)
	out = append(out, encodeLengthDelimited(2, payload)...)
	return out
}

// buildMeshPacket builds a MeshPacket sub-message.
//   MeshPacket: field 1 = to (varint), field 4 = decoded (Data, length-delimited)
func buildMeshPacket(to uint32, data []byte) []byte {
	var out []byte
	out = append(out, encodeVarintField(1, uint64(to))...)
	out = append(out, encodeLengthDelimited(4, data)...)
	return out
}

// buildToRadio wraps a MeshPacket in a ToRadio message.
//   ToRadio: field 2 = packet (MeshPacket, length-delimited)
func buildToRadio(meshPacket []byte) []byte {
	return encodeLengthDelimited(2, meshPacket)
}

// --- Public packet builders ---
// Each returns a complete ToRadio protobuf (NOT framed — caller must use EncodeFrame).

// BuildTextMessage builds a ToRadio containing a text message to the given address.
// Use BroadcastAddr (0xFFFFFFFF) for broadcast.
func BuildTextMessage(to uint32, text string) []byte {
	payload := []byte(text)
	data := buildDataMessage(PortNumTextMessage, payload)
	mp := buildMeshPacket(to, data)
	return buildToRadio(mp)
}

// BuildPosition builds a ToRadio containing a position report (broadcast).
//   Position proto: field 1 = latitude_i (sfixed32), field 2 = longitude_i (sfixed32),
//                   field 3 = altitude (int32, varint)
func BuildPosition(latI, lonI int32, altitude int32) []byte {
	var pos []byte
	pos = append(pos, encodeSFixed32Field(1, latI)...)
	pos = append(pos, encodeSFixed32Field(2, lonI)...)
	if altitude != 0 {
		pos = append(pos, encodeVarintField(3, uint64(altitude))...)
	}

	data := buildDataMessage(PortNumPosition, pos)
	mp := buildMeshPacket(BroadcastAddr, data)
	return buildToRadio(mp)
}

// BuildDeviceMetrics builds a ToRadio containing device telemetry (broadcast).
//   Telemetry proto: field 2 = device_metrics (sub-message)
//   DeviceMetrics: field 1 = battery_level (varint), field 2 = voltage (float/fixed32)
func BuildDeviceMetrics(batteryLevel uint32, voltage float32) []byte {
	var dm []byte
	if batteryLevel > 0 {
		dm = append(dm, encodeVarintField(1, uint64(batteryLevel))...)
	}
	if voltage > 0 {
		dm = append(dm, encodeFloat32Field(2, voltage)...)
	}

	// Telemetry field 2 = device_metrics
	telemetry := encodeLengthDelimited(2, dm)

	data := buildDataMessage(PortNumTelemetry, telemetry)
	mp := buildMeshPacket(BroadcastAddr, data)
	return buildToRadio(mp)
}

// BuildAdminShutdown builds a ToRadio containing an admin shutdown command (broadcast).
//   AdminMessage: field 16 = shutdown_seconds (varint)
func BuildAdminShutdown(seconds uint32) []byte {
	admin := encodeVarintField(16, uint64(seconds))

	data := buildDataMessage(PortNumAdmin, admin)
	mp := buildMeshPacket(BroadcastAddr, data)
	return buildToRadio(mp)
}

// BuildAdminDisplayConfig builds a ToRadio containing an admin command to set the screen-on duration.
//
//	AdminMessage: field 3 = set_config (Config, length-delimited)
//	Config: field 7 = display (DisplayConfig, length-delimited)
//	DisplayConfig: field 4 = screen_on_secs (varint)
func BuildAdminDisplayConfig(screenOnSecs uint32) []byte {
	// DisplayConfig: field 4 = screen_on_secs
	displayCfg := encodeVarintField(4, uint64(screenOnSecs))
	// Config: field 7 = display
	config := encodeLengthDelimited(7, displayCfg)
	// AdminMessage: field 3 = set_config
	admin := encodeLengthDelimited(3, config)

	data := buildDataMessage(PortNumAdmin, admin)
	mp := buildMeshPacket(BroadcastAddr, data)
	return buildToRadio(mp)
}

// BuildAdminBluetoothConfig builds a ToRadio containing an admin command to set Bluetooth config.
//   AdminMessage: field 3 = set_config (Config, length-delimited)
//   Config: field 8 = bluetooth (BluetoothConfig, length-delimited)
//   BluetoothConfig: field 1 = enabled (bool/varint), field 2 = mode (varint), field 3 = fixed_pin (varint)
func BuildAdminBluetoothConfig(enabled bool, mode uint32, fixedPin uint32) []byte {
	var btCfg []byte
	enabledVal := uint64(0)
	if enabled {
		enabledVal = 1
	}
	btCfg = append(btCfg, encodeVarintField(1, enabledVal)...)
	btCfg = append(btCfg, encodeVarintField(2, uint64(mode))...)
	if fixedPin > 0 {
		btCfg = append(btCfg, encodeVarintField(3, uint64(fixedPin))...)
	}
	// Config: field 8 = bluetooth
	config := encodeLengthDelimited(8, btCfg)
	// AdminMessage: field 3 = set_config
	admin := encodeLengthDelimited(3, config)

	data := buildDataMessage(PortNumAdmin, admin)
	mp := buildMeshPacket(BroadcastAddr, data)
	return buildToRadio(mp)
}

// EncodeSFixed32Field is an exported wrapper for building protobuf sfixed32 fields.
func EncodeSFixed32Field(fieldNum uint64, val int32) []byte {
	return encodeSFixed32Field(fieldNum, val)
}

// EncodeVarintField is an exported wrapper for building protobuf varint fields.
func EncodeVarintField(fieldNum, val uint64) []byte {
	return encodeVarintField(fieldNum, val)
}
