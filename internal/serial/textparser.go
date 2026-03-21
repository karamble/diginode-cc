package serial

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParsedEvent represents a parsed serial text line.
type ParsedEvent struct {
	Kind     string                 // "node-telemetry", "target-detected", "alert", "command-ack", "drone-telemetry", "text-message", "raw"
	NodeID   string                 // Source node ID
	PacketID uint32                 // Meshtastic packet ID (extracted from echo line, 0 if unavailable)
	Data     map[string]interface{} // Parsed fields
	Raw      string                 // Original line
	Category string                 // For alerts: "status", "gps", "attack", etc.
	Level    string                 // For alerts: "INFO", "NOTICE", "ALERT", "CRITICAL"
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

	// Payload normalization helpers
	nodeIDFallback *regexp.Regexp
	trailingHash   *regexp.Regexp
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
		ansiClean:      regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`),
		nodeIDFallback: regexp.MustCompile(`^([A-Za-z0-9_.:-]+)`),
		trailingHash:   regexp.MustCompile(`#+$`),
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
		// NODE_HB inline: "nodeId: Time:12345 Temp:38c/100F GPS:12.34,56.78"
		{
			name: "node-hb-inline",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):?\s*Time:(?P<time>[^ ]+)\s+Temp:(?P<tempC>-?\d+(?:\.\d+)?)(?:[cCfF])?(?:/(?P<tempF>-?\d+(?:\.\d+)?)[fF])?(?:\s+GPS:(?P<lat>-?\d+(?:\.\d+)?),(?P<lon>-?\d+(?:\.\d+)?))?`),
			handler: p.handleNodeHB,
		},
		// ACK lines: "nodeId: SCAN_ACK:OK" / "nodeId: DRONE_ACK:" etc.
		{
			name: "ack",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*(?P<kind>(?:SCAN|DEVICE_SCAN|DRONE|DEAUTH|RANDOMIZATION|BASELINE|CONFIG|TRIANGULATE(?:_STOP)?|TRI_START|STOP|REBOOT|BATTERY_SAVER(?:_START|_STOP)?)_ACK):?(?P<status>[A-Z_]*)`),
			handler: p.handleACK,
		},
		// ANOMALY: "nodeId: ANOMALY-NEW: WiFi AA:BB:CC:DD:EE:FF RSSI:-60 Name:test"
		{
			name: "anomaly",
			regex: regexp.MustCompile(
				`(?i)^(?P<id>[A-Za-z0-9_.:-]+):\s*ANOMALY-(?P<anomKind>NEW|RETURN|RSSI):\s*(?P<type>\w+)\s+(?P<mac>(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})(?:\s+RSSI:(?P<rssi>-?\d+))?(?:\s+Old:(?P<old>-?\d+)\s+New:(?P<new>-?\d+)\s+Delta:(?P<delta>-?\d+))?(?:\s+Name:(?P<name>[^ ]+))?`),
			handler: p.handleAnomaly,
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
	}
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

	return []*ParsedEvent{{
		Kind:     "text-message",
		NodeID:   nodeID,
		PacketID: packetID,
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

	return []*ParsedEvent{{
		Kind:     "alert",
		Level:    "NOTICE",
		Category: "status",
		NodeID:   nodeID,
		Data:     data,
		Raw:      raw,
	}}
}

func (p *TextParser) handleDrone(match []string, names []string, raw string) []*ParsedEvent {
	g := extractGroups(match, names)
	nodeID := g["id"]

	data := map[string]interface{}{
		"droneId": g["droneId"],
		"mac":     strings.ToUpper(g["mac"]),
		"rssi":    parseOptInt(g["rssi"]),
		"lat":     parseOptFloat(g["lat"]),
		"lon":     parseOptFloat(g["lon"]),
	}
	if v := g["alt"]; v != "" {
		data["altitude"] = parseOptFloat(v)
	}
	if v := g["spd"]; v != "" {
		data["speed"] = parseOptFloat(v)
	}
	if v := g["opLat"]; v != "" {
		data["operatorLat"] = parseOptFloat(v)
	}
	if v := g["opLon"]; v != "" {
		data["operatorLon"] = parseOptFloat(v)
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

// ParseNodeNum is the exported version of parseNodeNum.
func ParseNodeNum(nodeID string) uint32 {
	return parseNodeNum(nodeID)
}

// nodeIDHex formats a uint32 node number as a "!XXXXXXXX" hex string.
func nodeIDHex(num uint32) string {
	return fmt.Sprintf("!%08x", num)
}
