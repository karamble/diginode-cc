package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/inventory"
)

func (s *Server) handleListInventory(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Inventory.GetAll()
	if list == nil {
		list = []*inventory.Device{}
	}
	writeJSON(w, http.StatusOK, list)
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
