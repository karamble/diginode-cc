-- Fleet Security: control-center identity, per-node trust roster, channel
-- PSK lifecycle, and rotation transaction history. See FLEET_SECURITY.md
-- for the full design (§3.5 covers this schema specifically).
--
-- All cryptographic material is stored as fingerprints (truncated SHA-256)
-- only. Raw PSKs and private keys NEVER hit the database -- they live in
-- the Heltec NVS (firmware-managed) and operator-controlled cold storage.
-- The exception is the request-scoped privkey bytes that pass through
-- ImportIdentity / RecoveryStart on their way to the Heltec; those are
-- zeroed before the handler returns and never persisted.

-- The existing nodes.node_num UNIQUE INDEX (idx_nodes_node_num from
-- 000001_initial_schema) needs to back a UNIQUE CONSTRAINT for use as a
-- foreign-key target. Promote it.
ALTER TABLE nodes
    ADD CONSTRAINT nodes_node_num_unique UNIQUE USING INDEX idx_nodes_node_num;

-- Identity registry: named pubkeys the operator trusts. Includes the
-- control-center's primary identity, cold-storage rescue keys, and any
-- additional operator pubkeys. role drives UX semantics (a "rescue" entry
-- is shown in the recovery wizard; an "operator" entry is offered in
-- bulk admin-key edits; "primary" marks the active control-center key).
CREATE TABLE fleet_identities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    label           TEXT NOT NULL UNIQUE,
    public_key      BYTEA NOT NULL UNIQUE,
    fingerprint     TEXT NOT NULL UNIQUE,        -- 8-byte hex of SHA-256(pub), colon-separated
    role            TEXT NOT NULL,               -- 'primary' | 'rescue' | 'operator' | 'revoked'
    source          TEXT NOT NULL,               -- 'auto-generated' | 'imported' | 'rotated'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    notes           TEXT
);
CREATE INDEX idx_fleet_identities_role ON fleet_identities(role);

-- Per-node trust state, one row per known mesh node. Snapshot of the last
-- successful get_config(SECURITY) reply from that node, plus drift status
-- relative to fleet policy. Cascades on node delete so we don't pile up
-- orphan trust rows.
CREATE TABLE fleet_node_trust (
    node_num            BIGINT PRIMARY KEY REFERENCES nodes(node_num) ON DELETE CASCADE,
    admin_key_fps       JSONB NOT NULL DEFAULT '[]'::jsonb,  -- list of fingerprint hex strings
    is_managed          BOOLEAN NOT NULL DEFAULT false,
    last_verified_at    TIMESTAMPTZ,
    last_verify_method  TEXT,                                 -- 'local-usb' | 'remote-pkc'
    last_drift_check_at TIMESTAMPTZ,
    drift_status        TEXT NOT NULL DEFAULT 'unknown',      -- 'in-policy' | 'drift' | 'unreachable' | 'unknown'
    notes               TEXT
);
CREATE INDEX idx_fleet_node_trust_drift ON fleet_node_trust(drift_status);

-- Channel state, one row per channel index in the fleet. Shows the
-- operator the current PSK age and a coverage summary (computed by the
-- service layer at read time, not stored here). Only the fingerprint
-- of the active PSK is persisted -- never the PSK bytes themselves.
CREATE TABLE fleet_channels (
    channel_index       INT PRIMARY KEY,
    name                TEXT NOT NULL,
    role                TEXT NOT NULL,                        -- 'PRIMARY' | 'SECONDARY' | 'DISABLED'
    psk_fingerprint     TEXT,                                 -- 8-byte hex of SHA-256(psk), colon-separated
    psk_length          INT,                                  -- 0 / 16 / 32 (1 reserved by Meshtastic for "default channel index")
    last_rotated_at     TIMESTAMPTZ,
    last_rotated_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    last_rotation_id    UUID
);

-- Rotation transactions: every PSK rotation, identity rotation, admin-key
-- bulk edit, or recovery flow gets one row here. targets is a JSONB array
-- of {node_num, status, attempts, last_error} so the UI can render per-
-- target status pills and a retry tray. Persisted for audit and for the
-- "incomplete rotations" coverage gauge in the channels card.
CREATE TABLE fleet_rotations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    kind            TEXT NOT NULL,                            -- 'psk' | 'identity' | 'admin-keys' | 'recovery'
    channel_index   INT,                                      -- nullable; only meaningful for kind='psk'
    started_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    targets         JSONB NOT NULL,                           -- [{node_num, status, attempts, last_error}]
    new_psk_fp      TEXT,                                     -- redacted fingerprint, never the PSK
    notes           TEXT
);
CREATE INDEX idx_fleet_rotations_started_at ON fleet_rotations(started_at DESC);
CREATE INDEX idx_fleet_rotations_kind ON fleet_rotations(kind);

-- Operator-defined fleet policy. Singleton row (CHECK enforces id=1).
-- Permissive mode treats this as a display/diff target: the trust roster
-- shows drift against expected_admin_key_fps but doesn't auto-reconcile.
-- A future PR may add a "Reconcile drift" button that generates the
-- minimum admin transaction set to bring drifted nodes into line.
CREATE TABLE fleet_policy (
    id                      INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    expected_admin_key_fps  JSONB NOT NULL DEFAULT '[]'::jsonb,
    expected_is_managed     BOOLEAN NOT NULL DEFAULT false,
    expected_channels       JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by              UUID REFERENCES users(id) ON DELETE SET NULL
);
INSERT INTO fleet_policy (id) VALUES (1) ON CONFLICT DO NOTHING;
