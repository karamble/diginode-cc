package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// GeofenceAction represents a geofence CRUD action for federation.
type GeofenceAction string

const (
	GeofenceUpsert GeofenceAction = "upsert"
	GeofenceDelete GeofenceAction = "delete"
)

// GeofenceEvent is published/received via MQTT for cross-site geofence sync.
type GeofenceEvent struct {
	Action    GeofenceAction `json:"action"`
	SiteID    string         `json:"siteId"`
	Geofence  interface{}    `json:"geofence,omitempty"`
	ID        string         `json:"id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// GeofenceHandler is called when a remote geofence event is received.
type GeofenceHandler func(evt GeofenceEvent)

// SetGeofenceHandler registers a callback for inbound geofence events.
func (s *Service) SetGeofenceHandler(handler GeofenceHandler) {
	s.mu.Lock()
	s.geofenceHandler = handler
	s.mu.Unlock()
}

// PublishGeofenceUpsert publishes a geofence create/update to the federation topic.
func (s *Service) PublishGeofenceUpsert(geofence interface{}) {
	s.publishGeofence(GeofenceEvent{
		Action:    GeofenceUpsert,
		SiteID:    s.siteID,
		Geofence:  geofence,
		Timestamp: time.Now().UTC(),
	})
}

// PublishGeofenceDelete publishes a geofence deletion to the federation topic.
func (s *Service) PublishGeofenceDelete(id string) {
	s.publishGeofence(GeofenceEvent{
		Action:    GeofenceDelete,
		SiteID:    s.siteID,
		ID:        id,
		Timestamp: time.Now().UTC(),
	})
}

func (s *Service) publishGeofence(evt GeofenceEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Error("failed to marshal geofence event", "error", err)
		return
	}

	topic := fmt.Sprintf("diginode/%s/geofences", s.siteID)
	s.client.Publish(topic, 1, false, payload)
}

// subscribeGeofences subscribes to geofence events from all sites.
func (s *Service) subscribeGeofences(client pahomqtt.Client) {
	client.Subscribe("diginode/+/geofences", 1, func(c pahomqtt.Client, msg pahomqtt.Message) {
		var evt GeofenceEvent
		if err := json.Unmarshal(msg.Payload(), &evt); err != nil {
			slog.Warn("failed to unmarshal geofence event", "error", err)
			return
		}

		// Don't process our own events
		if evt.SiteID == s.siteID {
			return
		}

		s.mu.RLock()
		handler := s.geofenceHandler
		s.mu.RUnlock()

		if handler != nil {
			handler(evt)
		}

		slog.Debug("received remote geofence event", "action", evt.Action, "site", evt.SiteID)
	})
}
