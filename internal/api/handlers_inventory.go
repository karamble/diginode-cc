package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/inventory"
	"github.com/karamble/diginode-cc/internal/targets"
)

func (s *Server) handleListInventory(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Inventory.GetAll()
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
	manufacturer := ""
	if dev != nil {
		if dev.DeviceName != "" {
			name = dev.DeviceName
		}
		manufacturer = dev.Manufacturer
	}

	target := targets.Target{
		Name:        name,
		Description: fmt.Sprintf("Promoted from inventory (MAC: %s, manufacturer: %s)", mac, manufacturer),
		MAC:         mac,
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
