# Fleet Security tab — Plan

> **Note on storage**: this plan must be saved at `/home/user/go/src/github.com/karamble/diginode-cc/FLEET_SECURITY.md` (the diginode-cc repo root). During plan mode the planner can only edit the system plan path; the **first implementation step** is to copy this file there. The two files should be kept identical until implementation begins, after which the in-repo copy is the canonical reference.

---

## 1. Context

Halberd nodes deployed in the field consist of an ESP32 + Heltec pair where the Heltec runs stock Meshtastic with the SerialModule in `TEXTMSG` mode. The ESP32 emits ASCII over UART, the Heltec wraps each line in a LoRa `TEXT_MESSAGE_APP` packet under the data-channel PSK. The PSK is currently a deploy-time secret and rotating it requires touching every Halberd by hand.

diginode-cc already speaks the Meshtastic phone API directly to its locally-attached Heltec over USB (binary protobuf framing in `internal/serial/`, no Python CLI dependency, with admin-port builders for `set_config`/reboot/etc. already in place). What's missing is:

1. A unified operator view of who-can-admin-what across the fleet.
2. The ability to rotate channel PSKs fleet-wide via remote PKC admin.
3. Provisioning and recovery flows for the control-center's own keypair.
4. Fleet-wide visibility of `is_managed` state and `admin_key` lists, so an operator never silently locks themselves out.

**Outcome**: a new `/fleet-security` page in the React frontend and a new Go service+handlers in the backend that together expose: control-center identity management, per-node trust roster (admin keys + is_managed), per-channel PSK lifecycle, plus a Recovery flow for compromised/lost identity. Backed by Meshtastic 2.5+ PKC remote admin, so PSK changes don't require physical access.

**Design philosophy chosen**: **permissive**. Every action is reachable from the UI; safety lives in confirmation gates, lockout-prevention checks, and audit logging — not in a separate policy reconciliation layer. Reasoning: smaller surface to ship, more flexibility for one-off ops, audit log compensates for the lack of declarative policy. A future "policy-driven" mode can be layered on top once fleet size justifies it.

---

## 2. Architecture overview

```
React (FleetSecurityPage)
   │
   │   REST + WebSocket
   ▼
Go API handlers (handlers_fleetsecurity.go)
   │
   ▼
fleetsec.Service  ─── orchestrates ack-tracking, retries, audit
   │     │
   │     └─── audit.Log()  →  audit_log table
   │
   ▼
serial.Manager  +  meshtastic.Dispatcher
   │
   ▼   (encode admin protobufs, send ToRadio frames; receive Routing acks
   │    and FromRadio.config payloads)
   ▼
Heltec (USB) ─── LoRa PKC envelope ──→ deployed Halberd Heltecs
```

Three new backend layers, one new frontend page, four new database tables (or migrations onto existing tables), no changes to the Heltec/Halberd firmware itself.

---

## 3. Backend changes

### 3.1 Protobuf bindings — vendor and generate

The existing hand-coded builders in `internal/serial/encode.go` (TextMessage, Position, Telemetry, the small set of admin commands) stay as-is — they work and don't need churn. The Fleet Security surface (`AdminMessage` ~100-field union, nested `Channel`/`ChannelSettings`, `SecurityConfig` with repeated `admin_key`) is too tedious and error-prone to hand-code. We vendor the canonical Meshtastic protos and generate Go bindings.

**Pin**: `github.com/meshtastic/protobufs` at tag **`v2.7.23`** (full commit SHA `97ea65a10d31f24d84c8510342f2cd2d213c35a5`, 2026-04-22). This is the latest tagged release as of plan time and predates the in-flight `serial_hal` work we don't need. There is no official Meshtastic Go SDK (`github.com/meshtastic/go` 404s), and the third-party options (`lmatte7/goMesh`, `meshnet-gophers/meshtastic-go`) either aren't aimed at AdminMessage as a first-class concern or are stale — generating ourselves is the right call.

**No submodule, no vendor directory.** `make proto` downloads the source tarball at the pinned SHA into a temp dir, runs `protoc` against it, and cleans up. Generated `*.pb.go` files in `internal/meshpb/` are checked in, so routine builds and CI need neither protoc nor network access — only someone bumping the pin (or running `make proto-check` in CI) does. The pinned SHA lives as a single variable in the Makefile; bumping is "edit one line, run `make proto`, commit the diff."

**Generated package**: `internal/meshpb/` — the only place generated `.pb.go` files live. Generate from a curated subset of the proto tree (others pull in TAK / serial_hal / paxcounter that we don't need):

```
admin.proto        channel.proto       config.proto
mesh.proto         portnums.proto      module_config.proto   (transitive — required by Config)
telemetry.proto    deviceonly.proto    (transitive — required by Mesh)
```

**Generation toolchain**:
- `make proto-tools` installs the `protoc-gen-go` plugin (one-time per dev environment).
- `make proto` curls the tarball at `PROTO_SHA`, extracts to `mktemp -d`, runs `protoc --go_out=internal/meshpb --go_opt=paths=source_relative -I <tmpdir> ...` for each listed file, then removes the tmp dir.
- Generated files are checked in so routine builds and CI don't need protoc or network access.
- CI runs `make proto-check` (regenerate + assert clean working tree) on PRs that touch `internal/meshpb/` or the Makefile pin.

**Confirmed field numbers** (cross-checked against `meshtastic/protobufs@97ea65a` so the generated bindings produce the right wire format — kept here as a reviewer-aid only; the generated code is the source of truth):

| Message | Field | Number | Source |
|---|---|---|---|
| `MeshPacket.public_key` | bytes | 16 | mesh.proto:1742 |
| `MeshPacket.pki_encrypted` | bool | 17 | mesh.proto:1747 |
| `AdminMessage.get_channel_request` | uint32 | 1 | admin.proto:231 |
| `AdminMessage.get_channel_response` | Channel | 2 | admin.proto:236 |
| `AdminMessage.get_config_request` | ConfigType | 5 | admin.proto:251 |
| `AdminMessage.get_config_response` | Config | 6 | admin.proto:257 |
| `AdminMessage.set_channel` | Channel | 33 | admin.proto:372 |
| `AdminMessage.set_config` | Config | 34 | admin.proto:377 |
| `AdminMessage.begin_edit_settings` | bool | 64 | admin.proto:459 |
| `AdminMessage.commit_edit_settings` | bool | 65 | admin.proto:464 |
| `AdminMessage.session_passkey` | bytes | 101 | admin.proto:30 |
| `Config.security` | SecurityConfig | 8 | config.proto:1290 |
| `SecurityConfig.public_key` | bytes | 1 | config.proto:1238 |
| `SecurityConfig.private_key` | bytes | 2 | config.proto:1244 |
| `SecurityConfig.admin_key` | repeated bytes | 3 | config.proto:1249 |
| `SecurityConfig.is_managed` | bool | 4 | config.proto:1255 |
| `SecurityConfig.admin_channel_enabled` | bool | 8 | config.proto:1271 |
| `Channel.index` | int32 | 1 | channel.proto:146 |
| `Channel.settings` | ChannelSettings | 2 | channel.proto:151 |
| `Channel.role` | Role enum | 3 | channel.proto:156 |
| `ChannelSettings.psk` | bytes | 2 | channel.proto:47 |
| `ChannelSettings.name` | string | 3 | channel.proto:59 |

**Thin builders in `internal/serial/encode.go`** — the only hand-coded surface left, just the outer envelope:

```go
// BuildAdminPacket marshals a meshpb.AdminMessage into a ToRadio frame addressed to the local Heltec.
// Used for local admin (ESP32 → own Heltec) and operations that don't require PKC.
func BuildAdminPacket(localNodeNum uint32, msg *meshpb.AdminMessage) ([]byte, error)

// BuildAdminPacketPKC is the remote variant: sets pki_encrypted=true and want_ack=true on the
// MeshPacket so the local Heltec encrypts the admin payload to the recipient's pubkey from its
// NodeDB. The backend never touches Curve25519 itself.
func BuildAdminPacketPKC(remoteNodeNum uint32, msg *meshpb.AdminMessage) ([]byte, error)
```

Both functions: marshal `msg` with `proto.Marshal`, wrap in `meshpb.MeshPacket{Decoded: &Data{Portnum: ADMIN_APP, Payload: bytes}, ...}`, marshal that, wrap in `meshpb.ToRadio{PayloadVariant: &ToRadio_Packet{...}}`, marshal again. New runtime dependency: `google.golang.org/protobuf` (~500 KB, acceptable).

**Service-layer message construction** lives in `internal/fleetsec/` and uses generated structs directly:

```go
msg := &meshpb.AdminMessage{
    PayloadVariant: &meshpb.AdminMessage_SetChannel{
        SetChannel: &meshpb.Channel{
            Index: int32(idx),
            Role:  meshpb.Channel_PRIMARY,
            Settings: &meshpb.ChannelSettings{Psk: newPSK, Name: existingName},
        },
    },
}
frame, err := serial.BuildAdminPacketPKC(targetNodeNum, msg)
```

**Random PSK generation**: add `RandomPSK(size int) ([]byte, error)` to `internal/fleetsec/keys.go`, wraps `crypto/rand.Read`. Sizes 0/16/32 only; reject other lengths (`1` is reserved for Meshtastic's "default channel index" semantic and is not generated by us).

**X25519 keypair generation**: add `GenerateX25519Keypair() (priv, pub []byte, err error)` to `internal/fleetsec/keys.go` using `golang.org/x/crypto/curve25519`. 32 bytes each, with the standard private-key bit-clamping.

### 3.2 Dispatcher — `internal/meshtastic/dispatcher.go`

Currently `PortNumAdmin` (6) has no case in the dispatcher's port switch (the trailing `default:` swallows it). Add:

```go
case PortNumAdmin:
    // AdminMessage replies (get_channel_response, get_config_response,
    // routing acks). Decode just enough to route to fleetsec.Service.
    d.fleetsecHandler.HandleAdminReply(mp)
```

`PortNumRouting` (5) currently logs at debug only. Extend to extract `Routing.error_reason` and request_id, then forward to `fleetsec.Service` so it can correlate acks to in-flight admin transactions.

Decoding inbound admin replies is `proto.Unmarshal(payload, &meshpb.AdminMessage{})` plus a switch on `msg.GetPayloadVariant()` — no custom parser needed. Routing acks decode via `proto.Unmarshal(payload, &meshpb.Routing{})`. The dispatcher only routes; full interpretation (matching reply to in-flight transaction, applying the result) lives in `fleetsec.Service`.

### 3.3 New service package — `internal/fleetsec/`

Files:
- `service.go` — public surface (functions called by handlers).
- `keys.go` — X25519 keypair generation, base64 encoding, PSK random, fingerprint helper.
- `transactions.go` — in-flight admin transaction tracker (request_id → reply channel + timeout + retry).
- `messages.go` — constructors that build `*meshpb.AdminMessage` for each operation (set_channel, set_config/security, get_channel_request, etc.). Replaces the planned `decode.go` — generated bindings cover decode for free.
- `policy.go` — fleet-policy struct (used as a *display/diff* mechanism in permissive mode, not enforced).

Public API on `fleetsec.Service`:

```go
// Identity
GetIdentity(ctx) (Identity, error)                          // current control-center pubkey + label
RotateIdentity(ctx, label string) (Identity, error)         // mints new keypair on local Heltec; pushes to fleet
ImportIdentity(ctx, label string, priv, pub []byte) error   // BYO keypair (local-only set)
ExportPubkey(ctx) ([]byte, string, error)                   // safe — pubkey + fingerprint
ExportKeypair(ctx, ack string) ([]byte, []byte, error)      // gated — requires "EXPORT" ack string

// Trust roster
ListTrust(ctx) ([]NodeTrust, error)                         // per-node admin_key list + is_managed + last verified
GetTrust(ctx, nodeNum uint32) (NodeTrust, error)            // refreshes by sending get_config to remote
SetAdminKeys(ctx, nodeNum uint32, keys [][]byte) error      // refuses if it would lock us out (override flag)
SetIsManaged(ctx, nodeNum uint32, val bool) error           // refuses if remote unverified within 24h
VerifyTrust(ctx, nodeNum uint32) (VerifyResult, error)      // no-op get_config → ack

// Channels
ListChannels(ctx) ([]ChannelState, error)                   // per-channel name, psk fingerprint, age, coverage
RotatePSK(ctx, channelIndex uint32, newPSK []byte, targets []uint32, opts RotationOpts) (RotationID, error)
GetRotation(ctx, RotationID) (RotationStatus, error)        // live progress
RetryRotation(ctx, RotationID, nodeNums []uint32) error

// Recovery
StartRecovery(ctx, rescuePriv, rescuePub []byte, newLabel string) (RecoveryID, error)
GetRecovery(ctx, RecoveryID) (RecoveryStatus, error)

// Identity registry (label ↔ pubkey)
ListIdentities(ctx) ([]IdentityRecord, error)
RegisterIdentity(ctx, label string, pub []byte, role string) error  // role = "primary" | "rescue" | "operator"
RevokeIdentity(ctx, pub []byte, reason string) error
```

Lockout-prevention rule (implemented in `SetAdminKeys` and `SetIsManaged`):
- Refuse if the resulting `admin_key` list contains zero pubkeys known to the local identity registry.
- Refuse `SetIsManaged(true)` if `VerifyTrust` hasn't succeeded for that node in the last 24h.
- Both refusals can be overridden by passing `force=true` AND a typed confirmation string from the UI; both forced operations are logged with `severity=critical` in the audit log.

**Concurrency**: a `sync.Mutex` on `Service` serializes admin operations. Two operators trying to rotate the same channel at the same time get serialized; the second one sees the up-to-date state from the first's commit. WebSocket events broadcast progress per rotation so all sessions stay in sync.

**Ack tracking**: `transactions.go` registers `(packet_id → reply chan)` before sending, awaits `Routing` ack with a timeout (default 10s for local admin, 30s for remote PKC), and surfaces the result. Retries up to 3× per node, with exponential backoff, before marking failed.

### 3.4 REST handlers — `internal/api/handlers_fleetsecurity.go`

Mount under `/api/fleet-security` in `internal/api/server.go`. RBAC:
- Read endpoints (`GET *`): require role `OPERATOR` or higher.
- Mutating endpoints: require role `ADMIN`.
- `ExportKeypair` and `RotateIdentity`: require role `ADMIN` AND a fresh re-auth (re-validate JWT password within last 5 min; reuse pattern from `handlers_admin.go` factory-reset if it exists, otherwise add a `/auth/reauth` endpoint).

Endpoints:

```
GET    /api/fleet-security/identity                     → Identity
POST   /api/fleet-security/identity/rotate              → {label} → Identity (kicks off fleet-wide push)
POST   /api/fleet-security/identity/import              → {label, priv_b64, pub_b64} → Identity
GET    /api/fleet-security/identity/pubkey              → {pub_b64, fingerprint}
POST   /api/fleet-security/identity/export-keypair      → {ack:"EXPORT"} → {priv_b64, pub_b64} (downloaded, not stored)

GET    /api/fleet-security/identities                   → []IdentityRecord
POST   /api/fleet-security/identities                   → {label, pub_b64, role}
DELETE /api/fleet-security/identities/{pubFingerprint}  → revoke

GET    /api/fleet-security/trust                        → []NodeTrust
GET    /api/fleet-security/trust/{nodeNum}              → NodeTrust (forces a remote get_config)
PUT    /api/fleet-security/trust/{nodeNum}/admin-keys   → {keys:[fingerprint], force?:true, ack?:"LOCKOUT"}
PUT    /api/fleet-security/trust/{nodeNum}/is-managed   → {value:bool, force?:true, ack?:"LOCKDOWN"}
POST   /api/fleet-security/trust/{nodeNum}/verify       → VerifyResult

GET    /api/fleet-security/channels                     → []ChannelState
POST   /api/fleet-security/channels/{idx}/rotate        → {source:"random"|"explicit", psk_b64?, targets?:[nodeNum], ack:"ROTATE"}
                                                         → {rotation_id}
GET    /api/fleet-security/rotations/{id}               → RotationStatus
POST   /api/fleet-security/rotations/{id}/retry         → {targets:[nodeNum]}

POST   /api/fleet-security/recovery                     → {rescue_priv_b64, rescue_pub_b64, new_label, ack:"RECOVER"}
                                                         → {recovery_id}
GET    /api/fleet-security/recovery/{id}                → RecoveryStatus

GET    /api/fleet-security/policy                       → FleetPolicy (read-only display object)
PUT    /api/fleet-security/policy                       → FleetPolicy (saves expected admin_keys / is_managed for drift detection only)
```

WebSocket: extend the existing hub with a `fleet-security` topic; broadcast `rotation.progress`, `trust.updated`, `identity.changed` events. Reuse the broadcasting pattern from chat / nodes.

### 3.5 Database migrations

New migrations (next free numbers, currently 24+):

**`000024_fleet_security.up.sql`**

```sql
-- Named identities (control-center primary, rescue, operator pubkeys, etc.)
CREATE TABLE fleet_identities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    label           TEXT NOT NULL UNIQUE,
    public_key      BYTEA NOT NULL UNIQUE,
    fingerprint     TEXT NOT NULL UNIQUE,        -- 8-byte hex of SHA-256(pub)
    role            TEXT NOT NULL,               -- 'primary' | 'rescue' | 'operator' | 'revoked'
    source          TEXT NOT NULL,               -- 'auto-generated' | 'imported' | 'rotated'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    notes           TEXT
);

-- Per-node trust state (snapshot of last verify)
CREATE TABLE fleet_node_trust (
    node_num            BIGINT PRIMARY KEY REFERENCES nodes(node_num) ON DELETE CASCADE,
    admin_key_fps       JSONB NOT NULL DEFAULT '[]'::jsonb,  -- list of fingerprint hex
    is_managed          BOOLEAN NOT NULL DEFAULT false,
    last_verified_at    TIMESTAMPTZ,
    last_verify_method  TEXT,                                 -- 'local-usb' | 'remote-pkc'
    last_drift_check_at TIMESTAMPTZ,
    drift_status        TEXT NOT NULL DEFAULT 'unknown',      -- 'in-policy' | 'drift' | 'unreachable' | 'unknown'
    notes               TEXT
);

-- Channel state snapshot (one row per channel index in the fleet)
CREATE TABLE fleet_channels (
    channel_index       INT PRIMARY KEY,
    name                TEXT NOT NULL,
    role                TEXT NOT NULL,                        -- 'PRIMARY' | 'SECONDARY' | 'DISABLED'
    psk_fingerprint     TEXT,                                 -- 8-byte hex of PSK (NEVER the PSK itself)
    psk_length          INT,                                  -- 0/1/16/32
    last_rotated_at     TIMESTAMPTZ,
    last_rotated_by     UUID REFERENCES users(id),
    last_rotation_id    UUID
);

-- In-flight / completed rotation transactions (audit + retry surface)
CREATE TABLE fleet_rotations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            TEXT NOT NULL,                            -- 'psk' | 'identity' | 'admin-keys' | 'recovery'
    channel_index   INT,                                      -- nullable; only meaningful for kind='psk'
    started_by      UUID REFERENCES users(id),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    targets         JSONB NOT NULL,                           -- [{node_num, status, attempts, last_error}]
    new_psk_fp      TEXT,                                     -- redacted fingerprint, never the PSK
    notes           TEXT
);

-- Operator-defined fleet policy (display-only in permissive mode)
CREATE TABLE fleet_policy (
    id                      INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- singleton
    expected_admin_key_fps  JSONB NOT NULL DEFAULT '[]'::jsonb,
    expected_is_managed     BOOLEAN NOT NULL DEFAULT false,
    expected_channels       JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by              UUID REFERENCES users(id)
);

INSERT INTO fleet_policy (id) VALUES (1) ON CONFLICT DO NOTHING;
```

**Important security note for migrations**: never store raw PSKs or private keys in the database. Only fingerprints (truncated SHA-256) are kept. The control-center private key is stored in the local Heltec's NVS where it already lives — the backend only reads/writes via admin protobuf, never persists privkey bytes. The one exception is the brief moment during BYO import / recovery when privkey bytes pass through the request body to be pushed to the Heltec; they MUST NOT be logged, MUST NOT be persisted, and the request handler must zero the buffer before returning.

`000024_fleet_security.down.sql` drops the four tables in reverse order.

---

## 4. Frontend changes

### 4.1 New page — `web/src/pages/FleetSecurityPage.tsx`

Mirror the `ConfigPage.tsx` patterns: TanStack `useQuery` + `useMutation`, Zustand auth-store gating, query cache invalidation on mutate.

Three top-level sections rendered as stacked cards (not separate sub-routes — keeps one page scrollable, matching ConfigPage's collapsible-section style):

1. **Identity card** — top.
   - Display: pubkey (truncated with copy button), fingerprint, label, source, age.
   - Actions: `Rotate identity…`, `Export pubkey`, `Import keypair…`, `Manage identity registry…`.
   - Modal: `IdentityRotationModal` — collects new label, optional notes; preview shows: "Push new pubkey to N nodes' admin_key list; remove old pubkey after verify."
   - Modal: `IdentityImportModal` — paste-in or file-upload of keypair; client-side validation for length (32 bytes raw, 44 chars base64).
   - Modal: `IdentityRegistryModal` — list/add/revoke named pubkeys.

2. **Trust roster card** — middle.
   - Table of all nodes from `nodes` left-joined to `fleet_node_trust`. Columns: short name, last-seen, admin-key labels (chips, named via registry), is_managed status, trust-health pill, actions.
   - Trust-health pill colors: green (verified ≤ 1h), yellow (verified within 24h), orange (verified > 24h), red (unreachable).
   - Bulk-select with checkboxes; bulk actions: `Add identity to admin_key`, `Remove identity from admin_key`, `Set is_managed`, `Verify`.
   - Per-row actions: `Verify now`, `Edit admin_key`, `Toggle is_managed`.
   - Lockout warning banner if any selected operation would result in zero known pubkeys remaining on a node.
   - Modal: `EditAdminKeysModal` — multi-select from identity registry, shows diff before commit, requires typed `LOCKOUT` if forcing through a lockout-warning.

3. **Channels card** — bottom.
   - One panel per channel index from `fleet_channels`. Shows name, role, PSK fingerprint, age, coverage (`X / Y nodes on current PSK`).
   - Action: `Rotate PSK…` opens `RotatePSKModal`.
   - Modal includes: source (random | explicit base64 input), target selector (default = all healthy), estimated time/airtime, lockout-strand warnings, typed `ROTATE` confirmation.
   - Live progress: opens a `RotationProgressDrawer` that subscribes to the WebSocket `rotation.progress` topic; per-target status pills update as acks arrive. Failed targets land in a "Retry" tray.

4. **Recovery panel** — collapsed by default at the bottom.
   - Big red `Recover from compromise…` button.
   - Modal: `RecoveryWizard` walks through:
     1. Confirm scenario (compromise vs. lost-but-not-compromised).
     2. Plug in a fresh control-center Heltec; backend detects new device via the existing serial-detection plumbing.
     3. Paste rescue privkey (file upload preferred); backend pushes via `BuildAdminSetSecurityConfig` to the local Heltec only.
     4. Mint new primary keypair; backend pushes new `admin_key` list `[primary_new, rescue]` to every node and waits for ack.
     5. Result page: list of nodes that ack'd, list that didn't (with last-seen and which need physical recovery).

### 4.2 Sidebar nav — `web/src/components/Sidebar.tsx`

Add one entry near `/config`:

```ts
{ path: '/fleet-security', label: 'Fleet', icon: '<svg-d-for-shield-icon>' }
```

Use a shield/lock icon (suggest the heroicons "shield-check" path). Place between `/users` and `/config` so the security-adjacent items cluster.

### 4.3 Route wiring — `web/src/App.tsx`

Add `<Route path="/fleet-security" element={<FleetSecurityPage />} />` alongside the existing config route. Lazy-load if the existing pages are lazy-loaded; otherwise import directly.

### 4.4 Shared components

- `<PubkeyChip>` — renders `label · ab:cd:ef:12` with hover-expand to full base64 and copy-to-clipboard. Used in both Trust roster and Identity card.
- `<TypedConfirm>` — input that requires the user to type a specific phrase (`ROTATE`, `LOCKOUT`, `RECOVER`, `EXPORT`) before its parent submit button enables. Used in every destructive modal.
- `<TrustHealthPill>` — colored chip based on `last_verified_at` and `drift_status`.

### 4.5 API client

Add `web/src/api/fleetSecurity.ts` exposing typed wrappers around all endpoints; follow the shape of any existing `web/src/api/*.ts` (e.g., `web/src/api/config.ts` if present).

---

## 5. Permissions and audit

### 5.1 Permissions — `internal/permissions/`

Add new permission keys:
- `fleet_security:read`
- `fleet_security:rotate_psk`
- `fleet_security:edit_trust`
- `fleet_security:rotate_identity`
- `fleet_security:export_keypair`
- `fleet_security:recovery`
- `fleet_security:policy_edit`

Default role mapping:
- `VIEWER` → none (cannot see Fleet Security tab; sidebar item is hidden).
- `ANALYST` → `read`.
- `OPERATOR` → `read`, `rotate_psk`, `edit_trust`.
- `ADMIN` → all.

### 5.2 Audit log — reuse `audit_log` table

Every mutating operation calls `audit.Log()` with:
- `user_id` (from JWT context)
- `action` (one of: `fleetsec.rotate_identity`, `fleetsec.rotate_psk`, `fleetsec.set_admin_keys`, `fleetsec.set_is_managed`, `fleetsec.verify_trust`, `fleetsec.import_identity`, `fleetsec.export_keypair`, `fleetsec.recovery_start`, `fleetsec.recovery_complete`, `fleetsec.policy_update`)
- `resource` = `node` / `channel` / `identity` / `policy`
- `resource_id` = nodeNum / channelIndex / fingerprint / `policy`
- `details` (JSONB) = before/after diff, but with all PSKs and privkeys redacted to fingerprint-only
- `severity` = `critical` for any forced (lockout-override) operation or recovery, `info` otherwise

The `severity` column is not currently in `audit_log`; check schema and add it via migration `000025_audit_severity.up.sql` if absent. (Defer if it would cause cross-cutting churn — alternative is to encode severity in the `details` JSONB field.)

---

## 6. Workflows in detail

### 6.1 Initial provisioning (one-time per Halberd)

1. Operator plugs Halberd USB into diginode-cc host (or any host with `meshtastic` CLI access).
2. In Fleet Security → Trust roster, the new node appears with `last_verified_at = null` and `admin_key_fps = []`.
3. Operator clicks `Edit admin_keys`, selects `[primary, rescue]` from the registry, clicks Save.
4. Backend sends `BuildAdminSetSecurityConfig(localHeltecOfNewNode, ..., adminKeys=[primary, rescue], ...)` over **local USB** (since the Halberd's Heltec is the one connected — this requires the operator to switch the diginode-cc backend's serial port to the new device, OR run a small companion CLI on the operator's laptop. For now, document the manual `meshtastic --port /dev/ttyACM0 --set ...` path as the supported provisioning channel; the in-app provisioning flow is a follow-up.)
5. Once the Halberd is in the field, the control-center Heltec hears its NodeInfo, learns its pubkey, and `GetTrust` over PKC starts working.

### 6.2 Routine PSK rotation

1. Operator opens Fleet Security → Channels → Channel 0 → `Rotate PSK…`.
2. Modal shows: PSK age, coverage (47/47 on current), estimated time (~6 min), airtime budget.
3. Operator picks `Random 16 bytes`, leaves targets at default (all healthy), types `ROTATE`, clicks confirm.
4. Backend:
   - Allocates `RotationID`.
   - For each target, sequentially: `BuildAdminGetChannel` → patch PSK → `BuildAdminSetChannel` → await `Routing` ack with `want_ack=true`. Retry up to 3×.
   - Broadcasts `rotation.progress` events on the WebSocket as each target acks.
   - **Last**, rotates the local Heltec's own copy.
5. UI: progress drawer shows live status. Failed targets retain old PSK; appear in a retry tray.
6. Audit log: one entry per target ack/fail, plus a final summary.

### 6.3 Identity rotation (planned, not under attack)

1. Operator opens Fleet Security → Identity → `Rotate identity…`.
2. Modal: enter new label, confirm.
3. Backend mints new keypair locally, calls `BuildAdminSetSecurityConfig(local, newPriv, newPub, adminKeys+[old, new], ...)` to install both keys on the local Heltec (so it can sign with new while old is still trusted by remotes).
4. Backend pushes `BuildAdminSetSecurityConfig(remote, _, _, adminKeys=adminKeys+new, ...)` to every node that previously trusted `old` — adding `new`, keeping `old`.
5. Once all acks: backend pushes a second pass that **removes** `old` from every node's admin_key.
6. Once all acks: backend rewrites local Heltec's admin_key (and security config) to drop `old`.
7. Identity registry: marks `old` as `revoked` with reason `rotated`.

### 6.4 Recovery from compromise (no remote access lost)

1. Operator suspects `primary` privkey leaked. Opens Fleet Security → `Recover from compromise…`.
2. Wizard step 1: select scenario `compromised`.
3. Wizard step 2: plug in fresh Heltec or use existing one; paste/upload `rescue` privkey from cold storage. Backend installs it on the local Heltec via `BuildAdminSetSecurityConfig` (local USB).
4. Wizard step 3: backend mints new primary keypair on the local Heltec.
5. Wizard step 4: for every node, push `BuildAdminSetSecurityConfig(remote, _, _, adminKeys=[rescue, primary_new], ...)` — fully replacing the old admin_key list. Signed with `rescue` privkey by the local Heltec (since `rescue` is now the active identity until step 6).
6. Wizard step 5: switch local Heltec back to `primary_new` as its active identity (re-install via local USB), keeping `rescue` resident on the disk-stored backup, NOT on the live Heltec.
7. Result: any node that didn't ack is listed with last-seen and recovery instructions (USB factory reset + re-provision). Audit log entry has `severity=critical`.

### 6.5 Adding a second operator

1. New operator generates X25519 keypair offline (CLI tool documented in README), shares pubkey only.
2. Existing admin opens Fleet Security → Identity → `Manage identity registry…` → `Add identity` with role `operator`.
3. Goes to Trust roster, selects all nodes, bulk action `Add identity to admin_key`, picks the new operator chip, types confirmation, executes.
4. Backend rolls through nodes pushing updated admin_key lists with the new pubkey appended (max 3 slots — UI must check capacity and reject if full).

---

## 7. Cryptography and key handling

- **Curve25519 (X25519)** for keypairs. Use `golang.org/x/crypto/curve25519`. Private key is 32 bytes (with bit-clamping handled by the library). Public key is 32 bytes.
- **Fingerprints** are first 8 bytes of `SHA-256(pubkey)`, hex-encoded with colon separators (`ab:cd:ef:12:34:56:78:9a`). Used everywhere the UI renders a key for human consumption. Never store nor display raw pubkeys without the fingerprint alongside.
- **PSKs**: Meshtastic accepts 0/1/16/32 bytes. UI default is 16. PSKs are pushed as raw bytes inside `Channel.settings.psk`, never persisted server-side, only their fingerprint (first 8 bytes of SHA-256, same convention as pubkey fingerprint) lands in `fleet_channels.psk_fingerprint`.
- **Privkey handling rule**: privkeys live exactly two places — the Heltec NVS (managed by Meshtastic firmware) and operator-controlled cold storage (encrypted file outside the diginode-cc DB). The Go process holds privkey bytes only in the request scope of `ImportIdentity` / `RecoveryStart` and zeros the buffer before returning. No logging of privkey bytes. Express this as a code-level convention (helper `crypto.SecretBytes` with `Clear()`) rather than relying on developer discipline.
- **Side-channel-free comparison**: when checking whether a pubkey is in the admin_key list, use `crypto/subtle.ConstantTimeCompare`. Probably overkill in this threat model but cheap.
- **PKC envelope encryption** is done by the local Heltec, not by diginode-cc. Setting `pki_encrypted=true` on the outbound `MeshPacket` is sufficient — the local Heltec performs ECDH+AES-CCM using its private key (in NVS) and the recipient's public key (from its NodeDB).
- **NodeDB freshness**: a remote admin to node X requires the local Heltec to know X's pubkey. The backend should call `BuildNodeInfoRequest(nodeNum)` (already exists in `encode.go:330`) before each remote admin and wait briefly for the NodeInfo reply if the local NodeDB doesn't already have a cached pubkey for X.

---

## 8. Critical files to modify or create

**Backend (Go) — proto bindings**:
- `internal/meshpb/` — generated `*.pb.go` files (admin, channel, config, mesh, portnums, module_config, telemetry, deviceonly, plus transitive deps), checked in
- `Makefile` — `proto`, `proto-tools`, `proto-check` targets (already added). `PROTO_SHA` variable holds the pinned commit.
- `go.mod` — add `google.golang.org/protobuf`
- No submodule, no vendor directory, no `tools/tools.go` — `protoc-gen-go` is installed via `make proto-tools` (one-time per dev environment).

**Backend (Go) — feature code**:
- `internal/serial/encode.go` — extend (existing file; add `BuildAdminPacket` + `BuildAdminPacketPKC` envelope helpers, ~80 lines)
- `internal/meshtastic/dispatcher.go` — add `case PortNumAdmin:` (proto.Unmarshal into `meshpb.AdminMessage`) and extend `PortNumRouting` ack capture
- `internal/fleetsec/service.go` — new (~600 lines)
- `internal/fleetsec/keys.go` — new (~100 lines)
- `internal/fleetsec/transactions.go` — new (~200 lines)
- `internal/fleetsec/messages.go` — new (~150 lines; AdminMessage constructors using generated structs)
- `internal/fleetsec/policy.go` — new (~80 lines)
- `internal/api/handlers_fleetsecurity.go` — new (~500 lines)
- `internal/api/server.go` — register new route group
- `internal/permissions/permissions.go` — add new permission keys
- `internal/database/migrations/000024_fleet_security.up.sql` + `.down.sql` — new
- (optional) `internal/database/migrations/000025_audit_severity.up.sql` + `.down.sql` — only if `audit_log` lacks `severity`
- `cmd/diginode-cc/main.go` — wire `fleetsec.Service` into startup; wire its dispatcher hook

**Frontend (React/TS)**:
- `web/src/pages/FleetSecurityPage.tsx` — new (~600 lines)
- `web/src/pages/fleet/IdentityCard.tsx` — new
- `web/src/pages/fleet/TrustRoster.tsx` — new
- `web/src/pages/fleet/ChannelsCard.tsx` — new
- `web/src/pages/fleet/RotationProgressDrawer.tsx` — new
- `web/src/pages/fleet/RecoveryWizard.tsx` — new
- `web/src/pages/fleet/modals/*.tsx` — new (one per modal, ~5 files)
- `web/src/components/PubkeyChip.tsx` — new
- `web/src/components/TypedConfirm.tsx` — new
- `web/src/components/TrustHealthPill.tsx` — new
- `web/src/api/fleetSecurity.ts` — new
- `web/src/components/Sidebar.tsx` — add nav item
- `web/src/App.tsx` — add route

**Reference patterns to follow** (existing files, do not modify):
- `internal/serial/encode.go` lines 184–293 — the existing hand-coded AdminMessage builder pattern (kept untouched; the new envelope helpers sit alongside).
- `web/src/pages/ConfigPage.tsx` — TanStack Query / mutation / Zustand RBAC pattern.
- `internal/api/handlers_admin.go` — pattern for destructive endpoints with audit logging.
- `internal/audit/` — audit-log helper API.

---

## 9. Risks and things to defer

**Defer to follow-up PRs:**
- **In-app provisioning flow** (switching diginode-cc's serial port to a freshly-attached Halberd to push initial admin_key). For v1, document the manual `meshtastic` CLI provisioning path; the UI surface only manages already-provisioned units.
- **Fleet policy reconciliation** (the policy table exists but is display-only in this plan; future PR adds a "Reconcile drift" button that generates the minimum set of admin transactions to bring drifted nodes into compliance).
- **Multi-channel rotation** in a single transaction (current plan rotates one channel at a time; users wanting to rotate channels 0+1+2 simultaneously do three sequential rotations).
- **Hardware-backed key escrow** (HSM, Yubikey-stored privkey). Out of scope.

**Risks to call out during review:**
- **Lockout if recovery wizard fails mid-way**: between "rescue installed locally" and "primary_new pushed to remotes", the local Heltec's identity is `rescue`. If the process crashes or the operator unplugs, the rescue privkey is now persisted on the live Heltec — defeating the cold-storage assumption. Mitigation: the wizard explicitly warns of this, and the final step zeroizes `rescue` from the live Heltec by re-installing `primary_new` and overwriting. Document a "what to do if the wizard crashes mid-flight" runbook.
- **Proto pin drift**: pinned at `97ea65a10d31f24d84c8510342f2cd2d213c35a5` (v2.7.23) via the `PROTO_SHA` Makefile variable. When bumping, edit `PROTO_SHA`, run `make proto`, review the diff in `internal/meshpb/` for any field renames or removals on `AdminMessage` / `SecurityConfig` / `Channel`, then commit. CI's `make proto-check` catches accidental drift but not intentional bumps — those need a human review of the upstream changelog.
- **Airtime / regulatory compliance**: rotating 50 nodes in EU868 will hit duty-cycle limits. The rotation engine must respect a minimum inter-target gap (configurable, default 5s) and a maximum airtime budget per minute. Surface as a setting in fleet policy.
- **Concurrent admin from multiple control centers** (if any deployment runs more than one). The `admin_key` list permits up to 3 keys; if two control centers each rotate identity at the same time, they could interleave updates. Out of scope for v1; document that only one control center should drive admin at a time.
- **Heltec firmware compatibility**: the proto field numbers used here are stable in Meshtastic 2.5+. Older firmwares may reject. Add a runtime check on `MyInfo.firmware_version` before enabling PKC features.

---

## 10. Verification

End-to-end testing requires at least three Heltecs:
1. **Control-center Heltec** (USB-attached to a diginode-cc dev host).
2. **Halberd Heltec A** (in-range, will be the rotation target).
3. **Halberd Heltec B** (in-range, will be the second rotation target — proves multi-target).

Test plan:

**a. Backend unit tests** (`go test ./internal/fleetsec/...`):
- `keys_test.go`: keypair generation produces 32-byte priv/pub, fingerprint stable across calls, base64 round-trips.
- `transactions_test.go`: ack tracker times out cleanly, retries respect backoff, reply correlation works.
- `messages_test.go`: round-trip a constructed `meshpb.AdminMessage` through `proto.Marshal` → `proto.Unmarshal` and assert the variant + nested fields survive; verify the wire-level field numbers match the pinned proto by encoding to a known-good byte string.
- `service_test.go`: lockout-prevention rejects an admin_key list that omits all known identities; force flag bypasses with critical audit entry.

**b. Backend integration tests** (`go test ./internal/api/...`):
- Spin up an embedded postgres (or use a docker-compose dev DB), stub out `serial.Manager`, exercise each REST endpoint with role-based auth, assert audit_log entries.

**c. Manual end-to-end with three Heltecs:**
1. Provision Halberd A and B with the control-center pubkey via `meshtastic` CLI.
2. Start diginode-cc; navigate to `/fleet-security`.
3. Trust roster shows A and B; click `Verify` on each — should turn green within 30s.
4. Identity card shows the control-center's auto-generated pubkey. Use `Import keypair` with a freshly-generated BYO keypair; confirm both A and B trust update is required (banner appears).
5. Push `Rotate identity` workflow; confirm A and B's admin_key lists update via the two-pass add-then-remove flow; both turn green.
6. Click `Rotate PSK` on Channel 0 with `Random 16 bytes`. Watch progress drawer: A and B ack within ~30s each. Local Heltec rotates last. Verify new PSK age is < 1m.
7. Confirm a separate Meshtastic phone client on the new PSK can decrypt traffic; client on the old PSK cannot.
8. Disconnect Halberd B's antenna; click `Rotate PSK` again; confirm B times out, lands in failed tray, A succeeds. Reconnect B antenna; click `Retry` on the failed entry; B catches up.
9. Recovery flow: stash the current primary keypair as if it were leaked; use the rescue keypair (added to identity registry beforehand) to drive the recovery wizard end-to-end. After completion, verify the new primary works for a subsequent PSK rotation.
10. Audit log (`/audit` page if exists; otherwise `SELECT * FROM audit_log WHERE action LIKE 'fleetsec.%'`): every step from steps 4–9 has an entry with the right user, resource, and severity.

**d. Browser smoke:** open Fleet Security tab as a `VIEWER` role — page is hidden / redirected. As `OPERATOR` — read-only sections visible, mutating buttons disabled. As `ADMIN` — everything functional.

If steps a–d all pass, the feature is ready for a real deployment dry-run on a small Halberd cluster.

---

## 11. Implementation order

Sequence the work so each step is independently reviewable and the previous one is verifiable before moving on.

1. **Proto generation** (~½ day): Makefile `proto` target is in place; run `make proto-tools && make proto` to populate `internal/meshpb/`, commit. Add `make proto-check` to CI so pin bumps are caught.
2. **Envelope helpers** (~½ day): `BuildAdminPacket` + `BuildAdminPacketPKC` in `internal/serial/encode.go`, plus unit tests asserting wire bytes against canonical encodings.
3. **Dispatcher routing** (~½ day): `case PortNumAdmin:` and Routing-ack capture; stub `fleetsec.Service.HandleAdminReply` for now.
4. **Migrations + DB layer** (~1 day): `000024_fleet_security.up/down.sql`; pgx queries in `internal/fleetsec/store.go` (or extend an existing storage layer).
5. **Service core** (~3 days): `keys.go`, `transactions.go`, `messages.go`, then `service.go` for Identity + Trust APIs (no PSK rotation yet).
6. **REST handlers — Identity + Trust** (~1 day): mount routes, RBAC, audit-log wiring.
7. **Frontend skeleton + Identity + Trust cards** (~3 days): page, sidebar entry, route, `PubkeyChip`, `TypedConfirm`, `TrustHealthPill`, `IdentityCard`, `TrustRoster`. End-to-end smoke: provision two test Heltecs, run `Verify` from the UI.
8. **PSK rotation backend + frontend** (~2 days): `RotatePSK` service method, progress WebSocket events, `ChannelsCard`, `RotatePSKModal`, `RotationProgressDrawer`. End-to-end smoke against three Heltecs.
9. **Recovery wizard** (~2 days): `StartRecovery` service method, `RecoveryWizard` UI with explicit warnings, runbook for crash-mid-flight.
10. **Polish + audit-log review pages + RBAC smoke** (~1 day).

Total: ~2 weeks of focused work for v1, excluding manual three-Heltec verification.
