package targets

import (
	"strings"
	"testing"
)

// TestBuildConfigTargetsBLEWireFrame exercises the pure formatter that
// converts selected target IDs into the newline-separated key=value body
// the firmware expects for CONFIG_TARGETS_BLE.
//
// The Service struct can be constructed with nil db/hub for this test
// because the formatter only reads the in-memory targets map; we populate
// it directly via the package-private field instead of touching the DB.
func TestBuildConfigTargetsBLEWireFrame(t *testing.T) {
	mfr := 0x0087
	app136 := 136
	tx := -67
	matchAny := "ANY"
	mode128 := "ALL"

	s := &Service{
		targets: map[string]*Target{
			"u-1": {
				ID:                "u-1",
				Name:              "Garmin Forerunner",
				BLEShortID:        "T-B-1001",
				BLEManufacturerID: &mfr,
				BLEServiceUUIDs16: []int{0xFEF8, 0x180D},
				BLELocalNameGlob:  "Forerunner *",
				BLEMatchMode:      mode128,
			},
			"u-2": {
				ID:                "u-2",
				Name:              "Apple AirTag",
				BLEShortID:        "T-B-1002",
				BLEManufacturerID: ptrInt(0x004C),
				BLEAppearanceMin:  &app136,
				BLEAppearanceMax:  &app136,
				BLETxPowerMin:     &tx,
				BLETxPowerMax:     ptrInt(20),
				BLEMatchMode:      matchAny,
			},
			"u-3-not-ble": {
				ID:   "u-3-not-ble",
				Name: "WiFi target",
				MAC:  "AA:BB:CC:DD:EE:FF",
			},
			"u-4-uuid128": {
				ID:                 "u-4-uuid128",
				Name:               "FindMy device",
				BLEShortID:         "T-B-1003",
				BLEServiceUUIDs128: []string{"12345678-1234-5678-1234-567812345678"},
			},
		},
	}

	t.Run("empty list returns empty body", func(t *testing.T) {
		body, err := s.BuildConfigTargetsBLEWireFrame(nil)
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			t.Errorf("want empty body, got %q", body)
		}
	})

	t.Run("non-existent ID returns error", func(t *testing.T) {
		_, err := s.BuildConfigTargetsBLEWireFrame([]string{"does-not-exist"})
		if err == nil {
			t.Error("want error for missing target, got nil")
		}
	})

	t.Run("non-BLE target returns error", func(t *testing.T) {
		_, err := s.BuildConfigTargetsBLEWireFrame([]string{"u-3-not-ble"})
		if err == nil {
			t.Error("want error when targeting a WiFi/MAC row, got nil")
		}
	})

	t.Run("single target with mfr+uuid+name", func(t *testing.T) {
		body, err := s.BuildConfigTargetsBLEWireFrame([]string{"u-1"})
		if err != nil {
			t.Fatal(err)
		}
		want := "T-B-1001:mfr=0087;uuid=fef8,180d;name=Forerunner *"
		if body != want {
			t.Errorf("body:\nwant %q\ngot  %q", want, body)
		}
	})

	t.Run("single target with appearance and txpower ranges", func(t *testing.T) {
		body, err := s.BuildConfigTargetsBLEWireFrame([]string{"u-2"})
		if err != nil {
			t.Fatal(err)
		}
		// ANY mode is non-default so it appears as the trailing match=ANY
		want := "T-B-1002:mfr=004c;appmin=136;appmax=136;txmin=-67;txmax=20;match=ANY"
		if body != want {
			t.Errorf("body:\nwant %q\ngot  %q", want, body)
		}
	})

	t.Run("128-bit UUID target", func(t *testing.T) {
		body, err := s.BuildConfigTargetsBLEWireFrame([]string{"u-4-uuid128"})
		if err != nil {
			t.Fatal(err)
		}
		want := "T-B-1003:uuid128=12345678-1234-5678-1234-567812345678"
		if body != want {
			t.Errorf("body:\nwant %q\ngot  %q", want, body)
		}
	})

	t.Run("multiple targets newline-separated", func(t *testing.T) {
		body, err := s.BuildConfigTargetsBLEWireFrame([]string{"u-1", "u-4-uuid128"})
		if err != nil {
			t.Fatal(err)
		}
		// Order follows the input slice
		lines := strings.Split(body, "\n")
		if len(lines) != 2 {
			t.Fatalf("want 2 lines, got %d: %q", len(lines), body)
		}
		if !strings.HasPrefix(lines[0], "T-B-1001:") {
			t.Errorf("first line: want T-B-1001 prefix, got %q", lines[0])
		}
		if !strings.HasPrefix(lines[1], "T-B-1003:") {
			t.Errorf("second line: want T-B-1003 prefix, got %q", lines[1])
		}
	})
}

// TestFingerprintHasField guards the validator that prevents creating a
// completely-empty BLE target (which would match every advertisement and
// is almost certainly an operator mistake).
func TestFingerprintHasField(t *testing.T) {
	cases := []struct {
		name string
		fp   *BLEFingerprint
		want bool
	}{
		{"all empty", &BLEFingerprint{}, false},
		{"only matchMode", &BLEFingerprint{MatchMode: "ALL"}, false},
		{"with mfr", &BLEFingerprint{ManufacturerID: ptrInt(0x0087)}, true},
		{"with uuid16", &BLEFingerprint{ServiceUUIDs16: []int{0xFEF8}}, true},
		{"with uuid128", &BLEFingerprint{ServiceUUIDs128: []string{"abc"}}, true},
		{"with name glob", &BLEFingerprint{LocalNameGlob: "Forerunner *"}, true},
		{"with appearance min only", &BLEFingerprint{AppearanceMin: ptrInt(128)}, true},
		{"with appearance max only", &BLEFingerprint{AppearanceMax: ptrInt(192)}, true},
		{"with tx min only", &BLEFingerprint{TxPowerMin: ptrInt(-90)}, true},
		{"with tx max only", &BLEFingerprint{TxPowerMax: ptrInt(20)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fingerprintHasField(tc.fp)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
