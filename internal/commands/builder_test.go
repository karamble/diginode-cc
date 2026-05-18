package commands

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestRegistry_AlphabeticalWithinGroup mirrors the within-group sort
// the API handler applies before serialising the catalog. Pinning the
// expected slice for a representative group is the intended signal —
// adding a new Scanning command without updating this test fails CI,
// at which point the author updates the slice. Naming is prefix-
// disciplined, so A-Z naturally clusters START/STOP pairs.
func TestRegistry_AlphabeticalWithinGroup(t *testing.T) {
	collect := func(group string) []string {
		out := []string{}
		for name, def := range Registry {
			if def.Group == group {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}

	cases := []struct {
		group string
		want  []string
	}{
		{
			group: "Scanning",
			want: []string{
				"DEVICE_SCAN_START", "DEVICE_SCAN_STOP",
				"PROBE_START", "PROBE_STOP",
				"SCAN_START", "SCAN_STOP",
				"START", "STOP",
			},
		},
		{
			group: "Configuration",
			want: []string{
				"CONFIG_CHANNELS", "CONFIG_NODEID", "CONFIG_RSSI",
				"CONFIG_TARGETS", "CONFIG_TARGETS_BLE",
				"RAW_BLE_OFF", "RAW_BLE_ON",
			},
		},
		{
			group: "Diagnostics",
			want: []string{
				"AUTOERASE_STATUS", "BASELINE_STATUS", "BATTERY_SAVER_STATUS",
				"C5_I2C_SCAN", "I2C_SCAN",
				"RAW_BLE_STATUS", "STATUS", "VIBRATION_STATUS",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.group, func(t *testing.T) {
			got := collect(tc.group)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("group %q sorted A-Z mismatch:\nwant %v\ngot  %v", tc.group, tc.want, got)
			}
		})
	}
}

// TestBuild_ScanStartChannels covers the channels-segment normalisation
// for SCAN_START. The firmware-side handler tolerates a missing or
// blank channels segment, but operators (and older deployed firmware)
// benefit from a deterministic wire frame:
//
//   - mode=1 (BLE only): drop channels entirely. The firmware's WiFi
//     scan branch is gated by mode 0/2, so the segment is dead weight
//     for BLE-only scans.
//   - mode=0 / mode=2 with empty channels: substitute the drone-
//     optimised default 1,6,11 so the audit log records what the
//     firmware actually used.
//   - mode=0 / mode=2 with explicit channels: passthrough verbatim.
//
// Channels are exposed as 1..14 with no regional gating — the product
// is operated internationally and the firmware accepts every channel
// in the 1..14 range without restriction.
func TestBuild_ScanStartChannels(t *testing.T) {
	cases := []struct {
		name      string
		params    []string
		wantLine  string
		wantError bool
	}{
		{
			name:     "ble-only drops channels segment",
			params:   []string{"1", "60"},
			wantLine: "@HB55 SCAN_START:1:60",
		},
		{
			name:     "ble-only ignores supplied channels",
			params:   []string{"1", "60", "1,6,11"},
			wantLine: "@HB55 SCAN_START:1:60",
		},
		{
			name:     "wifi-both empty channels substitutes default",
			params:   []string{"2", "60"},
			wantLine: "@HB55 SCAN_START:2:60:1,6,11",
		},
		{
			name:     "wifi-only empty channels substitutes default",
			params:   []string{"0", "60", ""},
			wantLine: "@HB55 SCAN_START:0:60:1,6,11",
		},
		{
			name:     "wifi-both passthrough custom channels",
			params:   []string{"2", "60", "1..14"},
			wantLine: "@HB55 SCAN_START:2:60:1..14",
		},
		{
			name:     "wifi-only passthrough csv channels",
			params:   []string{"0", "60", "1,6,11,14"},
			wantLine: "@HB55 SCAN_START:0:60:1,6,11,14",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Build("@HB55", "SCAN_START", tc.params, false)
			if tc.wantError {
				if err == nil {
					t.Fatalf("want error, got nil; line=%q", out.Line)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if out.Line != tc.wantLine {
				t.Errorf("line:\nwant %q\ngot  %q", tc.wantLine, out.Line)
			}
		})
	}
}

// TestBuild_ScanStartForeverWithBLEOnly guards the interaction between
// the BLE-only channel strip and the FOREVER token: BLE-only forever
// scans should produce SCAN_START:1:0:FOREVER (not :1:0::FOREVER or
// similar). Build() inserts FOREVER just before any bool literals, so
// after the channels segment is stripped the FOREVER lands directly
// after the duration. Verifies the firmware's
// "SCAN_START:1:0:FOREVER" channels-omitted shorthand path is what
// the C2 emits.
func TestBuild_ScanStartForeverWithBLEOnly(t *testing.T) {
	out, err := Build("@HB55", "SCAN_START", []string{"1", "0"}, true)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	want := "@HB55 SCAN_START:1:0:FOREVER"
	if out.Line != want {
		t.Errorf("line:\nwant %q\ngot  %q", want, out.Line)
	}
	if !strings.Contains(out.Line, ":FOREVER") {
		t.Errorf("FOREVER token missing in %q", out.Line)
	}
}
