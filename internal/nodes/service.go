package nodes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// NodeOnlineTimeout is how long since last_heard before a node is considered offline.
// Meshtastic nodes broadcast NodeInfo every 15 minutes; 16 min = just over 1 heartbeat.
const NodeOnlineTimeout = 16 * time.Minute

// NodeType distinguishes C2 gateways from sensor nodes.
type NodeType string

const (
	NodeTypeUnknown    NodeType = ""           // Not yet classified
	NodeTypeOperator   NodeType = "operator"   // Plain Meshtastic client (operator handheld, 3rd-party hardware)
	NodeTypeGotailme   NodeType = "gotailme"   // C2 gateway (runs DigiNode CC / CC PRO)
	NodeTypeAntihunter NodeType = "antihunter" // AntiHunter detection sensor node
	NodeTypeGatesensor NodeType = "gatesensor" // Gate sensor (Arduino Nano → Heltec TEXTMSG bridge)
)

// Node represents a tracked mesh node in memory.
type Node struct {
	ID                 string    `json:"id"`
	NodeNum            uint32    `json:"nodeNum"`
	NodeID             string    `json:"nodeId,omitempty"`
	NodeType           NodeType  `json:"nodeType,omitempty"`
	LongName           string    `json:"longName,omitempty"`
	ShortName          string    `json:"shortName,omitempty"`
	HWModel            string    `json:"hwModel,omitempty"`
	MacAddr            string    `json:"macAddr,omitempty"`
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
	RSSI               int32     `json:"rssi"`
	LastHeard          time.Time `json:"lastHeard"`
	IsOnline           bool      `json:"isOnline"`
	IsLocal            bool      `json:"isLocal,omitempty"`
	SiteID             string    `json:"siteId,omitempty"`
	OriginSiteID       string    `json:"originSiteId,omitempty"`
	LastMessage        string    `json:"lastMessage,omitempty"`
	// AHShortID is the 2–5 char string the AntiHunter firmware uses as its own
	// address in TEXTMSG frames ("AH01" in "AH01: STATUS: ..."). It's set by
	// the sensor's CONFIG_NODEID and is what the remote dispatcher matches
	// against the @TARGET prefix — without this, @NODE_<meshShortName> routed
	// commands are silently dropped.
	AHShortID            string     `json:"ahShortId,omitempty"`
	TemperatureC         float64    `json:"temperatureC,omitempty"`
	TemperatureF         float64    `json:"temperatureF,omitempty"`
	TemperatureUpdatedAt *time.Time `json:"temperatureUpdatedAt,omitempty"`
	TelemetryUpdatedAt   *time.Time `json:"telemetryUpdatedAt,omitempty"`
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

// Load hydrates the in-memory node map from the database. Without this, a CC
// restart loses every node's last_heard until the mesh radio happens to hear
// it again — and the radio's NodeInfoLite dump frequently reports last_heard=0
// for nodes the radio itself hasn't heard this boot, leaving offline nodes
// stuck at Go's zero time.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT node_num, node_id, long_name, short_name, hw_model, role,
			latitude, longitude, altitude, battery_level, voltage,
			channel_utilization, air_util_tx, snr, last_heard, is_online,
			site_id, origin_site_id, temperature_c, temperature_f,
			temperature_updated_at, last_message, node_type, ah_short_id
		FROM nodes`)
	if err != nil {
		return fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := 0
	for rows.Next() {
		var (
			nodeNum                                 uint32
			nodeID, longName, shortName, hwModel    *string
			role, lastMessage                       *string
			siteID, originSiteID                    *string
			lat, lon, alt                           *float64
			battery                                 *int32
			voltage, chanUtil, airUtilTx, snr       *float64
			lastHeard, tempUpdatedAt                *time.Time
			isOnline                                *bool
			tempC, tempF                            *float64
			nodeType, ahShortID                     string
		)
		if err := rows.Scan(&nodeNum, &nodeID, &longName, &shortName, &hwModel, &role,
			&lat, &lon, &alt, &battery, &voltage, &chanUtil, &airUtilTx, &snr,
			&lastHeard, &isOnline, &siteID, &originSiteID, &tempC, &tempF,
			&tempUpdatedAt, &lastMessage, &nodeType, &ahShortID); err != nil {
			slog.Error("nodes.Load: scan failed", "error", err)
			continue
		}

		n := &Node{NodeNum: nodeNum}
		if nodeID != nil {
			n.NodeID = *nodeID
		} else {
			n.NodeID = fmt.Sprintf("!%08x", nodeNum)
		}
		if longName != nil {
			n.LongName = *longName
		}
		if shortName != nil {
			n.ShortName = *shortName
		}
		if hwModel != nil {
			n.HWModel = *hwModel
		}
		if role != nil {
			n.Role = *role
		}
		if lat != nil {
			n.Latitude = *lat
		}
		if lon != nil {
			n.Longitude = *lon
		}
		if alt != nil {
			n.Altitude = *alt
		}
		if battery != nil {
			n.BatteryLevel = uint32(*battery)
		}
		if voltage != nil {
			n.Voltage = float32(*voltage)
		}
		if chanUtil != nil {
			n.ChannelUtilization = float32(*chanUtil)
		}
		if airUtilTx != nil {
			n.AirUtilTx = float32(*airUtilTx)
		}
		if snr != nil {
			n.SNR = float32(*snr)
		}
		if lastHeard != nil {
			n.LastHeard = *lastHeard
		}
		if siteID != nil {
			n.SiteID = *siteID
		}
		if originSiteID != nil {
			n.OriginSiteID = *originSiteID
		}
		if tempC != nil {
			n.TemperatureC = *tempC
			n.Temperature = *tempC
		}
		if tempF != nil {
			n.TemperatureF = *tempF
		}
		if tempUpdatedAt != nil {
			n.TemperatureUpdatedAt = tempUpdatedAt
		}
		if lastMessage != nil {
			n.LastMessage = *lastMessage
		}
		if nodeType != "" {
			n.NodeType = NodeType(nodeType)
		}
		if ahShortID != "" {
			n.AHShortID = ahShortID
		}
		n.IsOnline = isNodeOnline(n)
		s.nodes[nodeNum] = n
		loaded++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate nodes: %w", err)
	}
	slog.Info("loaded nodes from database", "count", loaded)
	return nil
}

// isNodeOnline returns true if the node was heard within the online timeout window.
func isNodeOnline(n *Node) bool {
	if n.IsLocal {
		return true // local node is always online
	}
	if n.LastHeard.IsZero() {
		return false
	}
	return time.Since(n.LastHeard) < NodeOnlineTimeout
}

// GetAll returns all tracked nodes with isOnline computed from lastHeard,
// sorted by NodeNum for stable ordering.
func (s *Service) GetAll() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		n.IsOnline = isNodeOnline(n)
		result = append(result, n)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].NodeNum < result[j].NodeNum
	})
	return result
}

// GetLocalNodeNum returns the mesh node number of the local C2 gateway, or 0 if unknown.
func (s *Service) GetLocalNodeNum() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.nodes {
		if n.IsLocal {
			return n.NodeNum
		}
	}
	return 0
}

// GetByNodeNum returns a node by its mesh number.
func (s *Service) GetByNodeNum(nodeNum uint32) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodes[nodeNum]
}

// GetLongName returns the node's NodeInfo-supplied long name, or "" if the
// node is unknown or hasn't sent NodeInfo yet. The dispatcher uses this to
// distinguish gotailme-firmware nodes (LongName starts with "GoTailMe") from
// plain Meshtastic operator devices that have the same protobuf footprint.
func (s *Service) GetLongName(nodeNum uint32) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n, ok := s.nodes[nodeNum]; ok {
		return n.LongName
	}
	return ""
}

// TouchNode ensures a node entry exists for the given mesh node number.
// Called by the dispatcher on every incoming mesh packet so remote nodes
// appear in the node list even before we receive their full NodeInfo.
func (s *Service) TouchNode(nodeNum uint32, rxSNR float32, rxRSSI int32) {
	s.mu.Lock()
	node, exists := s.nodes[nodeNum]
	if !exists {
		node = &Node{
			NodeNum:  nodeNum,
			NodeID:   fmt.Sprintf("!%08x", nodeNum),
			IsOnline: true,
		}
		s.nodes[nodeNum] = node
		slog.Info("new mesh node discovered", "nodeNum", nodeNum, "nodeId", node.NodeID)

		s.hub.Broadcast(ws.Event{
			Type:    ws.EventNodeUpdate,
			Payload: node,
		})
	}
	node.SNR = rxSNR
	node.RSSI = rxRSSI
	node.LastHeard = time.Now()
	node.IsOnline = true
	s.mu.Unlock()
}

// MarkLocal flags a node as the local C2 gateway (ourselves).
func (s *Service) MarkLocal(nodeNum uint32) {
	s.mu.Lock()
	node, exists := s.nodes[nodeNum]
	if !exists {
		s.mu.Unlock()
		return
	}
	node.IsLocal = true
	node.NodeType = NodeTypeGotailme
	s.mu.Unlock()
	go s.persistNode(node)
}

// ClassifyNode sets the node type based on observed behavior.
// Called when we learn something about what a node does.
//
// Classification ladder, lowest → highest specificity:
//
//	operator (plain Meshtastic client) → gotailme (our C2 firmware) → antihunter / gatesensor (sensor firmware)
//
// Promotions go up the ladder; downgrades are blocked. operator is the lowest
// rung so anything more specific replaces it.
func (s *Service) ClassifyNode(nodeNum uint32, nodeType string) {
	s.mu.Lock()
	node, exists := s.nodes[nodeNum]
	if !exists {
		s.mu.Unlock()
		return
	}
	if nodeType == "" {
		s.mu.Unlock()
		return
	}
	target := NodeType(nodeType)
	if node.NodeType == target {
		s.mu.Unlock()
		return
	}
	// antihunter and gatesensor are leaf classifications — never downgrade.
	if node.NodeType == NodeTypeAntihunter || node.NodeType == NodeTypeGatesensor {
		s.mu.Unlock()
		return
	}
	// gotailme can only be replaced by a more-specific sensor classification,
	// not by a downgrade to operator.
	if node.NodeType == NodeTypeGotailme && target == NodeTypeOperator {
		s.mu.Unlock()
		return
	}
	node.NodeType = target
	s.mu.Unlock()
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})
	// Persist so the badge survives container restarts — without this the
	// type reverts to gotailme on next NodeInfo packet after a reload,
	// because Load() reads the column but classification was never written.
	go s.persistNode(node)
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
		node.MacAddr = info.User.MacAddr
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
	node.IsOnline = isNodeOnline(node)

	// Classify based on the LongName the firmware self-reports. gotailme nodes
	// are flashed with a "GoTailMe" prefix at manufacture; anything else is a
	// plain Meshtastic client (operator handheld, 3rd-party hardware, etc.).
	// Sensor types (antihunter / gatesensor) are set by the dispatcher when it
	// sees their fingerprinted TEXTMSG output and are leaf — never overridden here.
	if node.NodeType != NodeTypeAntihunter && node.NodeType != NodeTypeGatesensor {
		if strings.HasPrefix(node.LongName, "GoTailMe") {
			node.NodeType = NodeTypeGotailme
		} else if node.NodeType == NodeTypeUnknown {
			node.NodeType = NodeTypeOperator
		}
	}

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
	now := time.Now()
	node.LastHeard = now
	node.IsOnline = true
	node.TelemetryUpdatedAt = &now

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

// HandleAntihunterHeartbeat applies position + temperature extracted from an
// AntiHunter TEXTMSG heartbeat. AntiHunter never emits a protobuf Position or
// Telemetry packet — everything rides inside the text body — so this is the
// only path that refreshes a remote sensor's map coordinates and OLED temp
// reading. The data map may carry `lat`, `lon`, `temperatureC`, `temperatureF`.
func (s *Service) HandleAntihunterHeartbeat(from uint32, lat, lon float64, data map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}

	now := time.Now()
	changed := false

	if lat != 0 || lon != 0 {
		node.Latitude = lat
		node.Longitude = lon
		changed = true
	}
	if v, ok := data["temperatureC"].(float64); ok && v != 0 {
		node.Temperature = v
		node.TemperatureC = v
		if tf, ok := data["temperatureF"].(float64); ok && tf != 0 {
			node.TemperatureF = tf
		} else {
			node.TemperatureF = v*9.0/5.0 + 32.0
		}
		node.TemperatureUpdatedAt = &now
		changed = true
	}
	// Battery rides in STATUS frames emitted by a peer C2 (diginode-cc) —
	// AntiHunter sensors never provide it. Only update on a non-zero reading
	// so a sensor's STATUS (without Batt) doesn't clobber a previously known
	// percentage from its own TELEMETRY_APP broadcasts.
	if v, ok := data["battery"].(float64); ok && v > 0 {
		pct := uint32(v)
		if pct > 100 {
			pct = 100
		}
		if node.BatteryLevel != pct {
			node.BatteryLevel = pct
			node.TelemetryUpdatedAt = &now
			changed = true
		}
	}

	node.LastHeard = now
	node.IsOnline = true

	if !changed {
		// Nothing extracted — don't spam WS updates.
		return
	}

	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})
	go s.persistNode(node)
}

// SetAHShortID records the AntiHunter short id for a node (the 2–5 char
// CONFIG_NODEID string, e.g. "AH01"). Only broadcasts on first discovery or
// when the id changes, so repeated heartbeats don't spam the WS hub.
func (s *Service) SetAHShortID(from uint32, shortID string) {
	if shortID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}
	if node.AHShortID == shortID {
		return
	}
	node.AHShortID = shortID
	s.hub.Broadcast(ws.Event{
		Type:    ws.EventNodeUpdate,
		Payload: node,
	})
	go s.persistNode(node)
}

// SetLastMessage stores the most recent sensor line from a node and broadcasts
// a node update. Only called for recognizable AntiHunter TEXTMSG payloads so the
// expanded node detail in the UI shows what that sensor last reported.
func (s *Service) SetLastMessage(from uint32, msg string) {
	if msg == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[from]
	if !exists {
		node = &Node{NodeNum: from}
		s.nodes[from] = node
	}
	node.LastMessage = msg
	node.LastHeard = time.Now()
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
		Type: ws.EventNodePosition,
		Payload: map[string]interface{}{
			"nodeNum":   from,
			"latitude":  node.Latitude,
			"longitude": node.Longitude,
			"altitude":  node.Altitude,
		},
	})

	go s.persistPosition(node)
}

// ClearAll removes all nodes from memory and the database.
func (s *Service) ClearAll(ctx context.Context) error {
	s.mu.Lock()
	s.nodes = make(map[uint32]*Node)
	s.mu.Unlock()

	_, err := s.db.Pool.Exec(ctx, `DELETE FROM nodes`)
	return err
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

// PrunePositions removes node positions older than the retention period.
func (s *Service) PrunePositions(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	result, err := s.db.Pool.Exec(ctx, `
		DELETE FROM node_positions WHERE timestamp < NOW() - $1 * INTERVAL '1 day'`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *Service) persistNode(node *Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO nodes (node_num, node_id, long_name, short_name, hw_model, role,
			latitude, longitude, altitude, battery_level, voltage,
			channel_utilization, air_util_tx, snr, last_heard, is_online,
			temperature_c, temperature_f, temperature_updated_at, last_message,
			node_type, ah_short_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, NOW())
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
			last_heard = COALESCE(EXCLUDED.last_heard, nodes.last_heard),
			is_online = EXCLUDED.is_online,
			temperature_c = COALESCE(EXCLUDED.temperature_c, nodes.temperature_c),
			temperature_f = COALESCE(EXCLUDED.temperature_f, nodes.temperature_f),
			temperature_updated_at = COALESCE(EXCLUDED.temperature_updated_at, nodes.temperature_updated_at),
			last_message = COALESCE(EXCLUDED.last_message, nodes.last_message),
			node_type = CASE
				WHEN EXCLUDED.node_type = '' THEN nodes.node_type
				ELSE EXCLUDED.node_type
			END,
			ah_short_id = CASE
				WHEN EXCLUDED.ah_short_id = '' THEN nodes.ah_short_id
				ELSE EXCLUDED.ah_short_id
			END,
			updated_at = NOW()`,
		node.NodeNum, nullStr(node.NodeID), nullStr(node.LongName), nullStr(node.ShortName),
		nullStr(node.HWModel), nullStr(node.Role),
		nullFloat(node.Latitude), nullFloat(node.Longitude), nullFloat(node.Altitude),
		nullInt(int(node.BatteryLevel)), nullFloat(float64(node.Voltage)),
		nullFloat(float64(node.ChannelUtilization)), nullFloat(float64(node.AirUtilTx)),
		nullFloat(float64(node.SNR)),
		nullTime(node.LastHeard), node.IsOnline,
		nullFloat(node.TemperatureC), nullFloat(node.TemperatureF),
		nullTimePtr(node.TemperatureUpdatedAt), nullStr(node.LastMessage),
		string(node.NodeType), node.AHShortID,
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

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullTimePtr(t *time.Time) interface{} {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}
