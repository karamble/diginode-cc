package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/drones"
)

// droneResponse is the CC PRO-compatible JSON shape that gotailme expects.
// Field names use lat/lon/operatorLat/operatorLon/droneId instead of the
// internal Go struct names (latitude/longitude/pilotLatitude/pilotLongitude/id).
type droneResponse struct {
	ID            string                 `json:"id"`
	DroneID       string                 `json:"droneId"`
	MAC           string                 `json:"mac,omitempty"`
	SerialNumber  string                 `json:"serialNumber,omitempty"`
	UASID         string                 `json:"uasId,omitempty"`
	OperatorID    string                 `json:"operatorId,omitempty"`
	Description   string                 `json:"description,omitempty"`
	UAType        string                 `json:"uaType,omitempty"`
	Manufacturer  string                 `json:"manufacturer,omitempty"`
	Model         string                 `json:"model,omitempty"`
	Lat           float64                `json:"lat,omitempty"`
	Lon           float64                `json:"lon,omitempty"`
	Altitude      float64                `json:"altitude,omitempty"`
	Speed         float64                `json:"speed,omitempty"`
	Heading       float64                `json:"heading,omitempty"`
	VerticalSpeed float64                `json:"verticalSpeed,omitempty"`
	OperatorLat   float64                `json:"operatorLat,omitempty"`
	OperatorLon   float64                `json:"operatorLon,omitempty"`
	RSSI          int                    `json:"rssi,omitempty"`
	Status        drones.Status          `json:"status"`
	Source        string                 `json:"source,omitempty"`
	NodeID        string                 `json:"nodeId,omitempty"`
	SiteID        string                 `json:"siteId,omitempty"`
	OriginSiteID  string                 `json:"originSiteId,omitempty"`
	SiteName      string                 `json:"siteName,omitempty"`
	SiteColor     string                 `json:"siteColor,omitempty"`
	SiteCountry   string                 `json:"siteCountry,omitempty"`
	SiteCity      string                 `json:"siteCity,omitempty"`
	FAAData       map[string]interface{} `json:"faa,omitempty"`
	Ts            time.Time              `json:"ts"`
	FirstSeen     time.Time              `json:"firstSeen"`
	LastSeen      time.Time              `json:"lastSeen"`
}

// mapDroneToResponse converts an internal Drone to the CC PRO-compatible
// response format expected by gotailme.
func mapDroneToResponse(d *drones.Drone) droneResponse {
	return droneResponse{
		ID:            d.ID,
		DroneID:       d.ID,
		MAC:           d.MAC,
		SerialNumber:  d.SerialNumber,
		UASID:         d.UASID,
		OperatorID:    d.OperatorID,
		Description:   d.Description,
		UAType:        d.UAType,
		Manufacturer:  d.Manufacturer,
		Model:         d.Model,
		Lat:           d.Latitude,
		Lon:           d.Longitude,
		Altitude:      d.Altitude,
		Speed:         d.Speed,
		Heading:       d.Heading,
		VerticalSpeed: d.VerticalSpeed,
		OperatorLat:   d.PilotLatitude,
		OperatorLon:   d.PilotLongitude,
		RSSI:          d.RSSI,
		Status:        d.Status,
		Source:        d.Source,
		NodeID:        d.NodeRefID,
		SiteID:        d.SiteID,
		OriginSiteID:  d.OriginSiteID,
		FAAData:       d.FAAData,
		Ts:            d.LastSeen,
		FirstSeen:     d.FirstSeen,
		LastSeen:      d.LastSeen,
	}
}

// mapDronesToResponse converts a slice of internal drones to response format.
func mapDronesToResponse(ds []*drones.Drone) []droneResponse {
	result := make([]droneResponse, 0, len(ds))
	for _, d := range ds {
		result = append(result, mapDroneToResponse(d))
	}
	return result
}

// enrichDroneWithSite populates site metadata fields on a drone response
// by looking up the site from the sites service.
func (s *Server) enrichDroneWithSite(resp *droneResponse) {
	if resp.SiteID == "" {
		return
	}
	site, err := s.svc.Sites.GetByID(context.Background(), resp.SiteID)
	if err != nil || site == nil {
		return
	}
	resp.SiteName = site.Name
	resp.SiteColor = site.Color
	resp.SiteCountry = site.Country
	resp.SiteCity = site.City
}

// handleListDrones returns all tracked drones in CC PRO format.
func (s *Server) handleListDrones(w http.ResponseWriter, r *http.Request) {
	all := s.svc.Drones.GetAll()
	responses := mapDronesToResponse(all)
	for i := range responses {
		s.enrichDroneWithSite(&responses[i])
	}
	writeJSON(w, http.StatusOK, responses)
}

// handleGetDrone returns a single drone by its key (MAC/serial/UAS ID).
func (s *Server) handleGetDrone(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	drone := s.svc.Drones.GetByKey(id)
	if drone == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("drone %q not found", id))
		return
	}
	resp := mapDroneToResponse(drone)
	s.enrichDroneWithSite(&resp)
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateDroneStatus changes a drone's classification status.
func (s *Server) handleUpdateDroneStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Status drones.Status `json:"status"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate status value.
	switch body.Status {
	case drones.StatusUnknown, drones.StatusFriendly, drones.StatusNeutral, drones.StatusHostile:
		// ok
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q", body.Status))
		return
	}

	if err := s.svc.Drones.UpdateStatus(id, body.Status); err != nil {
		if err == drones.ErrDroneNotFound {
			writeError(w, http.StatusNotFound, fmt.Sprintf("drone %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update drone status")
		return
	}

	// Return the updated drone.
	drone := s.svc.Drones.GetByKey(id)
	if drone == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}
	resp := mapDroneToResponse(drone)
	s.enrichDroneWithSite(&resp)
	writeJSON(w, http.StatusOK, resp)
}

// handleDeleteDrone removes a drone from tracking.
func (s *Server) handleDeleteDrone(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.svc.Drones.Remove(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetDroneDetections returns detection history for a drone.
// The drone service does not currently expose a detection query method,
// so this returns an empty array as a placeholder.
func (s *Server) handleGetDroneDetections(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id") // acknowledge path param
	writeJSON(w, http.StatusOK, []struct{}{})
}
