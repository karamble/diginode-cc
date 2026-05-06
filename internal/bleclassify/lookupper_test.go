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

func TestLookupperUnavailableWhenNoServer(t *testing.T) {
	// Point at a definitely-closed local port so the probe fails fast.
	orig := lookupperURLOverride
	lookupperURLOverride = "http://127.0.0.1:1/api/ble/lookupper"
	defer func() { lookupperURLOverride = orig }()

	l := NewLookupper(context.Background())
	if l.Available() {
		t.Fatal("Lookupper.Available() should be false when no server is listening")
	}

	if _, err := l.Classify(context.Background(), "HB55", "AA:BB:CC:DD:EE:FF", -85, 39, []byte{0x01, 0x02}, false); err == nil {
		t.Fatal("Classify on unavailable lookupper should error")
	}
}

func TestLookupperAvailableAndClassify(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ble/lookupper", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"available":true}`))
		case http.MethodPost:
			var req classifyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			adv, err := base64.StdEncoding.DecodeString(req.RawAdvB64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(adv) == 0 {
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
		}
	})

	withFakeLookupper(t, mux.ServeHTTP, func() {
		l := NewLookupper(context.Background())
		if !l.Available() {
			t.Fatal("Lookupper should be available against the fake server")
		}

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

func TestLookupperNon200IsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ble/lookupper", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	withFakeLookupper(t, mux.ServeHTTP, func() {
		l := NewLookupper(context.Background())
		if !l.Available() {
			t.Fatal("ping must succeed for the rest of this test to be meaningful")
		}
		if _, err := l.Classify(context.Background(), "HB55", "AA:BB:CC:DD:EE:FF", -85, 39, []byte{0x01}, false); err == nil {
			t.Fatal("Classify should error on 500 response")
		}
	})
}
