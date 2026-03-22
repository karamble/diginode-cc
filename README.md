# DigiNode CC

Command center for Meshtastic mesh networks. Manages nodes, drone detection, WiFi surveillance, geofencing, alerts, and multi-site operations from a single Go binary serving REST API, WebSocket, and embedded React frontend.

## Architecture

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
     |  Middleware |   | (20+ pkgs)  |  |   Hub     |
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

**Stack:** Go 1.23 | PostgreSQL 16 | React 18 + TypeScript + Tailwind | Vite | Docker (2 containers)

## Features

- **Mesh Network Management** — Node discovery, telemetry (battery, GPS, temperature), online/offline tracking, BLE MAC exposure
- **Bidirectional Chat** — Broadcast and DM messaging over Meshtastic mesh, ring buffer history
- **Drone Detection** — Real-time tracking with FAA registry enrichment, status classification (FRIENDLY/NEUTRAL/HOSTILE)
- **WiFi Device Inventory** — MAC tracking, OUI vendor lookup, RSSI statistics, target promotion
- **Geofencing** — Visual polygon editor, entry/exit triggers, alarm integration
- **Alert Rules Engine** — Condition matching (MAC, OUI, SSID, RSSI, channel), severity levels, cooldowns
- **Target Tracking** — Triangulation from multiple nodes, bearing + RSSI, position history
- **Webhooks** — HMAC-SHA256 signed HTTP callbacks with event filtering and wildcard support
- **Command Queue** — Rate-limited mesh commands with ACK tracking, retries, and timeout handling
- **Multi-Site** — Site-scoped data, per-user site access, MQTT federation between sites
- **ADS-B / ACARS** — Aircraft tracking (dump1090 JSON) and ACARS UDP message capture
- **TAK/ATAK** — Cursor-on-Target protocol integration
- **Firewall** — IP/CIDR blocking, GeoIP, jail tracking
- **Security** — JWT auth, TOTP 2FA, bcrypt, account lockout, feature-level RBAC (4 roles), audit logging
- **Self-Update** — Git-based version checking with rollback

## Quick Start

### Docker Compose

```bash
docker compose up -d
```

Open `http://localhost:3000` and log in with:
- **Email:** `admin@example.com`
- **Password:** `admin`

> Change the default credentials immediately after first login.

### Without Docker

Prerequisites: Go 1.23+, Node.js 20+, PostgreSQL 16

```bash
# 1. Start PostgreSQL (or use an existing instance)
createdb diginode

# 2. Build frontend + backend
make all

# 3. Run
export JWT_SECRET="your-secret"
export DATABASE_URL="postgres://user:pass@localhost:5432/diginode?sslmode=disable"
make run
```

The server starts on `http://localhost:3000`, serving both the API and the built frontend.

### Development (hot-reload)

Run the Go backend and Vite dev server separately for live frontend reloading:

```bash
# Terminal 1 — Go backend on :3000
export JWT_SECRET="dev-secret"
export DATABASE_URL="postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable"
make build && ./diginode-cc

# Terminal 2 — Vite dev server on :5173 (proxies /api and /ws to :3000)
cd web && npm install && npm run dev
```

Open `http://localhost:5173` for hot-reloading frontend development. API calls and WebSocket connections are automatically proxied to the Go backend.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | *(required)* | JWT signing secret |
| `DATABASE_URL` | `postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable` | PostgreSQL connection |
| `LISTEN_ADDR` | `:3000` | HTTP listen address |
| `SERIAL_DEVICE` | *(empty)* | Meshtastic serial port (e.g. `/dev/ttyUSB0`) |
| `SERIAL_BAUD` | `115200` | Serial baud rate |
| `MQTT_ENABLED` | `false` | Enable MQTT federation |
| `ADSB_ENABLED` | `false` | Enable ADS-B aircraft polling |
| `ACARS_ENABLED` | `false` | Enable ACARS UDP listener |
| `TAK_ENABLED` | `false` | Enable TAK/ATAK integration |

See the [Technical Handbook](docs/TECHNICAL_HANDBOOK.md) for the full configuration reference including SMTP, GeoIP, and runtime AppConfig keys.

## Building

### Makefile Targets

```bash
make all                # Build frontend + backend
make build              # Build Go binary only
make build-frontend     # Build React frontend only
make run                # Build + run locally
make test               # Run Go tests
make clean              # Remove binary + dist/
```

### Docker

```bash
# Build ARM64 production image and push to Docker Hub
make docker-prod-push

# Build image locally
make docker-prod-build

# Container management
make docker-up
make docker-down
make docker-logs
```

## Project Structure

```
diginode-cc/
├── cmd/diginode-cc/        # Entry point + startup wiring
├── internal/
│   ├── api/                # Chi router, 165+ REST handlers
│   ├── auth/               # JWT + 2FA (TOTP) + bcrypt
│   ├── serial/             # Meshtastic serial framing + encoding
│   ├── meshtastic/         # Packet dispatcher + port routing
│   ├── nodes/              # Mesh node tracking + telemetry
│   ├── drones/             # Drone detection + FAA enrichment
│   ├── chat/               # Mesh messaging + ring buffer
│   ├── commands/           # Command queue + ACK tracking
│   ├── alerts/             # Rule engine + evaluator
│   ├── geofences/          # Polygon geofence engine
│   ├── targets/            # Target tracking + triangulation
│   ├── inventory/          # WiFi device inventory + OUI
│   ├── webhooks/           # HTTP dispatch + HMAC signing
│   ├── ws/                 # WebSocket hub + broadcast
│   ├── database/           # PostgreSQL pool + migrations
│   ├── config/             # Environment + runtime config
│   ├── users/              # User CRUD + invitations + RBAC
│   ├── sites/              # Multi-site management
│   ├── adsb/               # ADS-B aircraft feed
│   ├── acars/              # ACARS UDP listener
│   ├── mqtt/               # MQTT broker federation
│   ├── tak/                # TAK/ATAK COT protocol
│   ├── faa/                # FAA aircraft registry
│   ├── firewall/           # IP blocking + GeoIP
│   ├── alarms/             # Audio/visual alerts
│   ├── exports/            # CSV/JSON export
│   ├── audit/              # Action audit logging
│   ├── permissions/        # Feature-level RBAC
│   ├── mail/               # SMTP delivery
│   └── updates/            # Self-update + rollback
├── web/                    # React frontend (Vite + Tailwind)
│   ├── src/
│   │   ├── pages/          # 17 page components
│   │   ├── stores/         # Zustand state stores
│   │   ├── api/            # REST + WebSocket clients
│   │   └── types/          # TypeScript interfaces
│   └── dist/               # Built frontend (served by Go)
├── docker/
│   └── Dockerfile          # Multi-stage (Go + Node + Alpine)
├── docker-compose.yml
├── Makefile
└── docs/
    └── TECHNICAL_HANDBOOK.md
```

## Meshtastic Integration

Communicates with a Heltec WiFi LoRa 32 V3 (or compatible Meshtastic device) over serial:

- **Protocol:** Binary frames (`0x94 0xC3` + length + protobuf) with text fallback
- **Handshake:** `wantConfig` on connect triggers full node/config dump
- **Keepalive:** Heartbeat every 15s, config refresh every 10 minutes
- **Deduplication:** SHA-256 hash with 15s window (512 entry max)
- **Reconnect:** Exponential backoff (500ms → 15s, 1.5x, ±20% jitter)
- **Port routing:** TEXT_MESSAGE (1), POSITION (3), ADMIN (6), TELEMETRY (67), DETECTION_SENSOR (10)

## WebSocket

Connect to `GET /ws` with JWT token (query param `?token=` or `Authorization` header).

**Events:** `init`, `drone.telemetry`, `drone.status`, `drone.remove`, `node.update`, `node.position`, `node.remove`, `chat.message`, `alert`, `command.update`, `geofence.event`, `inventory.update`, `target.update`, `adsb.update`, `acars.message`, `health`, `config.update`

## Frontend

17-page React dashboard:

| Page | Description |
|------|-------------|
| Map | Leaflet map with color-coded node/drone/target markers |
| Nodes | Mesh node list with telemetry, online/offline status |
| Drones | Drone detections with FAA data, status management |
| Chat | Broadcast + DM messaging with unread indicators |
| Targets | Entity tracking with triangulation |
| Inventory | WiFi device list with OUI lookup |
| Alerts | Rule configuration + event log |
| Geofences | Visual polygon editor on Leaflet map |
| Commands | Mesh command queue with status tracking |
| Webhooks | HTTP callback configuration + testing |
| ADS-B | Aircraft tracking feed |
| ACARS | ACARS message viewer |
| Terminal | Real-time WebSocket event monitor |
| Config | Application settings |
| Exports | CSV/JSON data export |
| Users | User management + permissions |

## Documentation

- **[Technical Handbook](docs/TECHNICAL_HANDBOOK.md)** — Full architecture, protocol details, API reference, database schema, deployment guide

## License

This project is licensed under the [ISC License](LICENSE).
