package serial

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ParsedEvent represents a parsed serial text line.
type ParsedEvent struct {
	Kind     string                 // "node-telemetry", "target-detected", "alert", "command-ack", "drone-telemetry", "text-message", "raw"
	NodeID   string                 // Source node ID
	PacketID uint32                 // Meshtastic packet ID (extracted from echo line, 0 if unavailable)
	ToNode   uint32                 // Destination node (0 = unknown, BroadcastAddr = broadcast, else DM target)
	Data     map[string]interface{} // Parsed fields
	Raw      string                 // Original line
	Category string                 // For alerts: "status", "gps", "attack", etc.
	Level    string                 // For alerts: "INFO", "NOTICE", "ALERT", "CRITICAL"
}

// pktMeta caches addressing info extracted from Meshtastic debug lines.
type pktMeta struct {
	to uint32
}

// TextParser parses Meshtastic debug console text lines.
type TextParser struct {
	// Pre-compiled patterns (most common first for efficiency)
	patterns []patternEntry

	// Echo detection helpers
	echoRouter  *regexp.Regexp
	echoTextMsg *regexp.Regexp
	fromExtract *regexp.Regexp
	idExtract   *regexp.Regexp
	ansiClean   *regexp.Regexp
	toExtract   *regexp.Regexp // extracts to=0x... from Lora RX / phone downloaded lines

	// Payload normalization helpers
	nodeIDFallback *regexp.Regexp
	trailingHash   *regexp.Regexp

	// Packet metadata cache: packet ID → addressing info.
	// Populated from "Lora RX" and "phone downloaded" debug lines
	// that arrive before or after the text echo line.
	pktCache   map[uint32]pktMeta
	pktCacheMu sync.Mutex
}

type patternEntry struct {
	name    string
	regex   *regexp.Regexp
	handler func(match []string, names []string, raw string) []*ParsedEvent
}

// NewTextParser creates a text parser matching CC PRO's meshtastic-rewrite patterns.
func NewTextParser() *TextParser {
	p := &TextParser{
		echoRouter:     regexp.MustCompile(`(?i)\[(Router|SerialConsole)\]`),
		echoTextMsg:    regexp.MustCompile(`(?i)\btextmessage\s+msg=`),
		fromExtract:    regexp.MustCompile(`(?i)from=(?:0x)?([0-9a-fA-F]+)`),
		idExtract:      regexp.MustCompile(`(?i)\bid=(?:0x)?([0-9a-fA-F]+)`),
		toExtract:      regexp.MustCompile(`(?i)\bto=(?:0x)?([0-9a-fA-F]+)`),
		ansiClean:      regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`),
		nodeIDFallback: regexp.MustCompile(`^([A-Za-z0-9_.:-]+)`),
		trailingHash:   regexp.MustCompile(`#+$`),
		pktCache:       make(map[uint32]pktMeta),
	}
	p.initPatterns()
	return p
}

func (p *TextParser) initPatterns() {
	p.patterns = []patternEntry{
		// STATUS line: "nodeId: STATUS: Mode:SCAN Scan:1..14 Hits:42 Temp:38c/100F Up:01:23:45 GPS:12.34,56.78"
		{
			name: "status",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+)?:?\s*STATUS:\s*Mode:(?P<mode>\S+)\s+Scan:(?P<scan>\S+)\s+Hits:(?P<hits>\d+)\s+(?:Targets:(?P<targets>\d+)\s+)?Temp:(?P<tempC>-?\d+(?:\.\d+)?)[cC](?:/(?P<tempF>-?\d+(?:\.\d+)?)[Ff])?\s+Up:(?P<up>[0-9:]+)(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?(?:\s+HDOP[:=](?P<hdop>-?\d+(?:\.\d+)?))?`),
			handler: p.handleStatus,
		},
		// DRONE line: "nodeId: DRONE: MAC:AA:BB:CC:DD:EE:FF ID:drone1 R-75 GPS:12.34,56.78 ALT:100 SPD:20 OP:12.35,56.79"
		{
			name: "drone",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*DRONE:\s+(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+ID:(?P<droneId>[A-Za-z0-9_-]+)\s+R(?P<rssi>-?\d+)\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?)(?:\s+ALT:(?P<alt>-?\d+(?:\.\d+)?))?(?:\s+SPD:(?P<spd>-?\d+(?:\.\d+)?))?(?:\s+OP:(?P<opLat>-?\d+(?:\.\d+)?),(?P<opLon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleDrone,
		},
		// T_D (TARGET_DATA): triangulation detection from node
		// "nodeId: T_D: AA:BB:CC:DD:EE:FF RSSI:-45 Hits=5 Type:WiFi GPS=12.34,56.78"
		{
			name: "tri-target-data",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*(?:TARGET_DATA|T_D):\s*(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+RSSI:(?P<rssi>-?\d+)(?:\s+Hits=(?P<hits>\d+))?(?:\s+Type:(?P<type>\w+))?(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleTriData,
		},
		// T_F (FINAL): triangulation final result
		// "nodeId: T_F: MAC=AA:BB:CC:DD:EE:FF GPS=12.34,56.78 CONF=87.5 UNC=12.3"
		{
			name: "tri-final",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*T_F:\s*MAC=(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+GPS=(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?)\s+CONF=(?P<conf>-?\d+(?:\.\d+)?)\s+UNC=(?P<unc>-?\d+(?:\.\d+)?)`),
			handler: p.handleTriFinal,
		},
		// T_C (COMPLETE): triangulation complete
		// "nodeId: T_C: MAC=AA:BB:CC:DD:EE:FF Nodes=3"
		{
			name: "tri-complete",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*T_C:\s*(?:MAC=(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+)?Nodes=(?P<nodes>\d+)`),
			handler: p.handleTriComplete,
		},
		// Target: type-first: "nodeId: Target: WiFi AA:BB:CC:DD:EE:FF RSSI:-75 Name:MyDevice GPS:12.34,56.78"
		{
			name: "target-type-first",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*Target:\s*(?P<type>\w+)\s+(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+RSSI:(?P<rssi>-?\d+)(?:\s+Name:(?P<name>[^ ]+))?(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleTarget,
		},
		// Target: mac-first: "nodeId: Target: AA:BB:CC:DD:EE:FF RSSI:-75 Type:WiFi Name:MyDevice"
		{
			name: "target-mac-first",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*Target:\s*(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+RSSI:(?P<rssi>-?\d+)\s+Type:(?P<type>\w+)(?:\s+Name:(?P<name>[^ ]+))?(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleTarget,
		},
		// DEVICE line: "nodeId: DEVICE:AA:BB:CC:DD:EE:FF W -75 C6 N:MyDevice"
		{
			name: "device",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*DEVICE:(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+(?P<band>[A-Za-z])\s+(?P<rssi>-?\d+)(?:\s+C(?P<channel>\d+))?(?:\s+N:(?P<name>.+))?`),
			handler: p.handleDevice,
		},
		// ATTACK long: "nodeId: ATTACK: DEAUTH [BROADCAST] SRC:AA:BB:CC:DD:EE:FF DST:11:22:33:44:55:66 RSSI:-60dBm CH:6"
		{
			name: "attack-long",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ATTACK:\s*(?P<kind>DEAUTH|DISASSOC)(?:\s+\[(?P<mode>BROADCAST|TARGETED)\])?\s+SRC:(?P<src>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+DST:(?P<dst>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+RSSI:(?P<rssi>-?\d+)d?Bm?\s+CH:(?P<chan>\d+)`),
			handler: p.handleAttackLong,
		},
		// ATTACK short: "nodeId: ATTACK: DEAUTH AA:BB:CC:DD:EE:FF->11:22:33:44:55:66 R-60 C6"
		{
			name: "attack-short",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ATTACK:\s*(?P<kind>DEAUTH|DISASSOC)\s+(?P<src>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})->(?P<dst>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+R(?P<rssi>-?\d+)\s+C(?P<chan>\d+)`),
			handler: p.handleAttackShort,
		},
		// GPS LOCKED: "nodeId: GPS: LOCKED Location=12.34,56.78 Satellites=8 HDOP=1.2"
		{
			name: "gps-lock",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+)?:?\s*GPS:\s*LOCKED\s+Location[=:](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?)(?:\s+Satellites[=:](?P<sats>\d+))?(?:\s+HDOP[=:](?P<hdop>-?\d+(?:\.\d+)?))?`),
			handler: p.handleGPSLock,
		},
		// GPS LOST: "nodeId: GPS: LOST"
		{
			name: "gps-lost",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+)?:?\s*GPS:\s*LOST`),
			handler: p.handleGPSLost,
		},
		// NODE_HB bracketed: "[NODE_HB] nodeId Time:12345 Temp:38c/100F GPS:12.34,56.78"
		{
			name: "node-hb",
			regex: regexp.MustCompile(
				`(?i)^\[NODE_HB\]\s*(?P<id>[A-Za-z0-9_.:-]+)\s+Time:(?P<time>[^ ]+)\s+Temp:(?P<tempC>-?\d+(?:\.\d+)?)(?:[cCfF])?(?:/(?P<tempF>-?\d+(?:\.\d+)?)[fF])?(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleNodeHB,
		},
		// Battery-saver heartbeat: "nodeId: HEARTBEAT: Temp:38c/100F GPS:12.34,56.78 Battery:SAVER"
		// Must precede node-hb-inline since that pattern would otherwise swallow the "HEARTBEAT:" token.
		{
			name: "node-hb-saver",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*HEARTBEAT:\s*Temp:(?P<tempC>-?\d+(?:\.\d+)?)(?:[cCfF])?(?:/(?P<tempF>-?\d+(?:\.\d+)?)[fF])?(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?(?:\s+Battery:(?P<battery>\S+))?`),
			handler: p.handleNodeHB,
		},
		// NODE_HB inline: "nodeId: Time:12345 Temp:38c/100F GPS:12.34,56.78"
		{
			name: "node-hb-inline",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):?\s*Time:(?P<time>[^ ]+)\s+Temp:(?P<tempC>-?\d+(?:\.\d+)?)(?:[cCfF])?(?:/(?P<tempF>-?\d+(?:\.\d+)?)[fF])?(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleNodeHB,
		},
		// ACK lines: "nodeId: SCAN_ACK:OK" / "nodeId: DRONE_ACK:" etc.
		// VIBRATION_(ON|OFF)_ACK is included so the new firmware vibration toggle
		// (commit 1d9477d in karamble/AntiHunter feature/vibration-toggle) closes
		// the command lifecycle in DigiNode CC.
		{
			name: "ack",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*(?P<kind>(?:SCAN|DEVICE_SCAN|DRONE|DEAUTH|RANDOMIZATION|BASELINE|CONFIG|TRIANGULATE(?:_STOP)?|TRI_START|STOP|REBOOT|BATTERY_SAVER(?:_START|_STOP)?|VIBRATION_(?:ON|OFF))_ACK):?(?P<status>[A-Z_]*)`),
			handler: p.handleACK,
		},
		// ANOMALY: "nodeId: ANOMALY-NEW: WiFi AA:BB:CC:DD:EE:FF RSSI:-60 Name:test"
		{
			name: "anomaly",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ANOMALY-(?P<anomKind>NEW|RETURN|RSSI):\s*(?P<type>\w+)\s+(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})(?:\s+RSSI:(?P<rssi>-?\d+))?(?:\s+Old:(?P<old>-?\d+)\s+New:(?P<new>-?\d+)\s+Delta:(?P<delta>-?\d+))?(?:\s+Name:(?P<name>[^ ]+))?`),
			handler: p.handleAnomaly,
		},
		// CAM: camera detection: "CAM: front_door person 87% ZONE:driveway GPS:48.1234,11.5678"
		{
			name: "cam-detection",
			regex: regexp.MustCompile(
				`(?i)^CAM:\s+(?P<camera>\S+)\s+(?P<label>\S+)\s+(?P<score>\d+)%(?:\s+ZONE:(?P<zone>\S+))?(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleCamDetection,
		},
		// CAM_FACE: face recognition: "CAM_FACE: front_door John GPS:48.1234,11.5678"
		{
			name: "cam-face",
			regex: regexp.MustCompile(
				`(?i)^CAM_FACE:\s+(?P<camera>\S+)\s+(?P<name>\S+)(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleCamFace,
		},
		// CAM_PLATE: license plate: "CAM_PLATE: driveway ABC123 GPS:48.1234,11.5678"
		{
			name: "cam-plate",
			regex: regexp.MustCompile(
				`(?i)^CAM_PLATE:\s+(?P<camera>\S+)\s+(?P<plate>\S+)(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleCamPlate,
		},
		// STARTUP: "nodeId: STARTUP: firmware v1.0"
		{
			name: "startup",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+)?:?\s*STARTUP:\s*(?P<msg>.+)$`),
			handler: p.handleStartup,
		},
		// VIBRATION: "nodeId: VIBRATION: motion detected GPS:12.34,56.78 TAMPER_ERASE_IN:30s"
		{
			name: "vibration",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*VIBRATION:\s*(?P<msg>.+?)(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?(?:\s+TAMPER_ERASE_IN:(?P<erase>\d+)s)?$`),
			handler: p.handleVibration,
		},
		// TAMPER: "nodeId: TAMPER_DETECTED: message"
		{
			name: "tamper",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*TAMPER_(?P<tamperKind>DETECTED|CANCELLED):?(?:\s*(?P<msg>.+))?`),
			handler: p.handleTamper,
		},
		// SCAN_DONE: "nodeId: SCAN_DONE: W=42 B=15 U=30 Targets=5 Remaining=2"
		{
			name: "scan-done",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*SCAN_DONE:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("scan"),
		},
		// DEAUTH_DONE: "nodeId: DEAUTH_DONE: Total=8 Deauth=5 Disassoc=3 TX=7 Remaining=1"
		{
			name: "deauth-done",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*DEAUTH_DONE:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("deauth"),
		},
		// LIST_SCAN_DONE: "nodeId: LIST_SCAN_DONE: Hits=15 Unique=10 Targets=8 TX=8 Remaining=0"
		{
			name: "list-scan-done",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*LIST_SCAN_DONE:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("list-scan"),
		},
		// DRONE_DONE: "nodeId: DRONE_DONE: Detected=3 Unique=3 TX=3 Remaining=0"
		{
			name: "drone-done",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*DRONE_DONE:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("drone"),
		},
		// RANDOMIZATION_DONE: "nodeId: RANDOMIZATION_DONE: Identities=5 Sessions=2 TX=5 Remaining=0"
		{
			name: "randomization-done",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*RANDOMIZATION_DONE:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("randomization"),
		},
		// BASELINE_STATUS: "nodeId: BASELINE_STATUS: Scanning:YES Established:NO Devices:42 Anomalies:2 Phase1:PENDING"
		{
			name: "baseline-status",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*BASELINE_STATUS:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("baseline-status"),
		},
		// IDENTITY: "nodeId: IDENTITY:ID123 W -68 Hits:4 MACs:3"
		{
			name: "identity",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*IDENTITY:(?P<identityId>\S+)\s+(?P<band>[A-Za-z])\s+(?P<rssi>-?\d+)(?:\s+Hits:(?P<hits>\d+))?(?:\s+MACs:(?P<macs>\d+))?`),
			handler: p.handleIdentity,
		},
		// SETUP_MODE: "nodeId: SETUP_MODE: Auto-erase activates in 30s"
		{
			name: "setup-mode",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*SETUP_MODE:\s*(?P<msg>.+)$`),
			handler: p.handleSetupMode,
		},
		// BATTERY_SAVER: "nodeId: BATTERY_SAVER: ENABLED Interval:5min" / "DISABLED"
		{
			name: "battery-saver-state",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*BATTERY_SAVER:\s*(?P<state>ENABLED|DISABLED)(?:\s+Interval:(?P<interval>\S+))?`),
			handler: p.handleBatterySaverState,
		},
		// BATTERY_SAVER_STATUS: "nodeId: BATTERY_SAVER_STATUS: Enabled Interval:5min"
		{
			name: "battery-saver-status",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*BATTERY_SAVER_STATUS:\s*(?P<body>.+)$`),
			handler: p.handleDoneSummary("battery-saver-status"),
		},
		// ERASE_EXECUTING: "nodeId: ERASE_EXECUTING: reason GPS:12.34,56.78"
		{
			name: "erase-executing",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ERASE_EXECUTING:\s*(?P<msg>.+?)(?:\s+GPS[:=](?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?$`),
			handler: p.handleEraseEvent("executing", "CRITICAL"),
		},
		// ERASE_COMPLETE: "nodeId: ERASE_COMPLETE"
		{
			name: "erase-complete",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ERASE_COMPLETE\b\s*(?P<msg>.*)$`),
			handler: p.handleEraseEvent("complete", "CRITICAL"),
		},
		// ERASE_TOKEN: "nodeId: ERASE_TOKEN:ABC123DEF456 Expires:300s"
		{
			name: "erase-token",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ERASE_TOKEN:(?P<token>\S+)(?:\s+Expires:(?P<expires>\d+)s?)?`),
			handler: p.handleEraseToken,
		},
		// RTC_SYNC: "nodeId: RTC_SYNC: GPS"
		{
			name: "rtc-sync",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*RTC_SYNC:\s*(?P<source>\S+)`),
			handler: p.handleRTCSync,
		},
		// @ALL broadcast commands — these are commands relayed through the mesh,
		// no node-id prefix. Useful for classification + audit, low-value for state.
		// "@ALL TRIANGULATE_START:AA:BB:CC:DD:EE:FF:60:NODE1"
		{
			name: "bcast-triangulate-start",
			regex: regexp.MustCompile(
				`(?i)^@ALL\s+TRIANGULATE_START:(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}):(?P<duration>\d+):(?P<initiator>\S+)`),
			handler: p.handleBroadcastCommand("triangulate-start"),
		},
		// "@ALL TRI_CYCLE_START:5000:NODE1,NODE2,NODE3"
		{
			name: "bcast-tri-cycle-start",
			regex: regexp.MustCompile(
				`(?i)^@ALL\s+TRI_CYCLE_START:(?P<intervalMs>\d+):(?P<nodes>.+)$`),
			handler: p.handleBroadcastCommand("tri-cycle-start"),
		},
		// "@ALL TRIANGULATE_STOP"
		{
			name: "bcast-triangulate-stop",
			regex: regexp.MustCompile(
				`(?i)^@ALL\s+TRIANGULATE_STOP\b`),
			handler: p.handleBroadcastCommand("triangulate-stop"),
		},
		// "@ALL SYNC:1740512445.320 (drift-corrected)"
		{
			name: "bcast-sync",
			regex: regexp.MustCompile(
				`(?i)^@ALL\s+SYNC:(?P<timestamp>\d+(?:\.\d+)?)(?:\s+\((?P<note>[^)]+)\))?`),
			handler: p.handleBroadcastCommand("sync"),
		},
	}
}

// cachePacketMeta extracts packet ID and "to" address from Meshtastic debug lines
// like "Lora RX (id=0x343cf551 fr=0x0409c9d8 to=0x02ec2fe0 ...)" and
// "phone downloaded packet (id=0x343cf551 fr=0x0409c9d8 to=0x02ec2fe0 ...)".
// These arrive before the "Received text msg" echo and contain the full addressing.
func (p *TextParser) cachePacketMeta(cleaned string) {
	lower := strings.ToLower(cleaned)
	if !strings.Contains(lower, "lora rx") && !strings.Contains(lower, "phone downloaded") {
		return
	}

	idMatch := p.idExtract.FindStringSubmatch(cleaned)
	toMatch := p.toExtract.FindStringSubmatch(cleaned)
	if idMatch == nil || toMatch == nil {
		return
	}

	pktID, err := strconv.ParseUint(idMatch[1], 16, 32)
	if err != nil {
		return
	}
	toAddr, err := strconv.ParseUint(toMatch[1], 16, 32)
	if err != nil {
		return
	}

	p.pktCacheMu.Lock()
	p.pktCache[uint32(pktID)] = pktMeta{to: uint32(toAddr)}
	// Prune if cache gets too large
	if len(p.pktCache) > 256 {
		for k := range p.pktCache {
			delete(p.pktCache, k)
			if len(p.pktCache) <= 128 {
				break
			}
		}
	}
	p.pktCacheMu.Unlock()
}

// lookupPacketTo returns the cached "to" address for a packet ID, or 0 if unknown.
func (p *TextParser) lookupPacketTo(pktID uint32) uint32 {
	if pktID == 0 {
		return 0
	}
	p.pktCacheMu.Lock()
	meta, ok := p.pktCache[pktID]
	if ok {
		delete(p.pktCache, pktID) // consume
	}
	p.pktCacheMu.Unlock()
	if ok {
		return meta.to
	}
	return 0
}

// ParseLine processes a single text line from the Heltec serial console.
func (p *TextParser) ParseLine(line string) []*ParsedEvent {
	// 1. Clean ANSI escape codes and non-printable chars
	cleaned := p.ansiClean.ReplaceAllString(line, "")
	cleaned = cleanNonPrintable(cleaned)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil
	}

	// Drop tiny ANSI fragments like "0m"
	if len(cleaned) <= 3 {
		stripped := strings.TrimSpace(strings.ToLower(cleaned))
		if stripped == "0m" || stripped == "" {
			return nil
		}
	}

	// Cache packet addressing from Lora RX / phone downloaded debug lines.
	// These contain the full from/to/id before the text echo arrives.
	p.cachePacketMeta(cleaned)

	// 2. Check for Meshtastic text message echo (this determines payload extraction)
	isMeshEcho := p.isMeshtasticEcho(cleaned)
	payload := p.extractPayload(cleaned, isMeshEcho)

	// 3. Try each pattern against the extracted payload
	for _, pat := range p.patterns {
		match := pat.regex.FindStringSubmatch(payload)
		if match != nil {
			names := pat.regex.SubexpNames()
			return pat.handler(match, names, cleaned)
		}
	}

	// 4. Unmatched Meshtastic echoes are text messages from the mesh
	if isMeshEcho {
		return p.parseTextEcho(cleaned, payload)
	}

	// 5. Raw/unmatched
	return []*ParsedEvent{{Kind: "raw", Raw: cleaned}}
}

// --- Echo detection and payload extraction ---

func (p *TextParser) isMeshtasticEcho(line string) bool {
	msgIdx := strings.LastIndex(strings.ToLower(line), "msg=")
	if msgIdx < 0 {
		return false
	}
	hasRouterTag := p.echoRouter.MatchString(line)
	hasTextMsg := p.echoTextMsg.MatchString(line)
	if !hasRouterTag && !hasTextMsg {
		return false
	}
	// Must also mention "Received text msg" or "textmessage"
	lower := strings.ToLower(line)
	return strings.Contains(lower, "received text msg") || strings.Contains(lower, "textmessage")
}

func (p *TextParser) extractPayload(line string, isMeshEcho bool) string {
	lower := strings.ToLower(line)
	msgIdx := strings.LastIndex(lower, "msg=")
	if isMeshEcho && msgIdx >= 0 {
		payload := strings.TrimSpace(line[msgIdx+4:])
		return p.normalizePayload(payload)
	}
	if msgIdx >= 0 {
		payload := strings.TrimSpace(line[msgIdx+4:])
		return p.normalizePayload(payload)
	}
	return p.normalizePayload(line)
}

func (p *TextParser) normalizePayload(raw string) string {
	// Normalize multi-line continuation patterns
	s := strings.ReplaceAll(raw, "\n Type:", " Type:")
	s = strings.ReplaceAll(s, "\n RSSI:", " RSSI:")
	s = strings.ReplaceAll(s, "\n GPS=", " GPS=")
	// Strip trailing hashes and leading "0m" residue
	s = p.trailingHash.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "0m") {
		s = strings.TrimSpace(s[2:])
	}
	return s
}

func (p *TextParser) parseTextEcho(rawLine, payload string) []*ParsedEvent {
	fromMatch := p.fromExtract.FindStringSubmatch(rawLine)
	nodeID := "unknown"
	if fromMatch != nil {
		hex := fromMatch[1]
		// Pad to 8 hex chars
		for len(hex) < 8 {
			hex = "0" + hex
		}
		nodeID = "!" + hex
	}

	// Extract Meshtastic packet ID from the echo line (e.g. "id=0x47f7")
	var packetID uint32
	if idMatch := p.idExtract.FindStringSubmatch(rawLine); idMatch != nil {
		if v, err := strconv.ParseUint(idMatch[1], 16, 32); err == nil {
			packetID = uint32(v)
		}
	}

	// Look up the cached "to" address from the preceding Lora RX debug line.
	// The echo line itself only has from= and id=, not to=.
	toNode := p.lookupPacketTo(packetID)

	return []*ParsedEvent{{
		Kind:     "text-message",
		NodeID:   nodeID,
		PacketID: packetID,
		ToNode:   toNode,
		Data:     map[string]interface{}{"text": payload},
		Raw:      rawLine,
	}}
}

func (p *TextParser) extractSourceID(text string) string {
	// Try node= pattern first
	nodeRe := regexp.MustCompile(`(?i)node[=:]([A-Za-z0-9_.:-]+)`)
	if m := nodeRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	// Fallback to first alphanumeric token
	if m := p.nodeIDFallback.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// --- Pattern handlers ---

func (p *TextParser) handleStatus(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{
		"mode": g["mode"],
		"scan": g["scan"],
		"hits": parseOptInt(g["hits"]),
	}

	if v, ok := g["targets"]; ok && v != "" {
		data["targets"] = parseOptInt(v)
	}
	if v := g["tempC"]; v != "" {
		data["temperatureC"] = parseOptFloat(v)
	}
	if v := g["tempF"]; v != "" {
		data["temperatureF"] = parseOptFloat(v)
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}
	if v := g["hdop"]; v != "" {
		data["hdop"] = parseOptFloat(v)
	}

	// AntiHunter firmware replies to a STATUS command with a plain "STATUS:"
	// frame rather than a STATUS_ACK, so without a synthetic ACK a pending
	// STATUS row in DigiNode CC would stay SENT forever. Emit a companion
	// command-ack event with ackType=STATUS_ACK so the matcher in the commands
	// service can close out the lifecycle. This is a small improvement over
	// CC PRO, which leaves plain STATUS queries permanently in SENT.
	statusAck := &ParsedEvent{
		Kind:   "command-ack",
		NodeID: nodeID,
		Data: map[string]interface{}{
			"ackType":     "STATUS_ACK",
			"status":      "OK",
			"synthesized": true,
		},
		Raw: raw,
	}

	// Promote tempC / lat / lon into a node-telemetry event so the Nodes UI
	// reflects the reading from a STATUS frame, not just from a heartbeat.
	// Without this, STATUS replies update the alert feed but leave the node
	// record's Temperature / Latitude / Longitude stale.
	telemetryData := map[string]interface{}{}
	if v, ok := data["temperatureC"]; ok {
		telemetryData["temperatureC"] = v
	}
	if v, ok := data["temperatureF"]; ok {
		telemetryData["temperatureF"] = v
	}
	if v, ok := data["lat"]; ok {
		telemetryData["lat"] = v
	}
	if v, ok := data["lon"]; ok {
		telemetryData["lon"] = v
	}

	// A STATUS frame is a query response, not an alert — it belongs in the
	// node-telemetry stream and as a synthetic STATUS_ACK. Adding it to the
	// alert feed would spam operators with routine heartbeat noise every time
	// a STATUS command fired. If a specific field warrants an alert in the
	// future (e.g. temperature over threshold), route that through the alert
	// rules engine instead.
	events := []*ParsedEvent{}
	if len(telemetryData) > 0 {
		events = append(events, &ParsedEvent{
			Kind:   "node-telemetry",
			NodeID: nodeID,
			Data:   telemetryData,
			Raw:    raw,
		})
	}
	events = append(events, statusAck)
	return events
}

func (p *TextParser) handleDrone(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	// Keys must match DroneDetection JSON tags in drones/detection.go
	data := map[string]interface{}{
		"uasId":     g["droneId"],
		"mac":       strings.ToUpper(g["mac"]),
		"rssi":      parseOptInt(g["rssi"]),
		"latitude":  parseOptFloat(g["lat"]),
		"longitude": parseOptFloat(g["lon"]),
	}
	if v := g["alt"]; v != "" {
		data["altitude"] = parseOptFloat(v)
	}
	if v := g["spd"]; v != "" {
		data["speed"] = parseOptFloat(v)
	}
	if v := g["opLat"]; v != "" {
		data["pilotLatitude"] = parseOptFloat(v)
	}
	if v := g["opLon"]; v != "" {
		data["pilotLongitude"] = parseOptFloat(v)
	}

	return []*ParsedEvent{{
		Kind:   "drone-telemetry",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}}
}

func (p *TextParser) handleTarget(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{
		"mac":  strings.ToUpper(g["mac"]),
		"rssi": parseOptInt(g["rssi"]),
		"type": g["type"],
	}
	if v := g["name"]; v != "" {
		data["name"] = v
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}

	detected := &ParsedEvent{
		Kind:   "target-detected",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}

	alert := &ParsedEvent{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "inventory",
		NodeID:   nodeID,
		Data:     copyMap(data),
		Raw:      raw,
	}

	return []*ParsedEvent{detected, alert}
}

func (p *TextParser) handleDevice(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	devType := normalizeBand(g["band"])

	data := map[string]interface{}{
		"mac":  strings.ToUpper(g["mac"]),
		"rssi": parseOptInt(g["rssi"]),
		"type": devType,
	}
	if v := g["channel"]; v != "" {
		data["channel"] = parseOptInt(v)
	}
	if v := g["name"]; v != "" {
		data["name"] = strings.TrimSpace(v)
	}

	return []*ParsedEvent{{
		Kind:   "target-detected",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}}
}

func (p *TextParser) handleAttackLong(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{
		"kind":    g["kind"],
		"src":     strings.ToUpper(g["src"]),
		"dst":     strings.ToUpper(g["dst"]),
		"rssi":    parseOptInt(g["rssi"]),
		"channel": parseOptInt(g["chan"]),
	}
	if v := g["mode"]; v != "" {
		data["mode"] = v
	}

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "ALERT",
		Category: "attack",
		NodeID:   nodeID,
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleAttackShort(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "ALERT",
		Category: "attack",
		NodeID:   nodeID,
		Data: map[string]interface{}{
			"kind":    g["kind"],
			"src":     strings.ToUpper(g["src"]),
			"dst":     strings.ToUpper(g["dst"]),
			"rssi":    parseOptInt(g["rssi"]),
			"channel": parseOptInt(g["chan"]),
		},
		Raw: raw,
	}}
}

func (p *TextParser) handleGPSLock(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	lat := parseOptFloat(g["lat"])
	lon := parseOptFloat(g["lon"])

	telemetry := &ParsedEvent{
		Kind:   "node-telemetry",
		NodeID: nodeID,
		Data: map[string]interface{}{
			"lat": lat,
			"lon": lon,
		},
		Raw: raw,
	}
	if v := g["sats"]; v != "" {
		telemetry.Data["sats"] = parseOptInt(v)
	}

	alertData := map[string]interface{}{
		"lat": lat,
		"lon": lon,
	}
	if v := g["sats"]; v != "" {
		alertData["sats"] = parseOptInt(v)
	}
	if v := g["hdop"]; v != "" {
		alertData["hdop"] = parseOptFloat(v)
	}

	alert := &ParsedEvent{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "gps",
		NodeID:   nodeID,
		Data:     alertData,
		Raw:      raw,
	}

	return []*ParsedEvent{telemetry, alert}
}

func (p *TextParser) handleGPSLost(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "gps",
		NodeID:   nodeID,
		Data:     map[string]interface{}{},
		Raw:      raw,
	}}
}

func (p *TextParser) handleNodeHB(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{}
	if v := g["time"]; v != "" {
		data["deviceTime"] = parseOptFloat(v)
	}
	if v := g["tempC"]; v != "" {
		data["temperatureC"] = parseOptFloat(v)
	}
	if v := g["tempF"]; v != "" {
		data["temperatureF"] = parseOptFloat(v)
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}

	telemetry := &ParsedEvent{
		Kind:   "node-telemetry",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}

	alert := &ParsedEvent{
		Kind:     "alert",
		Level:    "INFO",
		Category: "heartbeat",
		NodeID:   nodeID,
		Data:     copyMap(data),
		Raw:      raw,
	}

	return []*ParsedEvent{telemetry, alert}
}

func (p *TextParser) handleACK(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	status := g["status"]
	if status == "" {
		status = "OK"
	}

	return []*ParsedEvent{{
		Kind:   "command-ack",
		NodeID: nodeID,
		Data: map[string]interface{}{
			"ackType": g["kind"],
			"status":  status,
		},
		Raw: raw,
	}}
}

func (p *TextParser) handleAnomaly(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{
		"kind": g["anomKind"],
		"type": g["type"],
		"mac":  strings.ToUpper(g["mac"]),
	}
	if v := g["rssi"]; v != "" {
		data["rssi"] = parseOptInt(v)
	}
	if v := g["old"]; v != "" {
		data["old"] = parseOptInt(v)
	}
	if v := g["new"]; v != "" {
		data["new"] = parseOptInt(v)
	}
	if v := g["delta"]; v != "" {
		data["delta"] = parseOptInt(v)
	}
	if v := g["name"]; v != "" {
		data["name"] = v
	}

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "anomaly",
		NodeID:   nodeID,
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleStartup(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "startup",
		NodeID:   nodeID,
		Data:     map[string]interface{}{"message": g["msg"]},
		Raw:      raw,
	}}
}

func (p *TextParser) handleVibration(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}
	if v := g["erase"]; v != "" {
		data["eraseIn"] = parseOptInt(v)
	}

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "ALERT",
		Category: "vibration",
		NodeID:   nodeID,
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleTamper(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "ALERT",
		Category: "tamper",
		NodeID:   nodeID,
		Data:     map[string]interface{}{"kind": g["tamperKind"]},
		Raw:      raw,
	}}
}

// --- Camera alert handlers ---

func (p *TextParser) handleCamDetection(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{
		"camera": g["camera"],
		"label":  g["label"],
		"score":  parseOptInt(g["score"]),
	}
	if v := g["zone"]; v != "" {
		data["zone"] = v
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "camera",
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleCamFace(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{
		"camera": g["camera"],
		"name":   g["name"],
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "ALERT",
		Category: "camera-face",
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleCamPlate(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{
		"camera": g["camera"],
		"plate":  g["plate"],
	}
	if v := g["lat"]; v != "" {
		data["lat"] = parseOptFloat(v)
	}
	if v := g["lon"]; v != "" {
		data["lon"] = parseOptFloat(v)
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "camera-plate",
		Data:     data,
		Raw:      raw,
	}}
}

// --- Triangulation event handlers ---

func (p *TextParser) handleTriData(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]
	mac := strings.ToUpper(g["mac"])
	data := map[string]interface{}{
		"mac":     mac,
		"rssi":    parseOptInt(g["rssi"]),
		"hits":    parseOptInt(g["hits"]),
		"type":    g["type"],
		"nodeLat": parseOptFloat(g["lat"]),
		"nodeLon": parseOptFloat(g["lon"]),
	}
	return []*ParsedEvent{{
		Kind:   "tri-data",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}}
}

func (p *TextParser) handleTriFinal(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]
	mac := strings.ToUpper(g["mac"])
	conf := parseOptFloat(g["conf"])
	if conf > 1 {
		conf = conf / 100.0 // CC PRO sends 0-100, normalize to 0-1
	}
	data := map[string]interface{}{
		"mac":         mac,
		"lat":         parseOptFloat(g["lat"]),
		"lon":         parseOptFloat(g["lon"]),
		"confidence":  conf,
		"uncertainty": parseOptFloat(g["unc"]),
	}
	return []*ParsedEvent{{
		Kind:   "tri-final",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}}
}

func (p *TextParser) handleTriComplete(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]
	data := map[string]interface{}{
		"mac":   strings.ToUpper(g["mac"]),
		"nodes": parseOptInt(g["nodes"]),
	}
	return []*ParsedEvent{{
		Kind:   "tri-complete",
		NodeID: nodeID,
		Data:   data,
		Raw:    raw,
	}}
}

// --- Helpers ---

// extractGroups converts regex named captures into a string map.
func extractGroups(match []string, names []string) map[string]string {
	groups := make(map[string]string)
	for i, name := range names {
		if name != "" && i < len(match) {
			groups[name] = match[i]
		}
	}
	return groups
}

// parseOptInt parses a string to int, returning 0 on failure.
func parseOptInt(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// parseOptFloat parses a string to float64, returning 0 on failure.
func parseOptFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// normalizeBand converts single-letter band codes to full names.
func normalizeBand(band string) string {
	switch strings.ToUpper(band) {
	case "W":
		return "WiFi"
	case "B":
		return "BLE"
	default:
		return band
	}
}

// copyMap returns a shallow copy of a map.
func copyMap(m map[string]interface{}) map[string]interface{} {
	cp := make(map[string]interface{}, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// cleanNonPrintable removes non-printable characters (keeping tab, newline, CR, and >= 0x20).
func cleanNonPrintable(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseNodeNum converts a node ID string like "!0043a605" to a uint32.
func parseNodeNum(nodeID string) uint32 {
	if strings.HasPrefix(nodeID, "!") {
		nodeID = nodeID[1:]
	}
	n, _ := strconv.ParseUint(nodeID, 16, 32)
	return uint32(n)
}

// ExtractMeshFrom pulls the mesh node number out of a Meshtastic debug echo
// line like "[Router] Received text msg from=0x0406a7a8 ...". AntiHunter
// STATUS / heartbeat payloads are prefixed with the sensor's CONFIG_NODEID
// (e.g. "AH34"), which carries no mesh addressing, so the telemetry dispatcher
// needs this as a fallback when the regex-captured NodeID can't be parsed as
// a hex mesh number.
func (p *TextParser) ExtractMeshFrom(raw string) uint32 {
	if raw == "" || p.fromExtract == nil {
		return 0
	}
	m := p.fromExtract.FindStringSubmatch(raw)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseUint(m[1], 16, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// ParseNodeNum is the exported version of parseNodeNum.
func ParseNodeNum(nodeID string) uint32 {
	return parseNodeNum(nodeID)
}

// nodeIDHex formats a uint32 node number as a "!XXXXXXXX" hex string.
func nodeIDHex(num uint32) string {
	return fmt.Sprintf("!%08x", num)
}

// kvBodyRe parses "Key=Value" and "Key:Value" pairs out of a done-summary or
// status body. Values stop at whitespace; keys are alphanumeric + underscore.
var kvBodyRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_]*)\s*[=:]\s*([^\s]+)`)

// parseKVBody extracts all Key=Value / Key:Value pairs from `body` into a map,
// converting numeric-looking values to int or float when possible. Used for
// SCAN_DONE, DEAUTH_DONE, BASELINE_STATUS, etc. which all follow the same shape.
func parseKVBody(body string) map[string]interface{} {
	out := map[string]interface{}{}
	for _, m := range kvBodyRe.FindAllStringSubmatch(body, -1) {
		key := strings.ToLower(m[1])
		val := m[2]
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			out[key] = i
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			out[key] = f
			continue
		}
		out[key] = val
	}
	return out
}

// handleDoneSummary builds a pattern handler that parses a trailing KV body
// (e.g. "W=42 B=15 U=30 Targets=5") and emits a single alert event tagged with
// the given category. Used for every *_DONE / *_STATUS line AntiHunter emits.
func (p *TextParser) handleDoneSummary(category string) func([]string, []string, string) []*ParsedEvent {
	return func(match []string, names []string, raw string) []*ParsedEvent {
		g := extractGroups(match, names)
		data := parseKVBody(g["body"])
		return []*ParsedEvent{{
			Kind:     "alert",
			Level:    "INFO",
			Category: category,
			NodeID:   g["id"],
			Data:     data,
			Raw:      raw,
		}}
	}
}

func (p *TextParser) handleIdentity(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{
		"identityId": g["identityId"],
		"band":       g["band"],
		"rssi":       parseOptInt(g["rssi"]),
	}
	if v := g["hits"]; v != "" {
		data["hits"] = parseOptInt(v)
	}
	if v := g["macs"]; v != "" {
		data["macs"] = parseOptInt(v)
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "identity",
		NodeID:   g["id"],
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleSetupMode(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "setup-mode",
		NodeID:   g["id"],
		Data:     map[string]interface{}{"message": g["msg"]},
		Raw:      raw,
	}}
}

func (p *TextParser) handleBatterySaverState(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{
		"state": strings.ToUpper(g["state"]),
	}
	if v := g["interval"]; v != "" {
		data["interval"] = v
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "INFO",
		Category: "battery-saver",
		NodeID:   g["id"],
		Data:     data,
		Raw:      raw,
	}}
}

// handleEraseEvent returns a handler that emits a tamper-category alert at the
// given level. Used for ERASE_EXECUTING and ERASE_COMPLETE, both of which signal
// secure-erase state transitions that operators must see immediately.
func (p *TextParser) handleEraseEvent(kind, level string) func([]string, []string, string) []*ParsedEvent {
	return func(match []string, names []string, raw string) []*ParsedEvent {
		g := extractGroups(match, names)
		data := map[string]interface{}{"kind": kind}
		if v := g["msg"]; v != "" {
			data["message"] = v
		}
		if v := g["lat"]; v != "" {
			data["lat"] = parseOptFloat(v)
		}
		if v := g["lon"]; v != "" {
			data["lon"] = parseOptFloat(v)
		}
		return []*ParsedEvent{{
			Kind:     "alert",
			Level:    level,
			Category: "erase",
			NodeID:   g["id"],
			Data:     data,
			Raw:      raw,
		}}
	}
}

func (p *TextParser) handleEraseToken(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	data := map[string]interface{}{"token": g["token"]}
	if v := g["expires"]; v != "" {
		data["expiresSeconds"] = parseOptInt(v)
	}
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "CRITICAL",
		Category: "erase-token",
		NodeID:   g["id"],
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleRTCSync(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "INFO",
		Category: "rtc-sync",
		NodeID:   g["id"],
		Data:     map[string]interface{}{"source": g["source"]},
		Raw:      raw,
	}}
}

// handleBroadcastCommand returns a handler that captures @ALL mesh commands
// (TRIANGULATE_START, TRI_CYCLE_START, TRIANGULATE_STOP, SYNC). There's no
// node-id prefix — these are relayed commands — so the event has an empty
// NodeID and all matched groups go straight into Data.
func (p *TextParser) handleBroadcastCommand(category string) func([]string, []string, string) []*ParsedEvent {
	return func(match []string, names []string, raw string) []*ParsedEvent {
		g := extractGroups(match, names)
		data := map[string]interface{}{}
		for k, v := range g {
			if v == "" {
				continue
			}
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				data[k] = i
				continue
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				data[k] = f
				continue
			}
			data[k] = v
		}
		return []*ParsedEvent{{
			Kind:     "alert",
			Level:    "INFO",
			Category: "mesh-command:" + category,
			Data:     data,
			Raw:      raw,
		}}
	}
}
