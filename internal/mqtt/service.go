package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Service manages MQTT broker connections for multi-site federation.
type Service struct {
	hub       *ws.Hub
	client    pahomqtt.Client
	siteID    string
	connected bool
	mu        sync.RWMutex
	stopCh    chan struct{}
}

// NewService creates a new MQTT federation service.
func NewService(hub *ws.Hub, brokerURL, siteID string, connectTimeoutMS int) *Service {
	s := &Service{
		hub:    hub,
		siteID: siteID,
		stopCh: make(chan struct{}),
	}

	opts := pahomqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(fmt.Sprintf("diginode-cc-%s", siteID)).
		SetAutoReconnect(true).
		SetConnectRetryInterval(time.Duration(connectTimeoutMS) * time.Millisecond).
		SetOnConnectHandler(func(c pahomqtt.Client) {
			slog.Info("MQTT connected", "broker", brokerURL)
			s.mu.Lock()
			s.connected = true
			s.mu.Unlock()
			s.subscribe(c)
		}).
		SetConnectionLostHandler(func(c pahomqtt.Client, err error) {
			slog.Warn("MQTT connection lost", "error", err)
			s.mu.Lock()
			s.connected = false
			s.mu.Unlock()
		})

	s.client = pahomqtt.NewClient(opts)
	return s
}

// Start connects to the MQTT broker.
func (s *Service) Start() error {
	token := s.client.Connect()
	token.Wait()
	return token.Error()
}

// Stop disconnects from the MQTT broker.
func (s *Service) Stop() {
	close(s.stopCh)
	s.client.Disconnect(1000)
}

// IsConnected returns the connection status.
func (s *Service) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// PublishDroneEvent publishes a drone event to the federation topic.
func (s *Service) PublishDroneEvent(eventType string, data interface{}) {
	msg := map[string]interface{}{
		"type":      eventType,
		"siteId":    s.siteID,
		"data":      data,
		"timestamp": time.Now().UTC(),
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	topic := fmt.Sprintf("diginode/%s/drones", s.siteID)
	s.client.Publish(topic, 1, false, payload)
}

// PublishAlert publishes an alert to the federation topic.
func (s *Service) PublishAlert(data interface{}) {
	msg := map[string]interface{}{
		"type":      "alert",
		"siteId":    s.siteID,
		"data":      data,
		"timestamp": time.Now().UTC(),
	}

	payload, _ := json.Marshal(msg)
	topic := fmt.Sprintf("diginode/%s/alerts", s.siteID)
	s.client.Publish(topic, 1, false, payload)
}

func (s *Service) subscribe(client pahomqtt.Client) {
	// Subscribe to all site drone events
	client.Subscribe("diginode/+/drones", 1, func(c pahomqtt.Client, msg pahomqtt.Message) {
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return
		}
		// Don't re-broadcast our own events
		if siteID, ok := data["siteId"].(string); ok && siteID == s.siteID {
			return
		}
		s.hub.Broadcast(ws.Event{
			Type:    ws.EventDroneTelemetry,
			Payload: data,
		})
	})

	// Subscribe to all site alerts
	client.Subscribe("diginode/+/alerts", 1, func(c pahomqtt.Client, msg pahomqtt.Message) {
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return
		}
		if siteID, ok := data["siteId"].(string); ok && siteID == s.siteID {
			return
		}
		s.hub.Broadcast(ws.Event{
			Type:    ws.EventAlert,
			Payload: data,
		})
	})

	slog.Info("MQTT subscriptions active")
}
