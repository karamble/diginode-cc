package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// EventType identifies WebSocket event types.
type EventType string

const (
	EventInit           EventType = "init"
	EventDroneTelemetry EventType = "drone.telemetry"
	EventDroneStatus    EventType = "drone.status"
	EventDroneRemove    EventType = "drone.remove"
	EventNodeUpdate     EventType = "node.update"
	EventNodeRemove     EventType = "node.remove"
	EventNodePosition   EventType = "node.position"
	EventChat           EventType = "chat.message"
	EventAlert          EventType = "alert"
	EventCommand        EventType = "command.update"
	EventHealth         EventType = "health"
	EventConfig         EventType = "config.update"
	EventInventory      EventType = "inventory.update"
	EventTarget         EventType = "target.update"
	EventGeofence       EventType = "geofence.event"
	EventADSB           EventType = "adsb.update"
	EventACARS          EventType = "acars.message"
)

// Event is a WebSocket message envelope.
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
}

// Hub manages WebSocket clients and broadcasts events.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	done       chan struct{}
	maxClients int
}

// NewHub creates a new WebSocket hub.
func NewHub(maxClients int) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
		maxClients: maxClients,
	}
}

// Run starts the hub event loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.maxClients > 0 && len(h.clients) >= h.maxClients {
				h.mu.Unlock()
				close(client.send)
				slog.Warn("WebSocket client rejected, max clients reached", "max", h.maxClients)
				continue
			}
			h.clients[client] = true
			h.mu.Unlock()
			slog.Debug("WebSocket client connected", "clients", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			slog.Debug("WebSocket client disconnected", "clients", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client buffer full, disconnect
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()

		case <-h.done:
			return
		}
	}
}

// Stop shuts down the hub.
func (h *Hub) Stop() {
	close(h.done)
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(evt Event) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("failed to marshal WebSocket event", "error", err)
		return
	}
	h.broadcast <- data
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
