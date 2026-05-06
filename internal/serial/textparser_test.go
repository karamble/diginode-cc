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
