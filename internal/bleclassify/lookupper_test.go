package bleclassify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withFakeLookupper points lookupperURL at a local httptest.Server for the
// duration of fn. Restores the original URL on return.
func withFakeLookupper(t *testing.T, h http.HandlerFunc, fn func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	orig := lookupperURLOverride
	lookupperURLOverride = srv.URL + "/api/ble/lookupper"
	defer func() { lookupperURLOverride = orig }()
	fn()
}

func TestClassifyErrorsWhenNoServer(t *testing.T) {
	// Point at a definitely-closed local port so the call fails fast.
	orig := lookupperURLOverride
	lookupperURLOverride = "http://127.0.0.1:1/api/ble/lookupper"
	defer func() { lookupperURLOverride = orig }()

	l := NewLookupper()
	if _, err := l.Classify(context.Background(), "HB55", "AA:BB:CC:DD:EE:FF", -85, 39, []byte{0x01, 0x02}, false); err == nil {
		t.Fatal("Classify against a closed port should error")
	}
}

func TestClassifyRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ble/lookupper", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req classifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		adv, err := base64.StdEncoding.DecodeString(req.RawAdvB64)
		if err != nil || len(adv) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result := ClassifyResult{
			MAC:            req.MAC,
			DetectionType:  "airtag",
			Manufacturer:   "Apple, Inc.",
			ManufacturerID: 0x004C,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	withFakeLookupper(t, mux.ServeHTTP, func() {
		l := NewLookupper()

		advBytes := []byte{0x1E, 0xFF, 0x4C, 0x00, 0x12, 0x19, 0x10}
		got, err := l.Classify(context.Background(), "HB55", "AA:BB:CC:DD:EE:FF", -85, 39, advBytes, true)
		if err != nil {
			t.Fatalf("classify: %v", err)
		}
		if got.MAC != "AA:BB:CC:DD:EE:FF" {
			t.Errorf("MAC roundtrip wrong: %s", got.MAC)
		}
		if got.DetectionType != "airtag" {
			t.Errorf("detection_type=%q, want airtag", got.DetectionType)
		}
		if got.ManufacturerID != 0x004C {
			t.Errorf("manufacturer_id=%d, want 76", got.ManufacturerID)
		}
		if !strings.Contains(strings.ToLower(got.Manufacturer), "apple") {
			t.Errorf("manufacturer=%q, want Apple", got.Manufacturer)
		}
	})
}

func TestClassifyNon200IsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ble/lookupper", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	withFakeLookupper(t, mux.ServeHTTP, func() {
		l := NewLookupper()
		if _, err := l.Classify(context.Background(), "HB55", "AA:BB:CC:DD:EE:FF", -85, 39, []byte{0x01}, false); err == nil {
			t.Fatal("Classify should error on 500 response")
		}
	})
}
