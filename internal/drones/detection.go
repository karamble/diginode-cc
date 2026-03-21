package drones

import (
	"encoding/json"
	"errors"
)

var ErrDroneNotFound = errors.New("drone not found")

// DroneDetection represents a raw drone detection event.
type DroneDetection struct {
	MAC            string  `json:"mac,omitempty"`
	SerialNumber   string  `json:"serialNumber,omitempty"`
	UASID          string  `json:"uasId,omitempty"`
	OperatorID     string  `json:"operatorId,omitempty"`
	UAType         string  `json:"uaType,omitempty"`
	Manufacturer   string  `json:"manufacturer,omitempty"`
	Model          string  `json:"model,omitempty"`
	Latitude       float64 `json:"latitude,omitempty"`
	Longitude      float64 `json:"longitude,omitempty"`
	Altitude       float64 `json:"altitude,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
	Heading        float64 `json:"heading,omitempty"`
	VerticalSpeed  float64 `json:"verticalSpeed,omitempty"`
	PilotLatitude  float64 `json:"pilotLatitude,omitempty"`
	PilotLongitude float64 `json:"pilotLongitude,omitempty"`
	RSSI           int     `json:"rssi,omitempty"`
	Source         string  `json:"source,omitempty"`
	NodeNum        uint32  `json:"nodeNum,omitempty"`
}

// Key returns a unique identifier for deduplication.
func (d *DroneDetection) Key() string {
	if d.MAC != "" {
		return d.MAC
	}
	if d.SerialNumber != "" {
		return d.SerialNumber
	}
	if d.UASID != "" {
		return d.UASID
	}
	return "unknown"
}

// ParseDetectionPayload parses a JSON drone detection payload from the mesh.
func ParseDetectionPayload(data []byte) (*DroneDetection, error) {
	var det DroneDetection
	if err := json.Unmarshal(data, &det); err != nil {
		return nil, err
	}
	return &det, nil
}
