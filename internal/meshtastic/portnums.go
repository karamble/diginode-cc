package meshtastic

// PortNum identifies the type of payload in a MeshPacket Data message.
// These match the Meshtastic protobuf PortNum enum.
type PortNum uint32

const (
	PortNumUnknown          PortNum = 0
	PortNumTextMessage      PortNum = 1
	PortNumRemoteHardware   PortNum = 2
	PortNumPosition         PortNum = 3
	PortNumNodeInfo         PortNum = 4
	PortNumRouting          PortNum = 5
	PortNumAdmin            PortNum = 6
	PortNumTextMessageComp  PortNum = 7
	PortNumWaypoint         PortNum = 8
	PortNumAudio            PortNum = 9
	PortNumDetectionSensor  PortNum = 10
	PortNumReply            PortNum = 32
	PortNumIPTunnel         PortNum = 33
	PortNumPaxcounter       PortNum = 34
	PortNumSerial           PortNum = 64
	PortNumStoreForward     PortNum = 65
	PortNumRangeTest        PortNum = 66
	PortNumTelemetry        PortNum = 67
	PortNumZPS              PortNum = 68
	PortNumSimulator        PortNum = 69
	PortNumTraceroute       PortNum = 70
	PortNumNeighborInfo     PortNum = 71
	PortNumATAKPlugin       PortNum = 72
	PortNumMapReport        PortNum = 73
	PortNumPowerStress      PortNum = 74
	PortNumPrivate          PortNum = 256
	PortNumATAKForwarder    PortNum = 257
	PortNumMax              PortNum = 511
)

func (p PortNum) String() string {
	names := map[PortNum]string{
		PortNumUnknown:         "UNKNOWN",
		PortNumTextMessage:     "TEXT_MESSAGE",
		PortNumRemoteHardware:  "REMOTE_HARDWARE",
		PortNumPosition:        "POSITION",
		PortNumNodeInfo:        "NODEINFO",
		PortNumRouting:         "ROUTING",
		PortNumAdmin:           "ADMIN",
		PortNumTextMessageComp: "TEXT_MESSAGE_COMPRESSED",
		PortNumWaypoint:        "WAYPOINT",
		PortNumAudio:           "AUDIO",
		PortNumDetectionSensor: "DETECTION_SENSOR",
		PortNumReply:           "REPLY",
		PortNumIPTunnel:        "IP_TUNNEL",
		PortNumPaxcounter:      "PAXCOUNTER",
		PortNumSerial:          "SERIAL",
		PortNumStoreForward:    "STORE_FORWARD",
		PortNumRangeTest:       "RANGE_TEST",
		PortNumTelemetry:       "TELEMETRY",
		PortNumZPS:             "ZPS",
		PortNumSimulator:       "SIMULATOR",
		PortNumTraceroute:      "TRACEROUTE",
		PortNumNeighborInfo:    "NEIGHBOR_INFO",
		PortNumATAKPlugin:      "ATAK_PLUGIN",
		PortNumMapReport:       "MAP_REPORT",
		PortNumPowerStress:     "POWER_STRESS",
		PortNumPrivate:         "PRIVATE",
		PortNumATAKForwarder:   "ATAK_FORWARDER",
	}
	if name, ok := names[p]; ok {
		return name
	}
	return "UNKNOWN"
}
