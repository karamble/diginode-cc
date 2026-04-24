package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/inventory"
	"github.com/karamble/diginode-cc/internal/targets"
)

func (s *Server) handleListInventory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nodeID := q.Get("nodeId")
	afterStr := q.Get("seenAfter")
	beforeStr := q.Get("seenBefore")

	// With no filters, return the full cache (preserves existing behavior).
	var list []*inventory.Device
	if nodeID == "" && afterStr == "" && beforeStr == "" {
		list = s.svc.Inventory.GetAll()
	} else {
		var after, before time.Time
		if afterStr != "" {
			t, err := time.Parse(time.RFC3339Nano, afterStr)
			if err != nil {
				writeError(w, http.StatusBadRequest, "seenAfter must be RFC3339: "+err.Error())
				return
			}
			after = t
		}
		if beforeStr != "" {
			t, err := time.Parse(time.RFC3339Nano, beforeStr)
			if err != nil {
				writeError(w, http.StatusBadRequest, "seenBefore must be RFC3339: "+err.Error())
				return
			}
			before = t
		}
		list = s.svc.Inventory.GetFiltered(nodeID, after, before)
	}

	if list == nil {
		list = []*inventory.Device{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleClearInventory removes all inventory devices from memory and the database.
func (s *Server) handleClearInventory(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Inventory.ClearAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear inventory: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePromoteToTarget promotes an inventory device to a target by MAC address.
func (s *Server) handlePromoteToTarget(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	if mac == "" {
		writeError(w, http.StatusBadRequest, "mac is required")
		return
	}

	// Look up the device in inventory
	dev := s.svc.Inventory.GetByMAC(mac)

	// Build target from device info
	name := "Promoted: " + mac
	var manufacturer, deviceType, ssid string
	var lat, lon float64
	if dev != nil {
		if dev.DeviceName != "" {
			name = dev.DeviceName
		} else if dev.LastSSID != "" {
			name = dev.LastSSID
		}
		manufacturer = dev.Manufacturer
		deviceType = dev.DeviceType
		ssid = dev.LastSSID
		lat = dev.LastLat
		lon = dev.LastLon
	}

	desc := fmt.Sprintf("Promoted from inventory (MAC: %s", mac)
	if manufacturer != "" {
		desc += ", manufacturer: " + manufacturer
	}
	if ssid != "" {
		desc += ", SSID: " + ssid
	}
	desc += ")"

	target := targets.Target{
		Name:        name,
		Description: desc,
		TargetType:  deviceType,
		MAC:         mac,
		Latitude:    lat,
		Longitude:   lon,
		Status:      "active",
	}

	if err := s.svc.Targets.Create(r.Context(), &target); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create target: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, target)
}

func (s *Server) handleUpdateInventoryDevice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id") // id is the MAC address

	var req struct {
		DeviceName string `json:"deviceName"`
		DeviceType string `json:"deviceType"`
		Notes      string `json:"notes"`
		IsKnown    bool   `json:"isKnown"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Inventory.Update(r.Context(), id, req.DeviceName, req.DeviceType, req.Notes, req.IsKnown); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update device")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
