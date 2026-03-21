# CLAUDE.md - DigiNode CC

## Project Overview

**DigiNode CC** (Command Center) is a Go replacement for AntiHunter CC PRO (NestJS). It's the backend that DigiNode hardware talks to via Meshtastic serial protocol. Standalone repo with its own React + Tailwind frontend.

## Architecture

### Backend (Go)
- **Entry point**: `cmd/diginode-cc/main.go`
- **API Server**: `internal/api/server.go` — Chi router, REST API + WebSocket
- **Serial**: `internal/serial/` — Meshtastic serial framing, protobuf decoding
- **Meshtastic**: `internal/meshtastic/` — Packet dispatcher, port numbers
- **Auth**: `internal/auth/` — JWT, bcrypt, TOTP 2FA, RBAC middleware
- **Database**: `internal/database/` — PostgreSQL via pgx, golang-migrate
- **WebSocket**: `internal/ws/` — Hub + client pattern

### Frontend (React + TypeScript + Tailwind + Vite)
- **Location**: `web/`
- **Pages**: `web/src/pages/`
- **Components**: `web/src/components/`
- **Stores**: `web/src/stores/` (Zustand)
- **API Client**: `web/src/api/client.ts`
- **WebSocket**: `web/src/api/websocket.ts`

### Domain Packages
| Package | Purpose |
|---------|---------|
| `internal/nodes/` | Mesh node tracking & telemetry |
| `internal/drones/` | Drone detection & cache |
| `internal/commands/` | Command queue with rate limiting |
| `internal/chat/` | Mesh text message routing |
| `internal/alerts/` | Alert rules engine |
| `internal/webhooks/` | HTTP callback dispatch + HMAC |
| `internal/geofences/` | Polygon geofence engine |
| `internal/targets/` | Target tracking & triangulation |
| `internal/inventory/` | WiFi device inventory |
| `internal/adsb/` | ADS-B feed polling (Dump1090) |
| `internal/acars/` | ACARS UDP listener |
| `internal/tak/` | TAK/ATAK COT protocol |
| `internal/mqtt/` | MQTT broker federation |
| `internal/firewall/` | IP/geo-blocking middleware |
| `internal/faa/` | FAA aircraft registry import |
| `internal/alarms/` | Audio/visual alert config |
| `internal/sites/` | Multi-site management |
| `internal/users/` | User CRUD, RBAC, invitations |
| `internal/exports/` | CSV/JSON data export |
| `internal/updates/` | Git-based self-update |
| `internal/mail/` | SMTP email delivery |

## Development

```bash
# Build
make build           # Go binary
make build-frontend  # React frontend
make all             # Both

# Run locally
export JWT_SECRET=dev-secret
export DATABASE_URL=postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable
make run

# Docker
docker compose up -d  # diginode-cc + postgres (2 containers)
make docker-prod-push # Build ARM64 + push to Docker Hub
```

## Database

- PostgreSQL 16 via pgx connection pool
- Migrations embedded in binary (`internal/database/migrations/`)
- Schema: ~20 tables (users, sites, nodes, drones, alerts, etc.)
- No ORM — direct SQL queries

## Key Dependencies

```
go.bug.st/serial              # Meshtastic serial I/O
github.com/go-chi/chi/v5      # HTTP router
github.com/gorilla/websocket  # WebSocket
github.com/golang-jwt/jwt/v5  # JWT auth
github.com/jackc/pgx/v5       # PostgreSQL
github.com/golang-migrate/migrate/v4  # DB migrations
github.com/pquerna/otp         # TOTP 2FA
github.com/eclipse/paho.mqtt.golang  # MQTT
golang.org/x/crypto            # bcrypt
```

## Relationship to CC PRO

This project replaces `TheRealSirHaXalot/AntiHunter-Command-Control-PRO`. Same API surface so gotailme can connect to either backend. Key improvements:
- 2 Docker containers (not 4)
- ~6K Go LOC (not 29K TypeScript)
- 30s build (not 6min qemu cross-compile)
- Zero npm packages in production
- Single binary serves API + frontend

## Git Workflow

- Main branch: `master`
- Deploy: `make docker-prod-push`
