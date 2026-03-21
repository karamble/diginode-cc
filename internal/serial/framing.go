package serial

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Meshtastic serial framing constants
// Protocol: [START1][START2][MSB_LEN][LSB_LEN][PROTOBUF_DATA...]
const (
	frameStart1 byte = 0x94
	frameStart2 byte = 0xC3
	maxFrameLen      = 512
)

var (
	ErrNotConnected = errors.New("serial port not connected")
	ErrFrameTooLong = errors.New("frame exceeds maximum length")
)

// FrameDecoder accumulates bytes and extracts complete Meshtastic frames.
type FrameDecoder struct {
	buf   []byte
	state frameState
	payloadLen int
}

type frameState int

const (
	stateWaitStart1 frameState = iota
	stateWaitStart2
	stateWaitLenMSB
	stateWaitLenLSB
	stateReadPayload
)

// NewFrameDecoder creates a new frame decoder.
func NewFrameDecoder() *FrameDecoder {
	return &FrameDecoder{
		buf:   make([]byte, 0, maxFrameLen),
		state: stateWaitStart1,
	}
}

// Feed processes incoming bytes and returns any complete frames found.
func (d *FrameDecoder) Feed(data []byte) [][]byte {
	var frames [][]byte

	for _, b := range data {
		switch d.state {
		case stateWaitStart1:
			if b == frameStart1 {
				d.state = stateWaitStart2
			}

		case stateWaitStart2:
			if b == frameStart2 {
				d.state = stateWaitLenMSB
				d.buf = d.buf[:0]
			} else {
				d.state = stateWaitStart1
			}

		case stateWaitLenMSB:
			d.payloadLen = int(b) << 8
			d.state = stateWaitLenLSB

		case stateWaitLenLSB:
			d.payloadLen |= int(b)
			if d.payloadLen > maxFrameLen || d.payloadLen == 0 {
				d.state = stateWaitStart1
			} else {
				d.state = stateReadPayload
			}

		case stateReadPayload:
			d.buf = append(d.buf, b)
			if len(d.buf) >= d.payloadLen {
				frame := make([]byte, d.payloadLen)
				copy(frame, d.buf[:d.payloadLen])
				frames = append(frames, frame)
				d.state = stateWaitStart1
			}
		}
	}

	return frames
}

// EncodeFrame wraps protobuf data in a Meshtastic serial frame.
func EncodeFrame(payload []byte) []byte {
	if len(payload) > maxFrameLen {
		return nil
	}

	frame := make([]byte, 4+len(payload))
	frame[0] = frameStart1
	frame[1] = frameStart2
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(payload)))
	copy(frame[4:], payload)
	return frame
}

// FromRadioPacket represents a decoded Meshtastic FromRadio message.
type FromRadioPacket struct {
	// Type of the payload variant
	Type FromRadioType

	// Raw protobuf bytes (for re-encoding or forwarding)
	Raw []byte

	// Decoded fields (populated based on Type)
	MyInfo    *MyNodeInfo
	NodeInfo  *NodeInfoLite
	MeshPacket *MeshPacketData
	Config    *ConfigPayload
	Channel   *ChannelPayload
	Metadata  *DeviceMetadata
}

type FromRadioType int

const (
	FromRadioUnknown FromRadioType = iota
	FromRadioMyInfo
	FromRadioNodeInfo
	FromRadioMeshPacket
	FromRadioConfig
	FromRadioChannel
	FromRadioMetadata
	FromRadioConfigComplete
	FromRadioRebooted
)

func (t FromRadioType) String() string {
	switch t {
	case FromRadioMyInfo:
		return "MyInfo"
	case FromRadioNodeInfo:
		return "NodeInfo"
	case FromRadioMeshPacket:
		return "MeshPacket"
	case FromRadioConfig:
		return "Config"
	case FromRadioChannel:
		return "Channel"
	case FromRadioMetadata:
		return "Metadata"
	case FromRadioConfigComplete:
		return "ConfigComplete"
	case FromRadioRebooted:
		return "Rebooted"
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}

// Simplified data structures for decoded packets.
// These mirror the Meshtastic protobuf types but as plain Go structs.

type MyNodeInfo struct {
	MyNodeNum       uint32
	RebootCount     uint32
	MinAppVersion   uint32
	MaxChannels     uint32
}

type NodeInfoLite struct {
	Num             uint32
	User            *UserInfo
	Position        *PositionData
	SNR             float32
	LastHeard       uint32
	DeviceMetrics   *DeviceMetrics
	Channel         uint32
	IsFavorite      bool
}

type UserInfo struct {
	ID        string
	LongName  string
	ShortName string
	HWModel   string
	Role      string
}

type PositionData struct {
	LatitudeI  int32
	LongitudeI int32
	Altitude   int32
	Time       uint32
	Sats       uint32
}

// Latitude returns the position latitude as a float64.
func (p *PositionData) Latitude() float64 {
	return float64(p.LatitudeI) * 1e-7
}

// Longitude returns the position longitude as a float64.
func (p *PositionData) Longitude() float64 {
	return float64(p.LongitudeI) * 1e-7
}

type DeviceMetrics struct {
	BatteryLevel      uint32
	Voltage           float32
	ChannelUtilization float32
	AirUtilTx         float32
	UptimeSeconds     uint32
}

// EnvironmentMetrics from Meshtastic Telemetry.environment_metrics (field 3)
type EnvironmentMetrics struct {
	Temperature        float32
	RelativeHumidity   float32
	BarometricPressure float32
}

type MeshPacketData struct {
	From        uint32
	To          uint32
	Channel     uint32
	ID          uint32
	RxTime      uint32
	RxSNR       float32
	RxRSSI      int32
	HopLimit    uint32
	HopStart    uint32
	Priority    uint32
	PortNum     uint32
	Payload     []byte
	WantAck     bool
}

type ConfigPayload struct {
	Section string
	Raw     []byte
}

type ChannelPayload struct {
	Index    uint32
	Role     uint32
	Settings []byte
}

type DeviceMetadata struct {
	FirmwareVersion string
	DeviceStateVersion uint32
	HasBluetooth    bool
	HasWifi         bool
	HasEthernet     bool
}

// DecodeFromRadio decodes a protobuf frame into a FromRadioPacket.
// This is a manual protobuf decoder that doesn't require generated code.
// Meshtastic FromRadio field numbers:
//   2 = my_info, 3 = node_info, 4 = config, 5 = log_record
//   7 = config_complete_id, 8 = rebooted, 11 = mesh_packet
//   12 = channel, 13 = metadata
func DecodeFromRadio(data []byte) (*FromRadioPacket, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty frame")
	}

	pkt := &FromRadioPacket{Raw: data}

	// Parse top-level fields
	pos := 0
	for pos < len(data) {
		if pos >= len(data) {
			break
		}

		// Read field tag (varint)
		fieldTag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n

		fieldNum := fieldTag >> 3
		wireType := fieldTag & 0x7

		switch wireType {
		case 0: // Varint
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				return pkt, fmt.Errorf("truncated varint at field %d", fieldNum)
			}
			pos += n

			switch fieldNum {
			case 7: // config_complete_id
				pkt.Type = FromRadioConfigComplete
			case 8: // rebooted
				_ = val
				pkt.Type = FromRadioRebooted
			}

		case 2: // Length-delimited
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				return pkt, fmt.Errorf("truncated length at field %d", fieldNum)
			}
			pos += n

			if pos+int(length) > len(data) {
				return pkt, fmt.Errorf("truncated payload at field %d", fieldNum)
			}

			subData := data[pos : pos+int(length)]
			pos += int(length)

			switch fieldNum {
			case 2: // my_info
				pkt.Type = FromRadioMyInfo
				pkt.MyInfo = decodeMyInfo(subData)
			case 3: // node_info
				pkt.Type = FromRadioNodeInfo
				pkt.NodeInfo = decodeNodeInfo(subData)
			case 11: // mesh_packet
				pkt.Type = FromRadioMeshPacket
				pkt.MeshPacket = decodeMeshPacket(subData)
			case 4: // config
				pkt.Type = FromRadioConfig
				pkt.Config = &ConfigPayload{Raw: subData}
			case 12: // channel
				pkt.Type = FromRadioChannel
			case 13: // metadata
				pkt.Type = FromRadioMetadata
				pkt.Metadata = decodeMetadata(subData)
			}

		case 5: // 32-bit
			if pos+4 > len(data) {
				return pkt, fmt.Errorf("truncated fixed32 at field %d", fieldNum)
			}
			pos += 4

		case 1: // 64-bit
			if pos+8 > len(data) {
				return pkt, fmt.Errorf("truncated fixed64 at field %d", fieldNum)
			}
			pos += 8

		default:
			return pkt, fmt.Errorf("unknown wire type %d at field %d", wireType, fieldNum)
		}
	}

	return pkt, nil
}

// BuildWantConfig creates a ToRadio protobuf requesting full config from the radio.
func BuildWantConfig() []byte {
	// ToRadio field 3 = want_config_id (uint32)
	// We use config_id = 1 (any nonzero value)
	configID := uint32(1)
	tag := encodeVarint((3 << 3) | 0) // field 3, wire type 0 (varint)
	val := encodeVarint(uint64(configID))
	return append(tag, val...)
}

// Protobuf varint helpers

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

func encodeVarint(val uint64) []byte {
	var buf [10]byte
	n := 0
	for val >= 0x80 {
		buf[n] = byte(val) | 0x80
		val >>= 7
		n++
	}
	buf[n] = byte(val)
	return buf[:n+1]
}

func decodeMyInfo(data []byte) *MyNodeInfo {
	info := &MyNodeInfo{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			switch fieldNum {
			case 1:
				info.MyNodeNum = uint32(val)
			case 8:
				info.RebootCount = uint32(val)
			case 11:
				info.MaxChannels = uint32(val)
			}
		} else {
			// Skip other wire types
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return info
}

func decodeNodeInfo(data []byte) *NodeInfoLite {
	info := &NodeInfoLite{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 0: // varint
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				return info
			}
			pos += n
			switch fieldNum {
			case 1:
				info.Num = uint32(val)
			case 4:
				info.LastHeard = uint32(val)
			case 7:
				info.Channel = uint32(val)
			}
		case 2: // length-delimited
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				return info
			}
			pos += n
			if pos+int(length) > len(data) {
				return info
			}
			subData := data[pos : pos+int(length)]
			pos += int(length)
			switch fieldNum {
			case 2:
				info.User = decodeUserInfo(subData)
			case 3:
				info.Position = decodePosition(subData)
			case 6:
				info.DeviceMetrics = decodeDeviceMetrics(subData)
			}
		case 5: // fixed32
			if pos+4 > len(data) {
				return info
			}
			if fieldNum == 5 {
				info.SNR = decodeFloat32(data[pos : pos+4])
			}
			pos += 4
		default:
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				return info
			}
		}
	}
	return info
}

func decodeUserInfo(data []byte) *UserInfo {
	user := &UserInfo{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 2 {
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			if pos+int(length) > len(data) {
				break
			}
			s := string(data[pos : pos+int(length)])
			pos += int(length)
			switch fieldNum {
			case 1:
				user.ID = s
			case 2:
				user.LongName = s
			case 3:
				user.ShortName = s
			}
		} else if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			switch fieldNum {
			case 5:
				user.HWModel = hwModelName(int(val))
			case 6:
				user.Role = roleName(int(val))
			}
		} else {
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return user
}

func decodePosition(data []byte) *PositionData {
	p := &PositionData{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 5 { // fixed32
			if pos+4 > len(data) {
				break
			}
			val := int32(binary.LittleEndian.Uint32(data[pos : pos+4]))
			pos += 4
			switch fieldNum {
			case 1:
				p.LatitudeI = val
			case 2:
				p.LongitudeI = val
			case 3:
				p.Altitude = val
			}
		} else if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			switch fieldNum {
			case 4:
				p.Time = uint32(val)
			case 10:
				p.Sats = uint32(val)
			}
		} else {
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return p
}

func decodeDeviceMetrics(data []byte) *DeviceMetrics {
	dm := &DeviceMetrics{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			switch fieldNum {
			case 1:
				dm.BatteryLevel = uint32(val)
			case 5:
				dm.UptimeSeconds = uint32(val)
			}
		} else if wireType == 5 { // fixed32 (float)
			if pos+4 > len(data) {
				break
			}
			f := decodeFloat32(data[pos : pos+4])
			pos += 4
			switch fieldNum {
			case 2:
				dm.Voltage = f
			case 3:
				dm.ChannelUtilization = f
			case 4:
				dm.AirUtilTx = f
			}
		} else {
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return dm
}

func decodeMeshPacket(data []byte) *MeshPacketData {
	mp := &MeshPacketData{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 0:
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				return mp
			}
			pos += n
			switch fieldNum {
			case 1:
				mp.From = uint32(val)
			case 2:
				mp.To = uint32(val)
			case 3:
				mp.Channel = uint32(val)
			case 6:
				mp.ID = uint32(val)
			case 7:
				mp.RxTime = uint32(val)
			case 10:
				mp.HopLimit = uint32(val)
			case 11:
				mp.WantAck = val != 0
			case 12:
				mp.Priority = uint32(val)
			case 15:
				mp.HopStart = uint32(val)
			}
		case 2: // length-delimited
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				return mp
			}
			pos += n
			if pos+int(length) > len(data) {
				return mp
			}
			subData := data[pos : pos+int(length)]
			pos += int(length)
			if fieldNum == 4 { // decoded payload (Data message)
				mp.PortNum, mp.Payload = decodeDataPayload(subData)
			}
		case 5: // fixed32
			if pos+4 > len(data) {
				return mp
			}
			u32 := binary.LittleEndian.Uint32(data[pos : pos+4])
			pos += 4
			switch fieldNum {
			case 1: // from (fixed32 in Meshtastic proto)
				mp.From = u32
			case 2: // to (fixed32 in Meshtastic proto)
				mp.To = u32
			case 6: // id (fixed32 in Meshtastic proto)
				mp.ID = u32
			case 7: // rx_time (fixed32)
				mp.RxTime = u32
			case 8: // rx_snr (float)
				mp.RxSNR = math.Float32frombits(u32)
			case 9: // rx_rssi (sint32 — but some firmware encodes as fixed32)
				mp.RxRSSI = int32(u32)
			}
		default:
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				return mp
			}
		}
	}
	return mp
}

func decodeDataPayload(data []byte) (portNum uint32, payload []byte) {
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			if fieldNum == 1 {
				portNum = uint32(val)
			}
		} else if wireType == 2 {
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			if pos+int(length) > len(data) {
				break
			}
			if fieldNum == 2 {
				payload = make([]byte, length)
				copy(payload, data[pos:pos+int(length)])
			}
			pos += int(length)
		} else {
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return
}

func decodeMetadata(data []byte) *DeviceMetadata {
	meta := &DeviceMetadata{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType == 2 {
			length, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			if pos+int(length) > len(data) {
				break
			}
			s := string(data[pos : pos+int(length)])
			pos += int(length)
			if fieldNum == 1 {
				meta.FirmwareVersion = s
			}
		} else if wireType == 0 {
			val, n := decodeVarint(data[pos:])
			if n == 0 {
				break
			}
			pos += n
			switch fieldNum {
			case 2:
				meta.DeviceStateVersion = uint32(val)
			case 4:
				meta.HasBluetooth = val != 0
			case 5:
				meta.HasWifi = val != 0
			case 6:
				meta.HasEthernet = val != 0
			}
		} else {
			pos = skipField(data, pos, wireType)
			if pos < 0 {
				break
			}
		}
	}
	return meta
}

func skipField(data []byte, pos int, wireType uint64) int {
	switch wireType {
	case 0: // varint
		for pos < len(data) {
			if data[pos]&0x80 == 0 {
				return pos + 1
			}
			pos++
		}
		return -1
	case 1: // 64-bit
		return pos + 8
	case 2: // length-delimited
		length, n := decodeVarint(data[pos:])
		if n == 0 {
			return -1
		}
		return pos + n + int(length)
	case 5: // 32-bit
		return pos + 4
	default:
		return -1
	}
}

func decodeFloat32(data []byte) float32 {
	bits := binary.LittleEndian.Uint32(data)
	return math.Float32frombits(bits)
}

// Meshtastic HardwareModel enum names (subset)
func hwModelName(model int) string {
	names := map[int]string{
		0: "UNSET", 1: "TLORA_V2", 2: "TLORA_V1", 3: "TLORA_V2_1_1P6",
		4: "TBEAM", 5: "HELTEC_V2_0", 6: "TBEAM_V0P7", 7: "T_ECHO",
		8: "TLORA_V1_1P3", 9: "RAK4631", 10: "HELTEC_V2_1",
		25: "TBEAM_S3_CORE", 39: "HELTEC_V3", 43: "HELTEC_WSL_V3",
		44: "BETAFPV_2400_TX", 47: "RAK11310",
		255: "PRIVATE_HW",
	}
	if name, ok := names[model]; ok {
		return name
	}
	return fmt.Sprintf("HW_%d", model)
}

// Meshtastic Role enum names
func roleName(role int) string {
	names := map[int]string{
		0: "CLIENT", 1: "CLIENT_MUTE", 2: "ROUTER", 3: "REPEATER",
		4: "TRACKER", 5: "SENSOR", 6: "TAK", 7: "CLIENT_HIDDEN",
		8: "LOST_AND_FOUND", 9: "SENSOR_MANAGED",
	}
	if name, ok := names[role]; ok {
		return name
	}
	return fmt.Sprintf("ROLE_%d", role)
}
