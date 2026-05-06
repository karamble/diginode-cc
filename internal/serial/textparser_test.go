package serial

import (
	"bytes"
	"testing"
)

// TestParseDevice_LongAndShort locks in backward compatibility for the
// DEVICE: → D: wire-format compression shipped with the BLERAW pacing
// fix. Old AntiHunter and pre-rebrand Halberd builds in the field keep
// emitting "DEVICE:" frames; new firmware emits "D:" frames. The parser
// must accept both indefinitely and route them to the same handler with
// identical captured fields.
func TestParseDevice_LongAndShort(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"long-wifi", "HB55: DEVICE:AA:BB:CC:DD:EE:FF W -75 C6 N:MyDevice"},
		{"short-wifi", "HB55: D:AA:BB:CC:DD:EE:FF W -75 C6 N:MyDevice"},
		{"long-ble", "HB55: DEVICE:AA:BB:CC:DD:EE:FF B -85 N:Watch"},
		{"short-ble", "HB55: D:AA:BB:CC:DD:EE:FF B -85 N:Watch"},
	}

	p := NewTextParser()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := p.ParseLine(tc.line)
			if len(events) != 1 {
				t.Fatalf("want 1 event, got %d", len(events))
			}
			ev := events[0]
			if ev.Kind != "target-detected" {
				t.Fatalf("want kind target-detected, got %q", ev.Kind)
			}
			if got, _ := ev.Data["mac"].(string); got != "AA:BB:CC:DD:EE:FF" {
				t.Errorf("mac: want AA:BB:CC:DD:EE:FF, got %q", got)
			}
			if ev.NodeID != "HB55" {
				t.Errorf("nodeID: want HB55, got %q", ev.NodeID)
			}
		})
	}
}

// TestParseBLERaw_LongAndShort locks in backward compatibility for the
// BLERAW: → B: wire-format compression. The short form drops the always-
// zero channel field; the parser defaults channel=0 via parseOptInt on
// the missing capture group so downstream handlers see identical data.
func TestParseBLERaw_LongAndShort(t *testing.T) {
	// "BAQEZmls" decodes to the bytes 0x04,0x04,0x04,0x66,0x69,0x6c — used
	// here just to give the parser a non-empty advBytes payload to decode.
	cases := []struct {
		name        string
		line        string
		wantChannel int
	}{
		{
			name:        "long-with-channel",
			line:        "HB55: BLERAW:AA:BB:CC:DD:EE:FF -85 39 BAQEZmls",
			wantChannel: 39,
		},
		{
			name:        "long-channel-zero",
			line:        "HB55: BLERAW:AA:BB:CC:DD:EE:FF -85 0 BAQEZmls",
			wantChannel: 0,
		},
		{
			name:        "short-no-channel",
			line:        "HB55: B:AA:BB:CC:DD:EE:FF -85 BAQEZmls",
			wantChannel: 0,
		},
	}

	p := NewTextParser()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := p.ParseLine(tc.line)
			if len(events) != 1 {
				t.Fatalf("want 1 event, got %d", len(events))
			}
			ev := events[0]
			if ev.Kind != "ble-raw" {
				t.Fatalf("want kind ble-raw, got %q", ev.Kind)
			}
			if got, _ := ev.Data["mac"].(string); got != "AA:BB:CC:DD:EE:FF" {
				t.Errorf("mac: want AA:BB:CC:DD:EE:FF, got %q", got)
			}
			if got, _ := ev.Data["rssi"].(int); got != -85 {
				t.Errorf("rssi: want -85, got %d", got)
			}
			if got, _ := ev.Data["channel"].(int); got != tc.wantChannel {
				t.Errorf("channel: want %d, got %d", tc.wantChannel, got)
			}
			advBytes, ok := ev.Data["advBytes"].([]byte)
			if !ok {
				t.Fatalf("advBytes missing or wrong type")
			}
			want := []byte{0x04, 0x04, 0x04, 0x66, 0x69, 0x6c}
			if !bytes.Equal(advBytes, want) {
				t.Errorf("advBytes: want %x, got %x", want, advBytes)
			}
		})
	}
}

// TestParseShortFormDoesNotMatchLongForm guards against regex ambiguity:
// the literal "D:" prefix must not match a DEVICE: line and "B:" must not
// match a BLERAW: line. The short patterns are defined after the long ones
// and the regexes are anchored on the literal colon, so this should be
// guaranteed structurally — this test catches a regression if either is
// reordered or relaxed.
func TestParseShortFormDoesNotMatchLongForm(t *testing.T) {
	p := NewTextParser()
	cases := []struct {
		name     string
		line     string
		wantKind string
	}{
		{
			name:     "device-long-stays-device",
			line:     "HB55: DEVICE:AA:BB:CC:DD:EE:FF B -85",
			wantKind: "target-detected",
		},
		{
			name:     "bleraw-long-stays-ble-raw",
			line:     "HB55: BLERAW:AA:BB:CC:DD:EE:FF -85 0 BAQEZmls",
			wantKind: "ble-raw",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := p.ParseLine(tc.line)
			if len(events) != 1 {
				t.Fatalf("want 1 event, got %d", len(events))
			}
			if events[0].Kind != tc.wantKind {
				t.Errorf("kind: want %q, got %q", tc.wantKind, events[0].Kind)
			}
		})
	}
}

// TestParseACK_ConfigSubkindValue locks in the fix for CONFIG_ACK frames
// emitted as CONFIG_ACK:SUBKIND:VALUE. Before the fix the greedy [A-Z_]*
// status group captured the SUBKIND ("TARGETS_BLE") and dropped the trailing
// :OK, leaving HandleStructuredACK with an unrecognised status that fell to
// StatusSent. The regex now exposes a separate optional value capture which
// handleACK prefers; the flat shapes (SCAN_ACK:OK / RAW_BLE_ACK:ON / etc.)
// must keep working as before.
func TestParseACK_ConfigSubkindValue(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantKind   string
		wantStatus string
	}{
		{"config-targets-ble-ok", "HB55: CONFIG_ACK:TARGETS_BLE:OK", "CONFIG_ACK", "OK"},
		{"config-targets-ok", "HB55: CONFIG_ACK:TARGETS:OK", "CONFIG_ACK", "OK"},
		{"config-channels-value", "HB55: CONFIG_ACK:CHANNELS:1,6,11", "CONFIG_ACK", "1,6,11"},
		{"config-rssi-signed", "HB55: CONFIG_ACK:RSSI:-65", "CONFIG_ACK", "-65"},
		{"config-node-id-invalid", "HB55: CONFIG_ACK:NODE_ID:INVALID_LEN", "CONFIG_ACK", "INVALID_LEN"},
		{"flat-scan-ok", "HB55: SCAN_ACK:OK", "SCAN_ACK", "OK"},
		{"flat-raw-ble-on", "HB55: RAW_BLE_ACK:ON", "RAW_BLE_ACK", "ON"},
		{"flat-stop-ok", "HB55: STOP_ACK:OK", "STOP_ACK", "OK"},
		{"flat-scan-started", "HB55: SCAN_ACK:STARTED", "SCAN_ACK", "STARTED"},
		{"target-interval-numeric", "HB55: TARGET_INTERVAL_ACK:120", "TARGET_INTERVAL_ACK", "120"},
		{"target-interval-edge-min", "HB55: TARGET_INTERVAL_ACK:5", "TARGET_INTERVAL_ACK", "5"},
	}

	p := NewTextParser()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := p.ParseLine(tc.line)
			if len(events) != 1 {
				t.Fatalf("want 1 event, got %d (%+v)", len(events), events)
			}
			ev := events[0]
			if ev.Kind != "command-ack" {
				t.Errorf("Kind: want command-ack, got %q", ev.Kind)
			}
			gotKind, _ := ev.Data["ackType"].(string)
			if !equalFold(gotKind, tc.wantKind) {
				t.Errorf("ackType: want %q, got %q", tc.wantKind, gotKind)
			}
			gotStatus, _ := ev.Data["status"].(string)
			if gotStatus != tc.wantStatus {
				t.Errorf("status: want %q, got %q", tc.wantStatus, gotStatus)
			}
		})
	}
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'a' <= ca && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if 'a' <= cb && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// TestParseTarget_TIDCapture confirms the optional TID field on the firmware's
// "Target:" hit frame parses into data["targetId"] and that BLE fingerprint
// hits route to the dedicated "ble-target-hit" alert category. Plain MAC/SSID
// matches stay on the legacy "inventory" category for backward compat.
func TestParseTarget_TIDCapture(t *testing.T) {
	cases := []struct {
		name             string
		line             string
		wantTargetID     string
		wantAlertCategory string
		wantAlertLevel   string
	}{
		{
			name:             "wifi-mac-target-no-tid",
			line:             "HB55: Target: AA:BB:CC:DD:EE:FF RSSI:-65 Type:WiFi Name:MyDevice",
			wantTargetID:     "",
			wantAlertCategory: "inventory",
			wantAlertLevel:   "NOTICE",
		},
		{
			name:             "ble-fingerprint-hit-with-tid",
			line:             "HB55: Target: 7d:f6:5a:25:3d:e4 RSSI:-65 Type:BLE Name:Forerunner TID:T-B-1001",
			wantTargetID:     "T-B-1001",
			wantAlertCategory: "ble-target-hit",
			wantAlertLevel:   "ALERT",
		},
		{
			name:             "ble-fingerprint-hit-with-tid-and-gps",
			line:             "HB55: Target: 7d:f6:5a:25:3d:e4 RSSI:-65 Type:BLE Name:Forerunner TID:T-B-1001 GPS=12.345678,56.789012",
			wantTargetID:     "T-B-1001",
			wantAlertCategory: "ble-target-hit",
			wantAlertLevel:   "ALERT",
		},
		{
			name:             "future-wifi-identity-tid",
			line:             "HB55: Target: AA:BB:CC:DD:EE:FF RSSI:-72 Type:WiFi TID:T-W-2034",
			wantTargetID:     "T-W-2034",
			wantAlertCategory: "inventory", // T-W- stays "inventory" — only T-B- triggers ble-target-hit
			wantAlertLevel:   "NOTICE",
		},
	}

	p := NewTextParser()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := p.ParseLine(tc.line)
			if len(events) != 2 {
				t.Fatalf("want 2 events (target-detected + alert), got %d", len(events))
			}
			detected := events[0]
			alert := events[1]
			if detected.Kind != "target-detected" {
				t.Errorf("first event Kind: want target-detected, got %q", detected.Kind)
			}
			if alert.Kind != "alert" {
				t.Errorf("second event Kind: want alert, got %q", alert.Kind)
			}

			gotTID, _ := detected.Data["targetId"].(string)
			if gotTID != tc.wantTargetID {
				t.Errorf("targetId: want %q, got %q", tc.wantTargetID, gotTID)
			}
			if alert.Category != tc.wantAlertCategory {
				t.Errorf("alert.Category: want %q, got %q", tc.wantAlertCategory, alert.Category)
			}
			if alert.Level != tc.wantAlertLevel {
				t.Errorf("alert.Level: want %q, got %q", tc.wantAlertLevel, alert.Level)
			}
		})
	}
}
