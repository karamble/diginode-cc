# Multi-Phase Plan: DigiNode CC → CC PRO Full Parity

## Context
Gap analysis identified missing features across 6 areas. This plan brings DigiNode CC to full CC PRO parity in phased delivery. Each phase is independently testable and committable.

---

## Phase 1: OUI Database + Inventory Enhancements

**Goal:** Full IEEE OUI vendor resolution, inventory search/filter, MAC flags

### 1.1 Full IEEE OUI Database
- Download IEEE OUI CSV from `https://standards-oui.ieee.org/oui/oui.csv`
- Parse into Go map at startup (load from embedded file or `data/oui.csv`)
- Replace hardcoded 10-entry `ouiDB` map in `internal/inventory/service.go`
- Add `GET /api/oui/import` to reload from file
- ~30k entries, fast map lookup

### 1.2 MAC Flags
- Add `locallyAdministered` and `multicast` bool fields to inventory `Device` struct
- Compute from MAC: bit 1 of first byte = locally administered, bit 0 = multicast
- Add to DB schema (`ALTER TABLE inventory_devices ADD COLUMN locally_administered BOOLEAN, multicast BOOLEAN`)
- Display in inventory page

### 1.3 Inventory Search + Sort
- Frontend: add search input (filters MAC, manufacturer, SSID, type)
- Frontend: clickable column headers for sorting (14 keys like CC PRO)
- Add `channel` field to inventory Device struct + DB + frontend column

### Files
- `internal/inventory/service.go` — OUI loading, MAC flags, channel field
- `internal/inventory/oui.go` — NEW: IEEE OUI CSV parser
- `data/oui.csv` — NEW: downloaded IEEE database
- `internal/database/migrations/000011_inventory_enhancements.up.sql`
- `web/src/pages/InventoryPage.tsx` — search, sort, new columns

---

## Phase 2: Drone Frontend (Map-Integrated View)

**Goal:** Full drone tracking UI with map markers, flight trails, operator pins

### 2.1 Drone Map Page
- New `DronesPage.tsx` with split layout: map (main) + sidebar table
- Leaflet map with dark tiles (matching geofence page)
- Drone markers: colored by status (grey/green/orange/red), pulsing when active
- Operator pin markers: connected to drone with dashed line
- Flight trail: polyline of last 80 positions (stored in Zustand store)
- Auto-center on active drones

### 2.2 Drone Sidebar Table
- Compact table: ID, status buttons (colored), MAC, RSSI bar, altitude, speed, heading, last seen
- FAA data expandable row (manufacturer, model, registrant, N-number)
- Click row to focus map on drone
- Status change buttons (UNKNOWN/FRIENDLY/NEUTRAL/HOSTILE) with colored active state

### 2.3 Drone Detections API
- Implement `handleGetDroneDetections` (currently returns empty array)
- Query `drone_detections` table filtered by drone MAC/ID
- Return position history for flight trail rendering

### 2.4 Drone Store
- Zustand store `dronesStore.ts` — already exists, enhance with:
  - Trail tracking: `trails: Map<string, {lat, lon, ts}[]>` (max 80 points per drone)
  - `appendTrail(droneId, lat, lon)` called on each telemetry update

### Files
- `web/src/pages/DronesPage.tsx` — REWRITE: map + sidebar
- `web/src/stores/dronesStore.ts` — add trail tracking
- `internal/api/handlers_drones.go` — implement `handleGetDroneDetections`

---

## Phase 3: Full Command Builder (51 Commands)

**Goal:** Structured command system matching CC PRO's command-builder.ts

### 3.1 Command Registry
- New `internal/commands/builder.go` — command type registry with:
  - Command name constants (51 types)
  - Per-command parameter validators
  - `Build(cmdType, target, params) → (line string, err error)`
  - Target patterns: `@ALL`, `@NODE_xxx`, MAC addresses

### 3.2 Command Types (grouped)
- **Scanning**: SCAN_START, SCAN_STOP, DEVICE_SCAN_START, DEVICE_SCAN_STOP
- **Activity**: DRONE_START, DRONE_STOP, DEAUTH_START, DEAUTH_STOP, RANDOMIZATION_START, RANDOMIZATION_STOP, BASELINE_START, BASELINE_STOP
- **Triangulation**: TRIANGULATE_START (MAC, duration, rfEnv, wifiPwr, blePwr), TRIANGULATE_STOP, TRIANGULATE_RESULTS
- **Config**: CONFIG_CHANNELS (range syntax 1..14), CONFIG_TARGETS (pipe-delimited MACs), CONFIG_RSSI, CONFIG_NODEID
- **Erase**: ERASE_REQUEST, ERASE_FORCE, ERASE_CANCEL, AUTOERASE_ENABLE, AUTOERASE_DISABLE, AUTOERASE_STATUS
- **Battery**: BATTERY_SAVER_START, BATTERY_SAVER_STOP, BATTERY_SAVER_STATUS
- **Status**: STATUS, BASELINE_STATUS, VIBRATION_STATUS
- **System**: REBOOT, STOP

### 3.3 Command API Enhancement
- `POST /api/commands` accepts structured `{type, target, params}` instead of raw payload
- Validates via builder before enqueuing
- Returns formatted mesh line for confirmation

### 3.4 Command Page UI
- Command selector dropdown grouped by category
- Per-command parameter forms with validation
- Node selector (dropdown of online nodes + @ALL)
- Command history table with status badges

### 3.5 Command ACK Enrichment
- Add `ackKind`, `ackStatus`, `ackNode`, `resultText`, `errorText` fields to Command struct + DB
- Parse ACK text lines from text parser to populate these fields

### Files
- `internal/commands/builder.go` — NEW: command registry + validators
- `internal/commands/service.go` — enhance with structured input
- `internal/database/migrations/000012_command_enrichment.up.sql`
- `web/src/pages/CommandsPage.tsx` — REWRITE: structured UI

---

## Phase 4: Target Tracking + Triangulation

**Goal:** Advanced target tracking with confidence scoring, triangulation protocol, enriched target model

### 4.1 Target Schema Extension
- Add fields: `url`, `tags` (TEXT[]), `createdBy`, `firstNodeId`, `trackingConfidence`, `trackingUncertainty`, `triangulationMethod`, `siteId`
- DB migration + Go struct + API handlers + frontend form

### 4.2 Target Tracking Service
- New `internal/targets/tracking.go`:
  - Sliding window (45s) of detections per MAC
  - Weighted RSSI centroid calculation
  - Confidence scoring (0-1) based on node count, sample count, spread
  - Persistence thresholds: confidence >= 0.35, movement >= 8m, interval >= 15s
  - Per-node contribution tracking

### 4.3 Triangulation Protocol (T_D / T_F / T_C)
- Wire `T_D` (intermediate data), `T_F` (final fix), `T_C` (complete) text events in dispatcher
- T_D → feed tracking service with per-node RSSI + GPS
- T_F → apply final position with confidence/uncertainty to target
- T_C → mark triangulation complete

### 4.4 Target Page Enhancements
- Add tags, url, notes fields to create/edit form
- Show tracking confidence badge
- Position history table (from `target_positions`)
- Search across name, MAC, tags, notes

### Files
- `internal/targets/service.go` — add new fields
- `internal/targets/tracking.go` — NEW: weighted tracking service
- `internal/serial/manager.go` — wire T_D/T_F/T_C events
- `internal/database/migrations/000013_target_enrichment.up.sql`
- `web/src/pages/TargetsPage.tsx` — enhanced form + tracking display

---

## Phase 5: Alert Enhancements + Email

**Goal:** Email notifications, matched criteria logging, audio/visual toggles

### 5.1 Email Alert Notifications
- Wire existing `mail.Service` to alert trigger path
- Add per-rule fields: `notifyEmail` (bool), `emailRecipients` ([]string)
- HTML email template: rule name, severity, MAC, SSID, RSSI, node, coordinates, timestamp
- Subject: `[DigiNode CC] Alert: {ruleName}`
- DB migration for new fields on `alert_rules`

### 5.2 Alert Audio/Visual Toggles
- Add per-rule `notifyVisual` (bool, default true), `notifyAudible` (bool, default true)
- Frontend alert rule form: checkboxes for each delivery channel
- WebSocket event includes flags so frontend can decide sound/notification

### 5.3 Matched Criteria Logging
- When alert rule fires, record which conditions matched in `data` JSON:
  - `matchedCriteria: ["mac", "rssi"]` (list of condition keys that triggered)
  - Include `lat`, `lon` from detection if available
- Display in alert event detail view

### 5.4 Inventory MAC-based Alerts
- New condition type: `inventoryMacs` — match against known inventory MACs
- Alert evaluator checks `inventorySvc.GetByMAC(evt.MAC)` to see if device is known

### Files
- `internal/alerts/service.go` — email integration, new fields
- `internal/alerts/evaluator.go` — matched criteria, inventoryMacs condition
- `internal/database/migrations/000014_alert_enhancements.up.sql`
- `web/src/pages/AlertsPage.tsx` — form updates for email/audio/visual toggles

---

## Phase 6: Webhook + Geofence Polish

**Goal:** Delivery history, MQTT geofence federation, client-side geofence detection

### 6.1 Webhook Delivery History
- Persist each delivery attempt: `webhook_deliveries` table (exists in schema)
- Store: webhookId, eventType, payload, statusCode, responseBody (truncated), errorMessage, timing
- API: `GET /api/webhooks/{id}/deliveries` returns last 20
- Frontend: delivery history tab per webhook

### 6.2 Standardized Webhook Event Types
- Define enum: `ALERT_TRIGGERED`, `INVENTORY_UPDATED`, `TARGET_DETECTED`, `DRONE_TELEMETRY`, `NODE_TELEMETRY`, `COMMAND_ACK`, `GEOFENCE_BREACH`
- Validate event subscription in webhook CRUD
- Frontend: event type multi-select in webhook form

### 6.3 MQTT Geofence Federation
- New `internal/mqtt/geofences.go`: publish geofence CRUD to MQTT topics
- Subscribe to inbound geofence upsert/delete from remote sites
- Topic pattern: `diginode/{siteId}/geofences/{action}`

### 6.4 Client-Side Geofence Detection
- Frontend `geofenceStore.ts`: add `processCoordinateEvent()` with ray-casting
- Track entity state (inside/outside) per geofence per entity
- Fire notifications on state transitions
- Complements server-side detection for immediate UI feedback

### 6.5 Geofence Color on Map
- Store and display `color` field (already in DB/Go, just wire to frontend map rendering)

### Files
- `internal/webhooks/service.go` — delivery persistence
- `internal/mqtt/geofences.go` — NEW: MQTT federation
- `web/src/stores/geofenceStore.ts` — client-side detection
- `web/src/pages/WebhooksPage.tsx` — delivery history + event type selector

---

## Completion Status

All 6 phases are **COMPLETE** as of 2026-03-25.

| Phase | Status | Key Commits |
|-------|--------|-------------|
| 1 — OUI + Inventory | DONE | OUI CSV loader, MAC flags, search/sort |
| 2 — Drone UI | DONE | Map + sidebar, trail tracking, detection history |
| 3 — Command Builder | DONE | 46 structured commands, category UI |
| 4 — Target Tracking | DONE | T_D/T_F/T_C protocol, weighted centroid, confidence |
| 5 — Alert Email | DONE | notifyEmail/Visual/Audible, HTML templates, per-rule toggles |
| 6 — Webhook + Geofence | DONE | Delivery history, MQTT geofence federation |

**Beyond parity (also completed):**
- Map tile proxy with caching (Jawg Matrix, OSM, Esri) + preload API
- API rate limiting (per-IP, per-endpoint group)
- Login anti-automation timing guard
- TAK protocol expansion (TCP/UDP/TLS/auth)
- OpenSky Network aircraft enrichment (OAuth2)
- Planespotters aircraft photo lookup
- 40+ configurable env vars (full CC PRO parity)
- `.env` file setup for Docker deployment
