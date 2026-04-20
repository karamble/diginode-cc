package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CommandDef defines a command type with its validation rules.
type CommandDef struct {
	Name         string
	Group        string
	Description  string
	Params       []ParamDef
	AllowForever bool     // supports FOREVER token as last param
	SingleNode   bool     // cannot target @ALL
	// SupportedTypes lists the nodes.NodeType values that accept this command.
	// "*" means universal (any node type, including unknown/@ALL broadcasts).
	// Empty slice defaults to antihunter-only for back-compat with any legacy
	// entry that doesn't declare its types.
	SupportedTypes []string
}

// ParamDef defines a single parameter for a command.
type ParamDef struct {
	Key         string
	Label       string
	Type        string // "text", "number", "select", "duration", "channels", "pipeList", "mac"
	Required    bool
	Min         float64
	Max         float64
	Options     []string // for select type
	Placeholder string
}

// BuildOutput is the result of building a command.
type BuildOutput struct {
	Target string   `json:"target"`
	Name   string   `json:"name"`
	Params []string `json:"params"`
	Line   string   `json:"line"` // formatted mesh text line
}

var targetRegex = regexp.MustCompile(`^@(ALL|NODE_[A-Za-z0-9]+|[A-Za-z0-9]{2,6})$`)
var macRegex = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)
var gateNameRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{1,15}$`)

// typeUniversal / typeAH / typeGate are SupportedTypes shorthands.
var (
	typeUniversal = []string{"*"}
	typeAH        = []string{"antihunter"}
	typeGate      = []string{"gatesensor"}
)

// Registry holds all known command definitions.
var Registry = map[string]*CommandDef{
	// Status (universal STATUS + AntiHunter-specific status queries)
	"STATUS":           {Name: "STATUS", Group: "Status", Description: "Request node status report", SupportedTypes: typeUniversal},
	"BASELINE_STATUS":  {Name: "BASELINE_STATUS", Group: "Status", Description: "Request baseline scan status", SupportedTypes: typeAH},
	"VIBRATION_STATUS": {Name: "VIBRATION_STATUS", Group: "Status", Description: "Request vibration sensor status", SupportedTypes: typeAH},
	"VIBRATION_OFF":    {Name: "VIBRATION_OFF", Group: "Status", Description: "Disable vibration TEXTMSG broadcasts (detection still runs locally)", SupportedTypes: typeAH},
	"VIBRATION_ON":     {Name: "VIBRATION_ON", Group: "Status", Description: "Enable vibration TEXTMSG broadcasts", SupportedTypes: typeAH},

	// Scanning (AntiHunter-only)
	"SCAN_START": {Name: "SCAN_START", Group: "Scanning", Description: "Start WiFi/BLE scanning", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}, Placeholder: "0=WiFi 1=BLE 2=Both"},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
		{Key: "channels", Label: "Channels", Type: "channels", Placeholder: "1,6,11 or 1..14"},
	}},
	"SCAN_STOP": {Name: "SCAN_STOP", Group: "Scanning", Description: "Stop scanning", SupportedTypes: typeAH},
	"DEVICE_SCAN_START": {Name: "DEVICE_SCAN_START", Group: "Scanning", Description: "Start device scan", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DEVICE_SCAN_STOP": {Name: "DEVICE_SCAN_STOP", Group: "Scanning", Description: "Stop device scan", SupportedTypes: typeAH},
	"STOP":             {Name: "STOP", Group: "Scanning", Description: "Stop all scanning activities", SupportedTypes: typeAH},

	// Detection (AntiHunter-only)
	"DRONE_START": {Name: "DRONE_START", Group: "Detection", Description: "Start drone detection", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DRONE_STOP": {Name: "DRONE_STOP", Group: "Detection", Description: "Stop drone detection", SupportedTypes: typeAH},
	"DEAUTH_START": {Name: "DEAUTH_START", Group: "Detection", Description: "Start deauth detection", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DEAUTH_STOP": {Name: "DEAUTH_STOP", Group: "Detection", Description: "Stop deauth detection", SupportedTypes: typeAH},
	"RANDOMIZATION_START": {Name: "RANDOMIZATION_START", Group: "Detection", Description: "Start MAC randomization detection", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"RANDOMIZATION_STOP": {Name: "RANDOMIZATION_STOP", Group: "Detection", Description: "Stop randomization detection", SupportedTypes: typeAH},
	"BASELINE_START": {Name: "BASELINE_START", Group: "Detection", Description: "Start baseline environment scan", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"BASELINE_STOP": {Name: "BASELINE_STOP", Group: "Detection", Description: "Stop baseline scan", SupportedTypes: typeAH},

	// Triangulation (AntiHunter-only)
	"TRIANGULATE_START": {Name: "TRIANGULATE_START", Group: "Triangulation", Description: "Start triangulation of a target", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "target", Label: "Target MAC", Type: "mac", Required: true, Placeholder: "AA:BB:CC:DD:EE:FF"},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 20, Max: 300},
		{Key: "rfEnv", Label: "RF Environment", Type: "select", Required: true, Options: []string{"0", "1", "2", "3", "4"}, Placeholder: "0=Open 1=Suburban 2=Indoor 3=Dense 4=Industrial"},
		{Key: "wifiPwr", Label: "WiFi Power", Type: "number", Min: 0.1, Max: 5.0, Placeholder: "1.5"},
		{Key: "blePwr", Label: "BLE Power", Type: "number", Min: 0.1, Max: 5.0, Placeholder: "0.8"},
	}},
	"TRIANGULATE_STOP":    {Name: "TRIANGULATE_STOP", Group: "Triangulation", Description: "Stop triangulation", SupportedTypes: typeAH},
	"TRIANGULATE_RESULTS": {Name: "TRIANGULATE_RESULTS", Group: "Triangulation", Description: "Request triangulation results", SupportedTypes: typeAH},

	// Configuration (AntiHunter-only)
	"CONFIG_CHANNELS": {Name: "CONFIG_CHANNELS", Group: "Configuration", Description: "Configure scan channels", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "channels", Label: "Channels", Type: "channels", Required: true, Placeholder: "1,6,11 or 1..14"},
	}},
	"CONFIG_TARGETS": {Name: "CONFIG_TARGETS", Group: "Configuration", Description: "Configure target MACs", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "targets", Label: "Target MACs", Type: "pipeList", Required: true, Placeholder: "AA:BB:CC:DD:EE:FF|11:22:33:44:55:66"},
	}},
	"CONFIG_RSSI": {Name: "CONFIG_RSSI", Group: "Configuration", Description: "Configure RSSI threshold", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "rssi", Label: "RSSI Threshold", Type: "number", Required: true, Min: -120, Max: -1},
	}},
	"CONFIG_NODEID": {Name: "CONFIG_NODEID", Group: "Configuration", Description: "Set node ID (AH + 1–3 digits, e.g. AH07)", SingleNode: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "nodeId", Label: "Node ID", Type: "text", Required: true, Placeholder: "AH07"},
	}},

	// Security / Erase (AntiHunter-only)
	"ERASE_REQUEST": {Name: "ERASE_REQUEST", Group: "Security", Description: "Request erase token from node", SupportedTypes: typeAH},
	"ERASE_FORCE": {Name: "ERASE_FORCE", Group: "Security", Description: "Force erase with token", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "token", Label: "Erase Token", Type: "text", Required: true, Placeholder: "AH_XXXXXXXX_XXXXXXXX_XXXXXXXX"},
	}},
	"ERASE_CANCEL": {Name: "ERASE_CANCEL", Group: "Security", Description: "Cancel pending erase", SupportedTypes: typeAH},
	"AUTOERASE_ENABLE": {Name: "AUTOERASE_ENABLE", Group: "Security", Description: "Enable auto-erase on tamper", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "setupDelay", Label: "Setup Delay (sec)", Type: "number", Min: 30, Max: 600},
		{Key: "eraseDelay", Label: "Erase Delay (sec)", Type: "number", Min: 10, Max: 300},
		{Key: "vibs", Label: "Vibration Count", Type: "number", Min: 2, Max: 5},
		{Key: "window", Label: "Window (sec)", Type: "number", Min: 10, Max: 60},
		{Key: "cooldown", Label: "Cooldown (sec)", Type: "number", Min: 300, Max: 3600},
	}},
	"AUTOERASE_DISABLE": {Name: "AUTOERASE_DISABLE", Group: "Security", Description: "Disable auto-erase", SupportedTypes: typeAH},
	"AUTOERASE_STATUS":  {Name: "AUTOERASE_STATUS", Group: "Security", Description: "Check auto-erase status", SupportedTypes: typeAH},

	// Battery (AntiHunter-only)
	"BATTERY_SAVER_START": {Name: "BATTERY_SAVER_START", Group: "Battery", Description: "Start battery saver mode", AllowForever: true, SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "interval", Label: "Interval (min)", Type: "number", Required: true, Min: 1, Max: 1440},
	}},
	"BATTERY_SAVER_STOP":   {Name: "BATTERY_SAVER_STOP", Group: "Battery", Description: "Stop battery saver", SupportedTypes: typeAH},
	"BATTERY_SAVER_STATUS": {Name: "BATTERY_SAVER_STATUS", Group: "Battery", Description: "Check battery saver status", SupportedTypes: typeAH},

	// System (universal)
	"REBOOT": {Name: "REBOOT", Group: "System", Description: "Reboot node", SupportedTypes: typeUniversal},

	// Heartbeat control — supported by both antihunter and gatesensor firmware
	"HB_ON":  {Name: "HB_ON", Group: "Status", Description: "Enable periodic status heartbeats", SupportedTypes: typeUniversal},
	"HB_OFF": {Name: "HB_OFF", Group: "Status", Description: "Disable periodic status heartbeats", SupportedTypes: typeUniversal},
	"HB_INTERVAL": {Name: "HB_INTERVAL", Group: "Status", Description: "Set heartbeat interval", SupportedTypes: typeUniversal, Params: []ParamDef{
		{Key: "minutes", Label: "Interval (min)", Type: "number", Required: true, Min: 1, Max: 60},
	}},

	// Triangulation coordinator-only sync pulse (AntiHunter-only)
	"TRI_CYCLE_START": {Name: "TRI_CYCLE_START", Group: "Triangulation", Description: "Sync participant scan cycle (coordinator only)", SupportedTypes: typeAH, Params: []ParamDef{
		{Key: "intervalMs", Label: "Interval (ms)", Type: "number", Required: true, Min: 1000, Max: 60000},
		{Key: "nodes", Label: "Node list", Type: "text", Placeholder: "NODE1,NODE2,NODE3"},
	}},

	// Gate sensor (meshtastic-gate-sensor Arduino firmware)
	"HITS_RESET": {Name: "HITS_RESET", Group: "Gate", Description: "Reset the cumulative hit counter", SupportedTypes: typeGate},
	"DEBOUNCE_SET": {Name: "DEBOUNCE_SET", Group: "Gate", Description: "Set RF debounce window (seconds)", SupportedTypes: typeGate, Params: []ParamDef{
		{Key: "seconds", Label: "Seconds", Type: "number", Required: true, Min: 1, Max: 60},
	}},
	"CODE_ADD": {Name: "CODE_ADD", Group: "Gate", Description: "Register (or rename) a 433 MHz sensor code", SupportedTypes: typeGate, Params: []ParamDef{
		{Key: "code", Label: "Code (decimal)", Type: "number", Required: true, Min: 1, Max: 16777215},
		{Key: "name", Label: "Gate name (optional)", Type: "text", Placeholder: "factorydoor"},
	}},
	"CODE_REMOVE": {Name: "CODE_REMOVE", Group: "Gate", Description: "Unregister a 433 MHz sensor code", SupportedTypes: typeGate, Params: []ParamDef{
		{Key: "code", Label: "Code (decimal)", Type: "number", Required: true, Min: 1, Max: 16777215},
	}},
	"CODE_LIST":  {Name: "CODE_LIST", Group: "Gate", Description: "List registered 433 MHz sensor codes", SupportedTypes: typeGate},
	"CODE_CLEAR": {Name: "CODE_CLEAR", Group: "Gate", Description: "Clear all registered 433 MHz sensor codes", SupportedTypes: typeGate},
}

// ACKMap maps an AntiHunter ACK frame name to the command it acknowledges.
// The firmware emits exactly these ACK types — see the ack pattern in
// internal/serial/textparser.go:188. CONFIG commands all ack as CONFIG_ACK
// (with the kind embedded in the status field), so they collapse to a single
// map entry and the matcher picks the most recent pending CONFIG_* row.
var ACKMap = map[string]string{
	"SCAN_ACK":             "SCAN_START",
	"DEVICE_SCAN_ACK":      "DEVICE_SCAN_START",
	"DRONE_ACK":            "DRONE_START",
	"DEAUTH_ACK":           "DEAUTH_START",
	"RANDOMIZATION_ACK":    "RANDOMIZATION_START",
	"BASELINE_ACK":         "BASELINE_START",
	"TRI_START_ACK":        "TRIANGULATE_START",
	"TRIANGULATE_ACK":      "TRIANGULATE_START", // back-compat alias
	"TRIANGULATE_STOP_ACK": "TRIANGULATE_STOP",
	"ERASE_ACK":            "ERASE_FORCE",
	"CONFIG_ACK":           "CONFIG_CHANNELS", // latest CONFIG_* pending wins
	"AUTOERASE_ACK":        "AUTOERASE_ENABLE",
	"AUTOERASE_STATUS_ACK": "AUTOERASE_STATUS",
	"BATTERY_SAVER_ACK":    "BATTERY_SAVER_START",
	"HB_ACK":               "HB_ON",
	"STOP_ACK":             "STOP",
	"REBOOT_ACK":           "REBOOT",
	// STATUS_ACK is synthesized by the textparser from the plain STATUS: reply
	// frame (the firmware never emits a real *_ACK for STATUS). Lets STATUS
	// commands close out as OK instead of sitting in SENT forever.
	"STATUS_ACK":        "STATUS",
	"VIBRATION_OFF_ACK": "VIBRATION_OFF",
	"VIBRATION_ON_ACK":  "VIBRATION_ON",
	// Gate-sensor ACKs. CODE_* all share CODE_ACK — latest pending CODE_* wins
	// via the same most-recent-pending pattern used by CONFIG_ACK.
	// CODE_LIST is a special case: the firmware replies with a plain CODES:
	// frame (no real ACK), so textparser.handleCodes synthesizes a
	// CODE_LIST_ACK so the command lifecycle closes — same pattern as
	// STATUS_ACK for STATUS queries.
	"HITS_RESET_ACK": "HITS_RESET",
	"DEBOUNCE_ACK":   "DEBOUNCE_SET",
	"CODE_ACK":       "CODE_ADD",
	"CODE_LIST_ACK":  "CODE_LIST",
}

// Build validates inputs and produces a formatted mesh text line.
func Build(target, name string, params []string, forever bool) (*BuildOutput, error) {
	// Normalize
	target = strings.ToUpper(strings.TrimSpace(target))
	name = strings.ToUpper(strings.TrimSpace(name))

	// Validate target
	if !strings.HasPrefix(target, "@") {
		target = "@" + target
	}
	if !targetRegex.MatchString(target) {
		return nil, fmt.Errorf("invalid target %q: must be @ALL, @NODE_<id>, or @<shortid>", target)
	}

	// Lookup command definition
	def, ok := Registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown command %q", name)
	}

	// Single-node check
	if def.SingleNode && target == "@ALL" {
		return nil, fmt.Errorf("command %s cannot target @ALL", name)
	}

	// Trim empty trailing params
	for len(params) > 0 && strings.TrimSpace(params[len(params)-1]) == "" {
		params = params[:len(params)-1]
	}

	// Validate required params
	for i, pd := range def.Params {
		if !pd.Required {
			continue
		}
		if i >= len(params) || strings.TrimSpace(params[i]) == "" {
			return nil, fmt.Errorf("missing required parameter %q for %s", pd.Key, name)
		}
	}

	// Validate param values
	cleanParams := make([]string, 0, len(params))
	for i, val := range params {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if i < len(def.Params) {
			pd := def.Params[i]
			if err := validateParam(pd, val); err != nil {
				return nil, fmt.Errorf("param %q: %w", pd.Key, err)
			}
		}
		cleanParams = append(cleanParams, val)
	}

	// Append FOREVER if requested and allowed
	if forever && def.AllowForever {
		cleanParams = append(cleanParams, "FOREVER")
	}

	// Build line: {target} {name}:{p1}:{p2}:...
	line := target + " " + name
	if len(cleanParams) > 0 {
		line += ":" + strings.Join(cleanParams, ":")
	}

	return &BuildOutput{
		Target: target,
		Name:   name,
		Params: cleanParams,
		Line:   line,
	}, nil
}

func validateParam(pd ParamDef, val string) error {
	switch pd.Type {
	case "number", "duration":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("must be a number")
		}
		if pd.Min != 0 || pd.Max != 0 {
			if f < pd.Min || f > pd.Max {
				return fmt.Errorf("must be between %.0f and %.0f", pd.Min, pd.Max)
			}
		}
	case "mac":
		if !macRegex.MatchString(strings.ToUpper(val)) {
			return fmt.Errorf("invalid MAC format (expected XX:XX:XX:XX:XX:XX)")
		}
	case "channels":
		if err := validateChannels(val); err != nil {
			return err
		}
	case "select":
		found := false
		for _, opt := range pd.Options {
			if val == opt {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("must be one of: %s", strings.Join(pd.Options, ", "))
		}
	case "text":
		if pd.Key == "nodeId" {
			// AntiHunter firmware's sanitizeNodeId (hardware.cpp) forces an "AH"
			// prefix + digits and silently mutates anything else, so only strings
			// shaped AH + 1–3 digits round-trip without surprise. The firmware's
			// own validator accepts any 2–5 alphanumeric chars, but the sanitize
			// step strips letters after position 2 and pads with random digits —
			// meaning "NODE1" becomes something like "AH1XX". Reject early here
			// to keep the UI honest.
			upper := strings.ToUpper(val)
			matched, _ := regexp.MatchString(`^AH\d{1,3}$`, upper)
			if !matched {
				return fmt.Errorf(`node ID must match "AH" + 1–3 digits (e.g. AH07, AH123)`)
			}
		}
		if pd.Key == "name" {
			// Mirror the gate-sensor firmware's nameValid() in firmware/src/main.cpp:
			// 1–15 chars of [A-Za-z0-9_-]. The CMD wire format uses ':' and space
			// as delimiters, so anything else would desync the parser.
			if !gateNameRegex.MatchString(val) {
				return fmt.Errorf("gate name must be 1-15 chars of letters, digits, '_' or '-'")
			}
		}
	}
	return nil
}

func validateChannels(val string) error {
	// Support range (1..14) or CSV (1,6,11)
	if strings.Contains(val, "..") {
		parts := strings.SplitN(val, "..", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid range format (use start..end)")
		}
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || start < 1 || end > 14 || start > end {
			return fmt.Errorf("channels must be 1-14")
		}
		return nil
	}
	for _, ch := range strings.Split(val, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(ch))
		if err != nil || n < 1 || n > 14 {
			return fmt.Errorf("channel %q out of range 1-14", ch)
		}
	}
	return nil
}

// GroupedCommands returns command definitions grouped by category.
func GroupedCommands() map[string][]*CommandDef {
	groups := make(map[string][]*CommandDef)
	for _, def := range Registry {
		groups[def.Group] = append(groups[def.Group], def)
	}
	return groups
}

// GroupOrder defines the display order of command groups.
var GroupOrder = []string{"Status", "Scanning", "Detection", "Triangulation", "Configuration", "Security", "Battery", "System", "Gate"}
