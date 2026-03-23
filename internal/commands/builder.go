package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CommandDef defines a command type with its validation rules.
type CommandDef struct {
	Name        string
	Group       string
	Description string
	Params      []ParamDef
	AllowForever bool   // supports FOREVER token as last param
	SingleNode   bool   // cannot target @ALL
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

// Registry holds all known command definitions.
var Registry = map[string]*CommandDef{
	// Status
	"STATUS":           {Name: "STATUS", Group: "Status", Description: "Request node status report"},
	"BASELINE_STATUS":  {Name: "BASELINE_STATUS", Group: "Status", Description: "Request baseline scan status"},
	"VIBRATION_STATUS": {Name: "VIBRATION_STATUS", Group: "Status", Description: "Request vibration sensor status"},

	// Scanning
	"SCAN_START": {Name: "SCAN_START", Group: "Scanning", Description: "Start WiFi/BLE scanning", AllowForever: true, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}, Placeholder: "0=WiFi 1=BLE 2=Both"},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
		{Key: "channels", Label: "Channels", Type: "channels", Placeholder: "1,6,11 or 1..14"},
	}},
	"SCAN_STOP":        {Name: "SCAN_STOP", Group: "Scanning", Description: "Stop scanning"},
	"DEVICE_SCAN_START": {Name: "DEVICE_SCAN_START", Group: "Scanning", Description: "Start device scan", AllowForever: true, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DEVICE_SCAN_STOP": {Name: "DEVICE_SCAN_STOP", Group: "Scanning", Description: "Stop device scan"},
	"STOP":             {Name: "STOP", Group: "Scanning", Description: "Stop all scanning activities"},

	// Detection
	"DRONE_START": {Name: "DRONE_START", Group: "Detection", Description: "Start drone detection", AllowForever: true, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DRONE_STOP":  {Name: "DRONE_STOP", Group: "Detection", Description: "Stop drone detection"},
	"DEAUTH_START": {Name: "DEAUTH_START", Group: "Detection", Description: "Start deauth detection", AllowForever: true, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"DEAUTH_STOP": {Name: "DEAUTH_STOP", Group: "Detection", Description: "Stop deauth detection"},
	"RANDOMIZATION_START": {Name: "RANDOMIZATION_START", Group: "Detection", Description: "Start MAC randomization detection", AllowForever: true, Params: []ParamDef{
		{Key: "mode", Label: "Mode", Type: "select", Required: true, Options: []string{"0", "1", "2"}},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"RANDOMIZATION_STOP": {Name: "RANDOMIZATION_STOP", Group: "Detection", Description: "Stop randomization detection"},
	"BASELINE_START": {Name: "BASELINE_START", Group: "Detection", Description: "Start baseline environment scan", AllowForever: true, Params: []ParamDef{
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 1, Max: 86400},
	}},
	"BASELINE_STOP": {Name: "BASELINE_STOP", Group: "Detection", Description: "Stop baseline scan"},

	// Triangulation
	"TRIANGULATE_START": {Name: "TRIANGULATE_START", Group: "Triangulation", Description: "Start triangulation of a target", AllowForever: true, Params: []ParamDef{
		{Key: "target", Label: "Target MAC", Type: "mac", Required: true, Placeholder: "AA:BB:CC:DD:EE:FF"},
		{Key: "duration", Label: "Duration (sec)", Type: "duration", Required: true, Min: 20, Max: 300},
		{Key: "rfEnv", Label: "RF Environment", Type: "select", Required: true, Options: []string{"0", "1", "2", "3", "4"}, Placeholder: "0=Open 1=Suburban 2=Indoor 3=Dense 4=Industrial"},
		{Key: "wifiPwr", Label: "WiFi Power", Type: "number", Min: 0.1, Max: 5.0, Placeholder: "1.5"},
		{Key: "blePwr", Label: "BLE Power", Type: "number", Min: 0.1, Max: 5.0, Placeholder: "0.8"},
	}},
	"TRIANGULATE_STOP":    {Name: "TRIANGULATE_STOP", Group: "Triangulation", Description: "Stop triangulation"},
	"TRIANGULATE_RESULTS": {Name: "TRIANGULATE_RESULTS", Group: "Triangulation", Description: "Request triangulation results"},

	// Configuration
	"CONFIG_CHANNELS": {Name: "CONFIG_CHANNELS", Group: "Configuration", Description: "Configure scan channels", Params: []ParamDef{
		{Key: "channels", Label: "Channels", Type: "channels", Required: true, Placeholder: "1,6,11 or 1..14"},
	}},
	"CONFIG_TARGETS": {Name: "CONFIG_TARGETS", Group: "Configuration", Description: "Configure target MACs", Params: []ParamDef{
		{Key: "targets", Label: "Target MACs", Type: "pipeList", Required: true, Placeholder: "AA:BB:CC:DD:EE:FF|11:22:33:44:55:66"},
	}},
	"CONFIG_RSSI": {Name: "CONFIG_RSSI", Group: "Configuration", Description: "Configure RSSI threshold", Params: []ParamDef{
		{Key: "rssi", Label: "RSSI Threshold", Type: "number", Required: true, Min: -120, Max: -1},
	}},
	"CONFIG_NODEID": {Name: "CONFIG_NODEID", Group: "Configuration", Description: "Set node ID", SingleNode: true, Params: []ParamDef{
		{Key: "nodeId", Label: "Node ID", Type: "text", Required: true, Placeholder: "AH03 (2-6 chars)"},
	}},

	// Security / Erase
	"ERASE_REQUEST":    {Name: "ERASE_REQUEST", Group: "Security", Description: "Request erase token from node"},
	"ERASE_FORCE":      {Name: "ERASE_FORCE", Group: "Security", Description: "Force erase with token", Params: []ParamDef{
		{Key: "token", Label: "Erase Token", Type: "text", Required: true, Placeholder: "AH_XXXXXXXX_XXXXXXXX_XXXXXXXX"},
	}},
	"ERASE_CANCEL":     {Name: "ERASE_CANCEL", Group: "Security", Description: "Cancel pending erase"},
	"AUTOERASE_ENABLE": {Name: "AUTOERASE_ENABLE", Group: "Security", Description: "Enable auto-erase on tamper", Params: []ParamDef{
		{Key: "setupDelay", Label: "Setup Delay (sec)", Type: "number", Min: 30, Max: 600},
		{Key: "eraseDelay", Label: "Erase Delay (sec)", Type: "number", Min: 10, Max: 300},
		{Key: "vibs", Label: "Vibration Count", Type: "number", Min: 2, Max: 5},
		{Key: "window", Label: "Window (sec)", Type: "number", Min: 10, Max: 60},
		{Key: "cooldown", Label: "Cooldown (sec)", Type: "number", Min: 300, Max: 3600},
	}},
	"AUTOERASE_DISABLE": {Name: "AUTOERASE_DISABLE", Group: "Security", Description: "Disable auto-erase"},
	"AUTOERASE_STATUS":  {Name: "AUTOERASE_STATUS", Group: "Security", Description: "Check auto-erase status"},

	// Battery
	"BATTERY_SAVER_START": {Name: "BATTERY_SAVER_START", Group: "Battery", Description: "Start battery saver mode", AllowForever: true, Params: []ParamDef{
		{Key: "interval", Label: "Interval (min)", Type: "number", Required: true, Min: 1, Max: 1440},
	}},
	"BATTERY_SAVER_STOP":   {Name: "BATTERY_SAVER_STOP", Group: "Battery", Description: "Stop battery saver"},
	"BATTERY_SAVER_STATUS": {Name: "BATTERY_SAVER_STATUS", Group: "Battery", Description: "Check battery saver status"},

	// System
	"REBOOT": {Name: "REBOOT", Group: "System", Description: "Reboot node"},
}

// ACK-to-command mapping for matching incoming ACKs to pending commands.
var ACKMap = map[string]string{
	"SCAN_ACK":            "SCAN_START",
	"DEVICE_SCAN_ACK":     "DEVICE_SCAN_START",
	"DRONE_ACK":           "DRONE_START",
	"DEAUTH_ACK":          "DEAUTH_START",
	"RANDOMIZATION_ACK":   "RANDOMIZATION_START",
	"BASELINE_ACK":        "BASELINE_START",
	"TRIANGULATE_ACK":     "TRIANGULATE_START",
	"TRIANGULATE_STOP_ACK":"TRIANGULATE_STOP",
	"ERASE_ACK":           "ERASE_FORCE",
	"CHANNELS_ACK":        "CONFIG_CHANNELS",
	"TARGETS_ACK":         "CONFIG_TARGETS",
	"AUTOERASE_ACK":       "AUTOERASE_ENABLE",
	"AUTOERASE_STATUS_ACK":"AUTOERASE_STATUS",
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
			upper := strings.ToUpper(val)
			if len(upper) < 2 || len(upper) > 6 {
				return fmt.Errorf("node ID must be 2-6 characters")
			}
			matched, _ := regexp.MatchString(`^[A-Z0-9]+$`, upper)
			if !matched {
				return fmt.Errorf("node ID must be uppercase alphanumeric")
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
var GroupOrder = []string{"Status", "Scanning", "Detection", "Triangulation", "Configuration", "Security", "Battery", "System"}
