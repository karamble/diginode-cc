package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/nodes"
)

// nodeResponse is the CC PRO-compatible JSON shape that gotailme expects.
// Field names use name/lat/lon/id instead of longName/latitude/longitude/nodeId.
type nodeResponse struct {
	ID                 string    `json:"id"`
	NodeNum            uint32    `json:"nodeNum"`
	Name               string    `json:"name"`
	ShortName          string    `json:"shortName,omitempty"`
	HWModel            string    `json:"hwModel,omitempty"`
	Role               string    `json:"role,omitempty"`
	FirmwareVersion    string    `json:"firmwareVersion,omitempty"`
	Lat                float64   `json:"lat,omitempty"`
	Lon                float64   `json:"lon,omitempty"`
	Altitude           float64   `json:"altitude,omitempty"`
	BatteryLevel       uint32    `json:"batteryLevel,omitempty"`
	Voltage            float32   `json:"voltage,omitempty"`
	ChannelUtilization float32   `json:"channelUtilization,omitempty"`
	AirUtilTx          float32   `json:"airUtilTx,omitempty"`
	Temperature        float64   `json:"temperature,omitempty"`
	TemperatureC          float64    `json:"temperatureC,omitempty"`
	TemperatureF          float64    `json:"temperatureF,omitempty"`
	TemperatureUpdatedAt  *time.Time `json:"temperatureUpdatedAt,omitempty"`
	SNR                   float32    `json:"snr,omitempty"`
	RSSI                  int32      `json:"rssi,omitempty"`
	Ts                    time.Time  `json:"ts"`
	LastHeard             time.Time  `json:"lastHeard"`
	LastSeen              *time.Time `json:"lastSeen,omitempty"`
	IsOnline              bool       `json:"isOnline"`
	SiteID             string    `json:"siteId,omitempty"`
	OriginSiteID       string    `json:"originSiteId,omitempty"`
	SiteName           string    `json:"siteName,omitempty"`
	SiteColor          string    `json:"siteColor,omitempty"`
	SiteCountry        string    `json:"siteCountry,omitempty"`
	SiteCity           string    `json:"siteCity,omitempty"`
	LastMessage        string    `json:"lastMessage,omitempty"`
}

// mapNodeToResponse converts an internal Node to the CC PRO-compatible
// response format expected by gotailme.
func mapNodeToResponse(n *nodes.Node) nodeResponse {
	return nodeResponse{
		ID:                 n.NodeID, // CC PRO uses the hex node ID string
		NodeNum:            n.NodeNum,
		Name:               n.LongName,
		ShortName:          n.ShortName,
		HWModel:            n.HWModel,
		Role:               n.Role,
		FirmwareVersion:    n.FirmwareVersion,
		Lat:                n.Latitude,
		Lon:                n.Longitude,
		Altitude:           n.Altitude,
		BatteryLevel:       n.BatteryLevel,
		Voltage:            n.Voltage,
		ChannelUtilization: n.ChannelUtilization,
		AirUtilTx:          n.AirUtilTx,
		Temperature:        n.Temperature,
		TemperatureC:       n.TemperatureC,
		TemperatureF:       n.TemperatureF,
		SNR:                n.SNR,
		RSSI:               n.RSSI,
		Ts:                 n.LastHeard,
		LastHeard:          n.LastHeard,
		LastSeen:           &n.LastHeard,
		IsOnline:           n.IsOnline,
		SiteID:             n.SiteID,
		OriginSiteID:       n.OriginSiteID,
		LastMessage:        n.LastMessage,
	}
}

// mapNodesToResponse converts a slice of internal nodes to response format.
func mapNodesToResponse(ns []*nodes.Node) []nodeResponse {
	result := make([]nodeResponse, 0, len(ns))
	for _, n := range ns {
		result = append(result, mapNodeToResponse(n))
	}
	return result
}

// enrichNodeWithSite populates site metadata fields on a node response
// by looking up the site from the sites service.
func (s *Server) enrichNodeWithSite(resp *nodeResponse) {
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

// handleListNodes returns all tracked mesh nodes in CC PRO format.
func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	all := s.svc.Nodes.GetAll()
	responses := mapNodesToResponse(all)
	for i := range responses {
		s.enrichNodeWithSite(&responses[i])
	}
	writeJSON(w, http.StatusOK, responses)
}

// handleGetNode returns a single node by its node number (uint32).
func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	nodeNum, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid node id %q", id))
		return
	}

	node := s.svc.Nodes.GetByNodeNum(uint32(nodeNum))
	if node == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", id))
		return
	}

	resp := mapNodeToResponse(node)
	s.enrichNodeWithSite(&resp)
	writeJSON(w, http.StatusOK, resp)
}

// handleGetNodePositions returns position history for a node.
// The node service does not currently expose position history queries,
// so this returns an empty array as a placeholder.
func (s *Server) handleGetNodePositions(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id") // acknowledge path param
	writeJSON(w, http.StatusOK, []struct{}{})
}

// handleDeleteNode removes a node from tracking.
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	nodeNum, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node number")
		return
	}
	s.svc.Nodes.Remove(uint32(nodeNum))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUpdateNode accepts a JSON body to update node properties.
func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	nodeNum, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid node id %q", id))
		return
	}

	node := s.svc.Nodes.GetByNodeNum(uint32(nodeNum))
	if node == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", id))
		return
	}

	var body struct {
		LongName string `json:"longName,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.LongName != "" {
		if err := s.svc.Nodes.UpdateLongName(uint32(nodeNum), body.LongName); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update node")
			return
		}
	}

	// Re-fetch to return the updated state.
	updated := s.svc.Nodes.GetByNodeNum(uint32(nodeNum))
	if updated == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}
	resp := mapNodeToResponse(updated)
	s.enrichNodeWithSite(&resp)
	writeJSON(w, http.StatusOK, resp)
}
