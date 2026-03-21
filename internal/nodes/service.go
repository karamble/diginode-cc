package nodes

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Node represents a tracked mesh node in memory.
type Node struct {
	ID                 string    `json:"id"`
	NodeNum            uint32    `json:"nodeNum"`
	NodeID             string    `json:"nodeId,omitempty"`
	LongName           string    `json:"longName,omitempty"`
	ShortName          string    `json:"shortName,omitempty"`
	HWModel            string    `json:"hwModel,omitempty"`
	Role               string    `json:"role,omitempty"`
	FirmwareVersion    string    `json:"firmwareVersion,omitempty"`
	Latitude           float64   `json:"latitude,omitempty"`
	Longitude          float64   `json:"longitude,omitempty"`
	Altitude           float64   `json:"altitude,omitempty"`
	BatteryLevel       uint32    `json:"batteryLevel,omitempty"`
	Voltage            float32   `json:"voltage,omitempty"`
	ChannelUtilization float32   `json:"channelUtilization,omitempty"`
	AirUtilTx          float32   `json:"airUtilTx,omitempty"`
	Temperature        float64   `json:"temperature,omitempty"`
	SNR                float32   `json:"snr,omitempty"`
	RSSI               int32     `json:"rssi,omitempty"`
	LastHeard          time.Time `json:"lastHeard"`
	IsOnline           bool      `json:"isOnline"`
	SiteID             string    `json:"siteId,omitempty"`
	OriginSiteID       string    `json:"originSiteId,omitempty"`
	LastMessage        string    `json:"lastMessage,omitempty"`
	TemperatureC          float64    `json:"temperatureC,omitempty"`
	TemperatureF          float64    `json:"temperatureF,omitempty"`
	TemperatureUpdatedAt  *time.Time `json:"temperatureUpdatedAt,omitempty"`
}

// Service manages mesh node state.
type Service struct {
	db    *database.DB
	hub   *ws.Hub
	nodes map[uint32]*Node
	mu    sync.RWMutex
}

// NewService creates a new node tracking service.
func NewService(db *database.DB, hub *ws.Hub) *Service {
	return &Service{
		db:    db,
		hub:   hub,
		nodes: make(map[uint32]*Node),
	}
}

// GetAll returns all tracked nodes.
func (s *Service) GetAll() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		result = append(result, n)
	}
	return result
}

// GetByNodeNum returns a node by its mesh number.
func (s *Service) GetByNodeNum(nodeNum uint32) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodes[nodeNum]
}

// LookupNodeIDAndSite returns the hex node ID and site ID for a mesh node number.
func (s *Service) LookupNodeIDAndSite(nodeNum uint32) (nodeID, siteID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	node, exists := s.nodes[nodeNum]
	if !exists {
		return "", ""
	}
	return node.NodeID, node.SiteID
}

// UpdateLongName changes a node's display name.
func (s *Service) UpdateLongName(nodeNum uint32, longName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[nodeNum]
	if !exists {
		return errors.New("node not found")
	}

	node.LongName = longName

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})

	go s.persistNode(node)
	return nil
}

// HandleNodeInfo processes a NodeInfoLite from the radio.
func (s *Service) HandleNodeInfo(info *serial.NodeInfoLite) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[info.Num]
	if !exists {
		node = &Node{
			NodeNum: info.Num,
		}
		s.nodes[info.Num] = node
	}

	if info.User != nil {
		node.NodeID = info.User.ID
		node.LongName = info.User.LongName
		node.ShortName = info.User.ShortName
		node.HWModel = info.User.HWModel
		node.Role = info.User.Role
	}

	if info.Position != nil {
		node.Latitude = info.Position.Latitude()
		node.Longitude = info.Position.Longitude()
		node.Altitude = float64(info.Position.Altitude)
	}

	if info.DeviceMetrics != nil {
		node.BatteryLevel = info.DeviceMetrics.BatteryLevel
		node.Voltage = info.DeviceMetrics.Voltage
		node.ChannelUtilization = info.DeviceMetrics.ChannelUtilization
		node.AirUtilTx = info.DeviceMetrics.AirUtilTx
	}

	node.SNR = info.SNR
	if info.LastHeard > 0 {
		node.LastHeard = time.Unix(int64(info.LastHeard), 0)
	}
	node.IsOnline = true

	slog.Info("node updated",
		"nodeNum", info.Num,
		"longName", node.LongName,
		"shortName", node.ShortName)

	// Broadcast update
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})

	// Persist to database (async)
	go s.persistNode(node)
}

// HandleTelemetry processes device metrics from a mesh packet.
func (s *Service) HandleTelemetry(from uint32, metrics *serial.DeviceMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}

	node.BatteryLevel = metrics.BatteryLevel
	node.Voltage = metrics.Voltage
	node.ChannelUtilization = metrics.ChannelUtilization
	node.AirUtilTx = metrics.AirUtilTx
	node.LastHeard = time.Now()
	node.IsOnline = true

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})

	go s.persistNode(node)
}

// HandleEnvironment processes environment metrics (temperature, humidity, pressure).
func (s *Service) HandleEnvironment(from uint32, env *serial.EnvironmentMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}

	node.Temperature = float64(env.Temperature)
	node.TemperatureC = float64(env.Temperature)
	node.TemperatureF = float64(env.Temperature)*9.0/5.0 + 32.0
	now := time.Now()
	node.TemperatureUpdatedAt = &now
	node.LastHeard = now
	node.IsOnline = true

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})

	go s.persistNode(node)
}

// HandlePosition processes a position update from a mesh packet.
func (s *Service) HandlePosition(from uint32, pos *serial.PositionData) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}

	node.Latitude = pos.Latitude()
	node.Longitude = pos.Longitude()
	node.Altitude = float64(pos.Altitude)
	node.LastHeard = time.Now()
	node.IsOnline = true

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodePosition,
		Payload: map[string]interface{}{
			"nodeNum":   from,
			"latitude":  node.Latitude,
			"longitude": node.Longitude,
			"altitude":  node.Altitude,
		},
	})

	go s.persistPosition(node)
}

// Remove deletes a node from tracking and broadcasts removal.
func (s *Service) Remove(nodeNum uint32) {
	s.mu.Lock()
	node, exists := s.nodes[nodeNum]
	if !exists {
		s.mu.Unlock()
		return
	}
	delete(s.nodes, nodeNum)
	s.mu.Unlock()

	s.hub.Broadcast(ws.Event{
		Type: ws.EventNodeRemove,
		Payload: map[string]interface{}{
			"nodeNum": nodeNum,
			"nodeId":  node.NodeID,
		},
	})
}

func (s *Service) persistNode(node *Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO nodes (node_num, node_id, long_name, short_name, hw_model, role,
			latitude, longitude, altitude, battery_level, voltage,
			channel_utilization, air_util_tx, snr, last_heard, is_online, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW())
		ON CONFLICT (node_num) DO UPDATE SET
			node_id = COALESCE(EXCLUDED.node_id, nodes.node_id),
			long_name = COALESCE(EXCLUDED.long_name, nodes.long_name),
			short_name = COALESCE(EXCLUDED.short_name, nodes.short_name),
			hw_model = COALESCE(EXCLUDED.hw_model, nodes.hw_model),
			role = COALESCE(EXCLUDED.role, nodes.role),
			latitude = COALESCE(EXCLUDED.latitude, nodes.latitude),
			longitude = COALESCE(EXCLUDED.longitude, nodes.longitude),
			altitude = COALESCE(EXCLUDED.altitude, nodes.altitude),
			battery_level = COALESCE(EXCLUDED.battery_level, nodes.battery_level),
			voltage = COALESCE(EXCLUDED.voltage, nodes.voltage),
			channel_utilization = COALESCE(EXCLUDED.channel_utilization, nodes.channel_utilization),
			air_util_tx = COALESCE(EXCLUDED.air_util_tx, nodes.air_util_tx),
			snr = COALESCE(EXCLUDED.snr, nodes.snr),
			last_heard = EXCLUDED.last_heard,
			is_online = EXCLUDED.is_online,
			updated_at = NOW()`,
		node.NodeNum, nullStr(node.NodeID), nullStr(node.LongName), nullStr(node.ShortName),
		nullStr(node.HWModel), nullStr(node.Role),
		nullFloat(node.Latitude), nullFloat(node.Longitude), nullFloat(node.Altitude),
		nullInt(int(node.BatteryLevel)), nullFloat(float64(node.Voltage)),
		nullFloat(float64(node.ChannelUtilization)), nullFloat(float64(node.AirUtilTx)),
		nullFloat(float64(node.SNR)),
		node.LastHeard, node.IsOnline,
	)
	if err != nil {
		slog.Error("failed to persist node", "nodeNum", node.NodeNum, "error", err)
	}
}

func (s *Service) persistPosition(node *Node) {
	if node.Latitude == 0 && node.Longitude == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO node_positions (node_id, latitude, longitude, altitude)
		SELECT id, $2, $3, $4 FROM nodes WHERE node_num = $1`,
		node.NodeNum, node.Latitude, node.Longitude, node.Altitude,
	)
	if err != nil {
		slog.Error("failed to persist node position", "nodeNum", node.NodeNum, "error", err)
	}
}

// helpers for nullable DB values
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

func nullInt(i int) interface{} {
	if i == 0 {
		return nil
	}
	return i
}
