# DigiNode CC -- Technical Handbook

## 1. Overview

DigiNode CC (Command Center) is a Go backend that manages Meshtastic mesh network devices, drone detection, WiFi surveillance, geofencing, and multi-site operations. A single binary serves the REST API, WebSocket events, and embedded React frontend.

**Key metrics:**

| | |
|--|-------------|
| Language | Go 1.23 |
| Docker containers | 2 (Go + PostgreSQL) |
| API routes | 165+ |
| Database | PostgreSQL 16 (pgx) |

---

## 2. Architecture

```
                    +------------------+
                    |  React Frontend  |
                    |  (web/dist)      |
                    +--------+---------+
                             |
                    +--------+---------+
                    |   Chi HTTP Router |
                    |   165+ REST routes|
                    |   /ws WebSocket   |
                    +--------+---------+
                             |
              +--------------+--------------+
              |              |              |
     +--------+--+   +------+------+  +----+------+
     |  Auth/JWT  |   | Domain Svcs |  | WebSocket |
     |  Middleware |   | (20 pkgs)   |  |   Hub     |
     +------------+   +------+------+  +-----------+
                             |
              +--------------+--------------+
              |              |              |
     +--------+--+   +------+------+  +----+--------+
     | PostgreSQL |   | Meshtastic  |  | External    |
     |  (pgx)     |   | Dispatcher  |  | Integrations|
     +------------+   +------+------+  +-------------+
                             |              |
                      +------+------+  ADS-B, MQTT,
                      | Serial Port |  ACARS, TAK
                      | (Heltec V3) |
                      +-------------+
```

### Data Flow

```
Heltec Radio
    |
    v
Serial Port (/dev/ttyUSB0)
    |
    v
Frame Decoder (0x94 0xC3 + length + protobuf)
    |
    v
FromRadio Protobuf Decoder
    |
    v
Meshtastic Dispatcher (port-based routing + dedup)
    |
    +---> NodeHandler      --> nodes/service.go    --> DB + WS broadcast
    +---> DroneHandler     --> drones/service.go   --> DB + WS + inventory + FAA + geofence check
    +---> ChatHandler      --> chat/service.go     --> DB + WS + ring buffer
    +---> TargetDetected   --> inventory + alerts + webhooks + geofence check
    +---> AlertCallback    --> alerts/evaluator.go --> rule matching + trigger
    +---> WebhookCallback  --> webhooks/service.go --> HTTP dispatch
    +---> DeviceTime       --> serial/manager.go   --> time tracking
```

---

## 3. Project Structure

```
diginode-cc/
+-- cmd/diginode-cc/
|   +-- main.go              # Entry point, service wiring, startup
|   +-- version.go           # Build version variable
+-- internal/
|   +-- api/                 # HTTP handlers (24 files)
|   |   +-- server.go        # Router setup, Services struct, middleware
|   |   +-- handlers_*.go    # One file per domain (auth, drones, nodes, ...)
|   +-- auth/                # JWT authentication + 2FA
|   +-- audit/               # Audit logging service
|   +-- alerts/              # Alert rules + evaluation engine
|   +-- alarms/              # Audio/visual alarm config
|   +-- adsb/                # ADS-B aircraft feed polling
|   +-- acars/               # ACARS UDP listener
|   +-- chat/                # Mesh text message handling
|   +-- commands/            # Command queue + ACK tracking
|   +-- config/              # Env config + runtime AppConfig
|   +-- database/            # PostgreSQL pool + embedded migrations
|   |   +-- migrations/      # 7 SQL migration files
|   +-- drones/              # Drone detection + tracking
|   +-- exports/             # CSV/JSON data export
|   +-- faa/                 # FAA aircraft registry
|   +-- firewall/            # IP/CIDR blocking middleware
|   +-- geofences/           # Polygon geofence engine
|   +-- inventory/           # WiFi device inventory
|   +-- mail/                # SMTP email delivery
|   +-- meshtastic/          # Packet dispatcher + port numbers
|   +-- mqtt/                # MQTT broker federation
|   +-- nodes/               # Mesh node tracking
|   +-- permissions/         # Feature-level RBAC
|   +-- serial/              # Meshtastic serial framing + encoding
|   +-- sites/               # Multi-site management
|   +-- tak/                 # TAK/ATAK COT protocol
|   +-- targets/             # Target tracking + triangulation
|   +-- updates/             # Self-update (git-based)
|   +-- users/               # User CRUD + invitations
|   +-- webhooks/            # HTTP callback dispatch + HMAC
|   +-- ws/                  # WebSocket hub + client
+-- web/                     # React frontend (Vite + Tailwind)
+-- docker/
|   +-- Dockerfile           # Multi-stage build (Go + Node + Alpine)
+-- docker-compose.yml       # PostgreSQL + DigiNode CC
+-- Makefile                 # Build targets
+-- docs/                    # Documentation
```

---

## 4. Startup Sequence

`main.go` executes in this order:

1. **Logger** -- structured logging via `slog`
2. **Config** -- `config.Load()` reads environment variables
3. **Database** -- PostgreSQL connection pool via `pgx`
4. **Migrations** -- `db.Migrate()` runs embedded SQL files (000001-000007)
5. **WebSocket Hub** -- `ws.NewHub()` + goroutine
6. **Serial Manager** -- `serial.NewManager(cfg, hub)`
7. **Domain Services** -- 20 services instantiated:
   - auth, users, sites, nodes, drones, chat, commands, alerts, geofences, targets, inventory, webhooks, alarms, firewall, faa, exports, permissions, audit, mail, appConfig
8. **Dispatcher Wiring** -- Meshtastic dispatcher connected to:
   - `nodesSvc` (NodeHandler)
   - `dronesSvc` (DroneHandler)
   - `chatSvc` (ChatHandler)
   - Alert evaluation callback
   - Webhook dispatch callback
   - Device time callback
9. **Service Callbacks** -- Cross-service wiring:
   - `dronesSvc.SetNodeLookup(nodesSvc.LookupNodeIDAndSite)`
   - `dronesSvc.SetInventoryCallback(inventorySvc.Track)`
   - `dronesSvc.SetFAALookup(...)` (FAA registry enrichment)
   - `dronesSvc.SetGeofenceChecker(...)` -- point-in-polygon for drone positions
   - `dronesSvc.SetGeofenceNotifier(...)` -- WebSocket + alert event + webhook on breach
   - `serialMgr.SetTargetDetectedCallback(...)` -- inventory upsert + alert rules + webhooks + geofences
   - `chatSvc.SetBufferCallback(serialMgr.AddTextMessage)`
10. **Startup Data** -- Load from DB: alerts, geofences, webhooks, alarms, firewall, inventory, targets, appConfig defaults
11. **Optional Services** -- ADS-B poller, MQTT connection (if enabled)
12. **HTTP Server** -- Chi router with all 165+ routes
13. **Daily Pruning** -- Background goroutine: positions (30d), detections (30d), commands (180d)
14. **Signal Handler** -- Graceful shutdown on SIGTERM/SIGINT

---

## 5. Meshtastic Serial Protocol

### Frame Format

```
[0x94] [0xC3] [MSB_LEN] [LSB_LEN] [PROTOBUF_PAYLOAD...]
```

- Start bytes: `0x94 0xC3`
- Length: Big-endian 16-bit (max 512 bytes)
- Payload: Meshtastic protobuf (FromRadio or ToRadio)

### FromRadio Decoding

Manual protobuf decoder (no generated code). Field numbers:

| Field | Type | Content |
|-------|------|---------|
| 2 | sub-message | MyInfo (node number, max channels) |
| 3 | sub-message | NodeInfoLite (num, user, position, metrics) |
| 4 | sub-message | Config |
| 7 | varint | ConfigComplete |
| 8 | varint | Rebooted |
| 11 | sub-message | MeshPacket (from, to, channel, decoded data) |
| 12 | sub-message | Channel |
| 13 | sub-message | DeviceMetadata (firmware, bluetooth, wifi) |

### MeshPacket Port Numbers

| Port | Name | Handler |
|------|------|---------|
| 1 | TEXT_MESSAGE_APP | ChatHandler |
| 3 | POSITION_APP | NodeHandler.HandlePosition |
| 6 | ADMIN_APP | Admin commands (config, shutdown) |
| 67 | TELEMETRY_APP | NodeHandler.HandleTelemetry + HandleEnvironment |
| 10 | DETECTION_SENSOR_APP | DroneHandler |

### ToRadio Encoding

Builder functions in `serial/encode.go`:

```go
BuildTextMessage(to uint32, text string) []byte
BuildPosition(latI, lonI int32, altitude int32) []byte
BuildDeviceMetrics(batteryLevel uint32, voltage float32) []byte
BuildAdminShutdown(seconds uint32) []byte
BuildAdminDisplayConfig(screenOnSecs uint32) []byte
BuildAdminBluetoothConfig(enabled bool, mode, fixedPin uint32) []byte
BuildAdminNodedbReset() []byte  // AdminMessage field 100 = true
```

Each builds a complete ToRadio protobuf (caller wraps with `EncodeFrame`).

### Message Deduplication

The dispatcher filters mesh rebroadcasts:
- Hash key: `from << 32 | packetID`
- Window: 15 seconds
- Max entries: 512 (auto-pruned)

### Serial Reconnect

Exponential backoff with jitter:
- Base delay: 500ms
- Max delay: 15s
- Scale factor: 1.5x per attempt
- Jitter: +/-20%
- Resets to base on successful connection

---

## 6. Domain Services

### 6.1 Drones

**Detection lifecycle:**
1. Meshtastic detection sensor packet arrives (port 10 binary or `DRONE:` text line)
2. `HandleDroneDetection(from, payload)` parses JSON payload
3. `HandleDetection(detection)` creates/updates in-memory drone
4. `nodeLookup` resolves detecting node's ID and site
5. FAA enrichment (async, if serial number present)
6. **Geofence evaluation** -- `CheckPoint(lat, lon, "drone")` tests all armed geofences
   - Entry/exit state tracked per drone per geofence
   - On breach: WebSocket `geofence.event` + alert event (persisted) + notification bell + optional webhook (`alert.geofence`)
7. Persistence debouncing (200ms batch writes)
8. Detection history append (immediate)
9. WebSocket broadcast `drone.telemetry`
10. Inventory tracking callback
11. Alert evaluation + webhook dispatch

**Status enum:** UNKNOWN (grey), FRIENDLY (green), NEUTRAL (orange), HOSTILE (red)

**Text parser `DRONE:` format** (from AntiHunter sensor firmware):
```
<nodeId>: DRONE: <MAC> ID:<droneId> R<rssi> GPS:<lat>,<lon> ALT:<alt> SPD:<spd> OP:<opLat>,<opLon>
```
Fields mapped to `DroneDetection` JSON tags: `uasId`, `mac`, `rssi`, `latitude`, `longitude`, `altitude`, `speed`, `pilotLatitude`, `pilotLongitude`

**Drone simulation** (`scripts/simulate-drone.sh`):
- Bash script using `curl` to POST simulated DRONE lines to `/api/serial/simulate`
- Configurable coordinates (`--lat`, `--lon`), distance, speed, altitude, drone count
- `--with-targets` flag also sends `Target:` lines for inventory testing
- Drone approaches target coordinates with realistic RSSI progression

**API response** uses CC PRO field names: `droneId`, `lat`, `lon`, `operatorLat`, `operatorLon`, `faa`, `ts`, `nodeId`, `siteId`, `siteName`, `siteColor`, `siteCountry`, `siteCity`

### 6.2 Nodes

**Event handlers:**
- `HandleNodeInfo` -- mesh node metadata (name, hardware, role, firmware)
- `HandleTelemetry` -- device metrics (battery, voltage, channel utilization)
- `HandlePosition` -- GPS coordinates + device time sync
- `HandleEnvironment` -- temperature (C+F conversion), humidity, pressure

**API response** uses CC PRO field names: `id` (hex node ID), `name` (longName), `lat`, `lon`, `ts`, `lastSeen`, `temperatureC`, `temperatureF`, `temperatureUpdatedAt`

### 6.3 Alert Rules Engine

**Condition matching:**
- `macAddresses` -- exact MAC match
- `ouiPrefixes` -- OUI prefix match (first 3 bytes)
- `ssids` -- SSID string match
- `channels` -- channel number match
- `minRssi` / `maxRssi` -- RSSI range
- `matchMode` -- "ANY" (default, OR) or "ALL" (AND)

**Template rendering** with placeholders: `{mac}`, `{oui}`, `{ssid}`, `{channel}`, `{rssi}`, `{nodeId}`, `{nodeName}`, `{rule}`, `{severity}`

**Severity levels:** INFO, NOTICE, ALERT, CRITICAL

**Alert sources:**
- **Rule-based**: `Evaluate(DetectionEvent)` matches conditions → `Trigger(ruleID, ...)`
- **Geofence breach**: `TriggerDirect(severity, title, message, data)` — no rule needed, persisted directly to `alert_events`
- **Both** appear in Alerts > Recent Events and trigger WebSocket `alert` event + notification bell

### 6.4 Geofences

- Polygon storage as JSON array of `{lat, lng}` points
- Ray casting point-in-polygon algorithm
- Entity filtering: ADSB, drones, targets, devices (per-geofence checkboxes)
- Alarm config: enabled checkbox, level (INFO/NOTICE/ALERT/CRITICAL), message template with `{entity}` and `{geofence}` placeholders, trigger on entry/exit
- **Notify webhook** checkbox: when enabled, breaches fire `alert.geofence` webhook event
- Geofence map auto-centers on mesh nodes when no geofences exist (falls back to Zurich only if no nodes have GPS)
- Mesh node markers displayed on geofence map (blue=online, grey=offline) with name tooltips

**Breach detection flow:**
1. `drones.Service.evaluateGeofences()` called on every drone telemetry update
2. `geofences.CheckPoint(lat, lon, "drone")` tests all enabled + alarm-armed geofences
3. Entry/exit state tracked per drone per geofence in `geofenceState` map
4. On entry (was outside, now inside):
   - `geofences.NotifyViolation()` → WebSocket `geofence.event` (shows in terminal + notification bell)
   - `alerts.TriggerDirect()` → persisted to `alert_events` table (shows in Alerts > Recent Events)
   - If `notifyWebhook=true`: `webhooks.Dispatch("alert.geofence", payload)`
5. Target detections with GPS also checked against geofences (`appliesToTargets`)

### 6.5 Inventory (Device Tracking)

**Purpose:** Historical catalog of ALL detected WiFi/BLE devices from AntiHunter sensor nodes.

**Detection pipeline:**
1. Sensor sends `Target: WiFi AA:BB:CC:DD:EE:FF RSSI:-72 Name:device GPS:lat,lon` over mesh
2. Text parser produces `target-detected` event with `{mac, rssi, type, name, channel, lat, lon}`
3. `dispatchTextEvent` calls `onTargetDetected` callback
4. Callback executes in parallel:
   - **Inventory upsert**: `TrackFull(mac, manufacturer, ssid, deviceType, rssi, nodeID, lat, lon)`
     - OUI vendor lookup from MAC prefix
     - Running RSSI statistics (min/max/avg)
     - Hit counter increments
     - Location + detecting node tracked
   - **Alert rule evaluation**: matches MAC, OUI, SSID, channel, RSSI against active rules
   - **Webhook dispatch**: `target.detected` event to subscribed webhooks
   - **Geofence check**: if target has GPS, tests against armed geofences

**Persistence:** PostgreSQL `inventory_devices` table with upsert on MAC. Loaded into memory on startup via `Load()`.

**Promote to target:** `POST /api/inventory/{mac}/promote` copies MAC, name, deviceType, GPS coordinates, and manufacturer info into a new Target record.

### 6.6 Targets

**Purpose:** Manually curated threats of interest. Created by user promotion from inventory or manual entry.

**Status lifecycle:** `active` → `resolved`

**Fields:** name, description, targetType (WiFi/BLE/Drone/Vehicle/Person), MAC, lat/lon, status

**Position tracking:** `target_positions` table stores history; triangulation via weighted RSSI centroid from multiple observer nodes.

**Persistence:** PostgreSQL `targets` table. Loaded into memory on startup via `Load()`.

### 6.5 Commands

**Lifecycle:** PENDING -> SENT -> OK / ERROR / TIMEOUT

- Rate limiting: 1 command per node per 2 seconds
- Retry: configurable max retries (default 3)
- ACK handling via `HandleACK(cmdID, result)`
- Persistence: immediate on state change

### 6.7 Webhooks

- HMAC-SHA256 signing (`X-Signature-256` header)
- Event matching with wildcard (`*`) support
- Custom headers and HTTP method selection (POST/PUT/PATCH)
- 10-second timeout, async delivery (goroutine per webhook)

**Event types dispatched:**

| Event | Trigger |
|-------|---------|
| `mesh.text_message` | Chat message over mesh |
| `mesh.position` | GPS position update from node |
| `mesh.drone_detection` | Raw drone detection packet |
| `target.detected` | WiFi/BLE device detected by sensor |
| `alert.geofence` | Drone or target entered geofence (if `notifyWebhook` enabled on geofence) |

---

## 7. Authentication & Authorization

### JWT Authentication

- Algorithm: HMAC-SHA256
- Expiry: 24 hours
- Claims: `uid` (user ID), `email`, `role`
- Middleware extracts from `Authorization: Bearer <token>` header

### Role Hierarchy

```
ADMIN (3) > OPERATOR (2) > ANALYST (1) > VIEWER (0)
```

### Feature-Level RBAC

12 granular permissions with role defaults:

| Feature | ADMIN | OPERATOR | ANALYST | VIEWER |
|---------|:-----:|:--------:|:-------:|:------:|
| map.view | X | X | X | X |
| inventory.view | X | X | X | X |
| inventory.manage | X | X | | |
| targets.view | X | X | X | X |
| targets.manage | X | X | | |
| commands.send | X | X | | |
| commands.audit | X | X | X | |
| config.manage | X | | | |
| alarms.manage | X | X | | |
| exports.generate | X | X | X | |
| users.manage | X | | | |
| scheduler.manage | X | | | |

### Account Security

- **Password hashing:** bcrypt (DefaultCost)
- **Account lockout:** 5 failed attempts -> 15 minute lock
- **2FA:** TOTP with recovery codes
- **Password reset:** Token-based (4h expiry), email delivery

---

## 8. Database Schema

10 migrations (`internal/database/migrations/`), 33+ tables:

### Core Tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `users` | User accounts | email, password_hash, role, totp, lockout fields |
| `sites` | Deployment locations | name, color, region, country, city, lat/lon/radius |
| `nodes` | Mesh node state | node_num, node_id, telemetry (battery, voltage, temp, SNR) |
| `drones` | Detected drones | MAC, serial, lat/lon, pilot lat/lon, status, FAA data |
| `targets` | Tracked entities | name, MAC, lat/lon, status, tracking confidence |
| `inventory_devices` | WiFi devices | MAC, manufacturer, RSSI stats (min/max/avg/hits) |
| `geofences` | Polygon boundaries | polygon (JSONB), alarm config, entity filtering, notify_webhook |
| `alert_rules` | Alert conditions | condition (JSONB), severity, cooldown, match mode, notify_webhook |
| `commands` | Command queue | target_node, status lifecycle, retry tracking |

### History & Audit

| Table | Purpose | Retention |
|-------|---------|-----------|
| `node_positions` | GPS history | 30 days |
| `drone_detections` | Detection log | 30 days |
| `alert_events` | Triggered alerts | -- |
| `chat_messages` | Mesh text messages | -- |
| `webhook_deliveries` | Delivery tracking | -- |
| `audit_logs` | User action audit | 365 days |
| `commands` | Command history | 180 days |

### Configuration

| Table | Purpose |
|-------|---------|
| `app_config` | Runtime key-value config (33 defaults) |
| `serial_config` | Serial port settings (singleton) |
| `mqtt_config` | MQTT per-site config |
| `tak_config` | TAK/ATAK integration |
| `alarm_configs` | Alarm profiles |
| `alarm_sounds` | Sound files per severity |
| `visual_config` | Pulse/blink/stroke settings |
| `coverage_config` | Default radio coverage radius |
| `firewall_rules` | IP/CIDR blocks + jailed IPs |

### Enums

```sql
user_role:      ADMIN, OPERATOR, ANALYST, VIEWER
drone_status:   UNKNOWN, FRIENDLY, NEUTRAL, HOSTILE
alert_severity: INFO, NOTICE, ALERT, CRITICAL
command_status: PENDING, SENT, ACKED, OK, FAILED, ERROR, TIMEOUT
geofence_action: ALERT, LOG, ALARM
webhook_method: POST, PUT, PATCH
node_role:      CLIENT, ROUTER, REPEATER, TRACKER, SENSOR, TAK, ...
```

---

## 9. WebSocket Events

Connection: `GET /ws` (supports `?token=` query param or `Authorization` header)

### Init Event

Sent per-client on connect:
```json
{
  "type": "init",
  "payload": {
    "nodes": [...],
    "drones": [...],
    "geofences": [...]
  }
}
```

### Event Types

| Type | Trigger | Payload |
|------|---------|---------|
| `drone.telemetry` | New detection / position update | Full drone object |
| `drone.status` | Status change (UNKNOWN->HOSTILE) | Drone with new status |
| `drone.remove` | Drone deleted | {droneId, id, mac} |
| `node.update` | Node info/telemetry/environment | Full node object |
| `node.position` | GPS position update | {nodeNum, lat, lon, alt} |
| `node.remove` | Node deleted | {nodeNum, nodeId} |
| `chat.message` | Mesh text message | {fromNode, toNode, channel, text} |
| `alert` | Alert rule triggered | Alert event object |
| `command.update` | Command status change | Command object |
| `geofence.event` | Geofence violation | {geofence, entityType, lat, lng} |
| `inventory.update` | Device tracked | Device object |
| `target.update` | Target position update | Target object |
| `adsb.update` | ADS-B aircraft | Aircraft object |
| `acars.message` | ACARS message | Message object |
| `health` | System health | Health status |
| `config.update` | Config changed | Config diff |

---

### Notification Bell (Frontend)

Universal notification accumulator in the header. Memory-only (Zustand store, max 50 items).

**Sources (dispatched in `useWebSocketBridge.ts`):**
- `drone.telemetry` → "New drone detected" (first sighting only, tracked via `seenDrones` Set)
- `alert` → title + message from alert event (skips geofence-sourced to avoid duplicates)
- `chat.message` → "Chat from !nodeId" / "DM from !nodeId" (remote messages only)
- `geofence.event` → "Geofence: {name}" with alarm level severity
- `inventory.update` → "New device detected" (first hit only, `hits === 1`)

**UI:** Bell icon in header with red badge count. Click opens dropdown panel with severity color bars, dismiss (x) per item, "Mark all read", "Clear".

---

## 10. API Reference

### Authentication

| Method | Path | Auth | Description |
|--------|------|:----:|-------------|
| POST | `/api/auth/login` | No | Login, returns JWT |
| POST | `/api/auth/register` | No | Create first user (ADMIN) |
| POST | `/api/auth/forgot-password` | No | Request password reset email |
| POST | `/api/auth/reset-password` | No | Reset password with token |
| POST | `/api/auth/logout` | Yes | Logout (stateless) |
| GET | `/api/auth/me` | Yes | Current user info |
| POST | `/api/auth/legal-ack` | Yes | Accept terms of service |
| POST | `/api/auth/2fa/setup` | Yes | Generate TOTP secret |
| POST | `/api/auth/2fa/verify` | Yes | Verify TOTP code |
| POST | `/api/auth/2fa/confirm` | Yes | Confirm 2FA setup |
| POST | `/api/auth/2fa/disable` | Yes | Disable 2FA |
| POST | `/api/auth/2fa/recovery/regenerate` | Yes | New recovery codes |

### Users

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/users` | List all users |
| POST | `/api/users` | Create user |
| GET | `/api/users/me` | Current user (alias) |
| GET | `/api/users/features` | List feature permissions |
| POST | `/api/users/invite` | Send invitation |
| GET | `/api/users/{id}` | Get user by ID |
| PUT | `/api/users/{id}` | Update user |
| DELETE | `/api/users/{id}` | Delete user |
| POST | `/api/users/{id}/unlock` | Unlock locked account |
| PATCH | `/api/users/{id}/permissions` | Set feature permissions |
| PATCH | `/api/users/{id}/sites` | Set site access |
| POST | `/api/users/{id}/password-reset` | Admin password reset |
| GET | `/api/users/{id}/audit` | User audit log |

### Serial / Meshtastic

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/serial/ports` | List available serial ports |
| GET | `/api/serial/protocols` | List supported protocols |
| GET | `/api/serial/status` | Connection status |
| GET | `/api/serial/config` | Serial configuration |
| PUT | `/api/serial/config` | Update serial config |
| POST | `/api/serial/config/reset` | Reset to defaults |
| POST | `/api/serial/connect` | Open serial connection |
| POST | `/api/serial/disconnect` | Close serial connection |
| GET | `/api/serial/text-messages` | Poll ring buffer (?sinceSeq=N) |
| GET | `/api/serial/device-time` | Meshtastic device clock |
| POST | `/api/serial/text-message` | Send text to mesh |
| POST | `/api/serial/text-alert` | Broadcast alert |
| POST | `/api/serial/position` | Send GPS position |
| POST | `/api/serial/device-metrics` | Send battery/voltage |
| POST | `/api/serial/display-config` | Set screen-on duration |
| POST | `/api/serial/bluetooth-config` | Set Bluetooth mode |
| POST | `/api/serial/shutdown` | Shutdown Heltec device |
| POST | `/api/serial/nodedb-reset` | Reset Heltec node database |
| POST | `/api/serial/simulate` | Inject test packet or `{lines:[]}` for text parser |

### Resources (CRUD pattern)

Each resource follows: `GET /`, `POST /`, `GET /{id}`, `PUT /{id}`, `DELETE /{id}`

- `/api/sites` -- Deployment locations
- `/api/nodes` -- Mesh nodes (+ `POST /clear`, `GET /{id}/positions`)
- `/api/drones` -- Detected drones (+ `POST /clear`, `PUT /{id}/status`, `GET /{id}/detections`)
- `/api/targets` -- Tracked entities (+ `POST /clear`, `POST /{id}/resolve`, `GET /{id}/positions`)
- `/api/geofences` -- Polygon geofences
- `/api/alerts/rules` -- Alert rule definitions
- `/api/alerts/events` -- Triggered alert log (+ `POST /{id}/acknowledge`)
- `/api/webhooks` -- HTTP callbacks (+ `POST /{id}/test`)
- `/api/commands` -- Mesh command queue
- `/api/inventory` -- WiFi devices (+ `POST /clear`, `POST /{mac}/promote`)
- `/api/alarms` -- Alarm configs (+ `POST /sounds/{level}`, `DELETE /sounds/{level}`)
- `/api/firewall/rules` -- IP blocking rules

### Integrations

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/adsb/status` | ADS-B feed status |
| GET | `/api/adsb/tracks` | Current aircraft |
| GET/PUT | `/api/adsb/config` | ADS-B configuration |
| GET | `/api/mqtt/sites` | MQTT site configs |
| GET | `/api/mqtt/sites-status` | Connection status |
| GET/PUT | `/api/tak/config` | TAK configuration |
| GET | `/api/acars/status` | ACARS listener status |
| GET | `/api/updates/check` | Check for updates |
| POST | `/api/updates/trigger` | Apply update |
| GET | `/api/oui/resolve/{mac}` | MAC vendor lookup |
| GET | `/api/faa/lookup/{serial}` | FAA registry lookup |
| GET | `/api/audit` | System audit log |

---

## 11. Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | (required) | JWT signing secret |
| `DATABASE_URL` | `postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable` | PostgreSQL connection |
| `LISTEN_ADDR` | `:3000` | HTTP listen address |
| `SERIAL_DEVICE` | (empty) | Meshtastic serial port |
| `SERIAL_BAUD` | `115200` | Serial baud rate |
| `MQTT_ENABLED` | `false` | Enable MQTT federation |
| `MQTT_BROKER_URL` | `tcp://localhost:1883` | MQTT broker URL |
| `ADSB_ENABLED` | `false` | Enable ADS-B polling |
| `ADSB_URL` | `http://localhost:8080/data/aircraft.json` | dump1090 JSON URL |
| `ACARS_ENABLED` | `false` | Enable ACARS listener |
| `ACARS_PORT` | `5555` | ACARS UDP port |
| `TAK_ENABLED` | `false` | Enable TAK/ATAK |
| `TAK_ADDR` | (empty) | TAK server address |
| `SMTP_HOST` | (empty) | SMTP server |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_USER` | (empty) | SMTP username |
| `SMTP_PASSWORD` | (empty) | SMTP password |
| `SMTP_FROM` | (empty) | Sender email |
| `GEOIP_DB_PATH` | (empty) | GeoIP database path |

### Runtime AppConfig (33 keys)

Stored in `app_config` table, seeded on first startup:

| Key | Default | Description |
|-----|---------|-------------|
| `appName` | "DigiNode CC" | Application name |
| `protocol` | "meshtastic-binary" | Serial protocol |
| `ackTimeoutMs` | 3000 | Command ACK timeout |
| `resultTimeoutMs` | 10000 | Command result timeout |
| `maxRetries` | 2 | Command max retries |
| `perNodeCmdRate` | 8 | Commands/min per node |
| `globalCmdRate` | 30 | Commands/min global |
| `defaultRadiusM` | 50 | Default coverage radius |
| `nodePosRetentionDays` | 30 | Position history retention |
| `commandRetentionDays` | 180 | Command history retention |
| `auditRetentionDays` | 365 | Audit log retention |
| `mapTileUrl` | OSM tile URL | Map tile server |
| `invitationExpiryHours` | 48 | Invitation token expiry |
| `passwordResetExpiryHours` | 4 | Reset token expiry |

---

## 12. Deployment

### Docker Compose

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: diginode
      POSTGRES_PASSWORD: diginode
      POSTGRES_DB: diginode
    volumes:
      - pgdata:/var/lib/postgresql/data

  diginode-cc:
    build: .
    ports:
      - "3000:3000"
    environment:
      DATABASE_URL: postgres://diginode:diginode@postgres:5432/diginode?sslmode=disable
      JWT_SECRET: your-secret-here
      SERIAL_DEVICE: /dev/ttyUSB0
    devices:
      - /dev/ttyUSB0:/dev/ttyUSB0
    depends_on:
      - postgres
```

### Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build Go binary |
| `make build-frontend` | Build React frontend |
| `make all` | Build both |
| `make run` | Build + run locally |
| `make docker-prod-push` | Build ARM64 image + push to Docker Hub |
| `make docker-prod-build` | Build ARM64 image locally |
| `make docker-up` | Start containers |
| `make docker-down` | Stop containers |
| `make docker-logs` | View container logs |

### Production (Raspberry Pi)

1. Push image: `make docker-prod-push`
2. Watchtower auto-updates hourly, or force-update via Watchtower HTTP API

---

## 13. Dependencies

```
github.com/go-chi/chi/v5          # HTTP router
github.com/gorilla/websocket      # WebSocket
github.com/golang-jwt/jwt/v5      # JWT authentication
github.com/jackc/pgx/v5           # PostgreSQL driver
github.com/golang-migrate/migrate/v4  # Database migrations
github.com/pquerna/otp            # TOTP 2FA
go.bug.st/serial                  # Serial port I/O
golang.org/x/crypto               # bcrypt
github.com/eclipse/paho.mqtt.golang  # MQTT client
```

---

## 14. Service Token Authentication

External services can authenticate via service token instead of user login:

- Set `CC_JWT_SECRET` environment variable on both DigiNode CC and the connecting service
- Send `Authorization: Bearer <CC_JWT_SECRET>` — the auth middleware grants synthetic admin claims
- No `/api/auth/login` roundtrip needed; user password changes don't break machine-to-machine connections
- Username/password login remains available as a fallback
