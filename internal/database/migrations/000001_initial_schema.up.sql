-- DigiNode CC initial schema
-- Migrated from CC PRO Prisma schema

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Enums
CREATE TYPE user_role AS ENUM ('ADMIN', 'OPERATOR', 'ANALYST', 'VIEWER');
CREATE TYPE drone_status AS ENUM ('UNKNOWN', 'FRIENDLY', 'NEUTRAL', 'HOSTILE');
CREATE TYPE alert_severity AS ENUM ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL');
CREATE TYPE command_status AS ENUM ('PENDING', 'SENT', 'ACKED', 'FAILED', 'TIMEOUT');
CREATE TYPE geofence_action AS ENUM ('ALERT', 'LOG', 'ALARM');
CREATE TYPE webhook_method AS ENUM ('POST', 'PUT', 'PATCH');
CREATE TYPE node_role AS ENUM ('CLIENT', 'ROUTER', 'REPEATER', 'TRACKER', 'SENSOR', 'TAK', 'CLIENT_MUTE', 'TAK_TRACKER', 'LOST_AND_FOUND', 'SENSOR_MANAGED');

-- Sites
CREATE TABLE sites (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    description TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    radius_m DOUBLE PRECISION DEFAULT 1000,
    is_primary BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Users
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT,
    role user_role DEFAULT 'VIEWER',
    totp_secret TEXT,
    totp_enabled BOOLEAN DEFAULT false,
    must_change_password BOOLEAN DEFAULT false,
    tos_accepted BOOLEAN DEFAULT false,
    tos_accepted_at TIMESTAMPTZ,
    last_login TIMESTAMPTZ,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- User invitations
CREATE TABLE invitations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT NOT NULL,
    role user_role DEFAULT 'VIEWER',
    token TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    invited_by UUID REFERENCES users(id),
    site_id UUID REFERENCES sites(id),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Password reset tokens
CREATE TABLE password_resets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- App configuration (singleton-ish, keyed by site)
CREATE TABLE app_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    site_id UUID REFERENCES sites(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(site_id, key)
);

-- Mesh nodes
CREATE TABLE nodes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    node_num BIGINT NOT NULL,
    node_id TEXT,
    long_name TEXT,
    short_name TEXT,
    hw_model TEXT,
    role node_role,
    firmware_version TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    altitude DOUBLE PRECISION,
    battery_level INTEGER,
    voltage DOUBLE PRECISION,
    channel_utilization DOUBLE PRECISION,
    air_util_tx DOUBLE PRECISION,
    temperature DOUBLE PRECISION,
    relative_humidity DOUBLE PRECISION,
    barometric_pressure DOUBLE PRECISION,
    snr DOUBLE PRECISION,
    rssi INTEGER,
    last_heard TIMESTAMPTZ,
    is_online BOOLEAN DEFAULT false,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_nodes_node_num ON nodes(node_num);

-- Node position history
CREATE TABLE node_positions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    altitude DOUBLE PRECISION,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_node_positions_node_id ON node_positions(node_id);
CREATE INDEX idx_node_positions_timestamp ON node_positions(timestamp);

-- Drones
CREATE TABLE drones (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    mac TEXT,
    serial_number TEXT,
    uas_id TEXT,
    operator_id TEXT,
    description TEXT,
    ua_type TEXT,
    manufacturer TEXT,
    model TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    altitude DOUBLE PRECISION,
    speed DOUBLE PRECISION,
    heading DOUBLE PRECISION,
    vertical_speed DOUBLE PRECISION,
    pilot_latitude DOUBLE PRECISION,
    pilot_longitude DOUBLE PRECISION,
    rssi INTEGER,
    status drone_status DEFAULT 'ACTIVE',
    source TEXT,
    node_id UUID REFERENCES nodes(id) ON DELETE SET NULL,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    faa_data JSONB,
    first_seen TIMESTAMPTZ DEFAULT NOW(),
    last_seen TIMESTAMPTZ DEFAULT NOW(),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_drones_mac ON drones(mac);
CREATE INDEX idx_drones_status ON drones(status);
CREATE INDEX idx_drones_last_seen ON drones(last_seen);

-- Drone detection history
CREATE TABLE drone_detections (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    drone_id UUID REFERENCES drones(id) ON DELETE SET NULL,
    mac TEXT,
    serial_number TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    altitude DOUBLE PRECISION,
    speed DOUBLE PRECISION,
    heading DOUBLE PRECISION,
    rssi INTEGER,
    source TEXT,
    raw_data JSONB,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_drone_detections_drone_id ON drone_detections(drone_id);
CREATE INDEX idx_drone_detections_timestamp ON drone_detections(timestamp);

-- WiFi device inventory
CREATE TABLE inventory_devices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    mac TEXT NOT NULL,
    manufacturer TEXT,
    device_name TEXT,
    device_type TEXT,
    rssi INTEGER,
    last_ssid TEXT,
    first_seen TIMESTAMPTZ DEFAULT NOW(),
    last_seen TIMESTAMPTZ DEFAULT NOW(),
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    is_known BOOLEAN DEFAULT false,
    notes TEXT
);

CREATE UNIQUE INDEX idx_inventory_mac ON inventory_devices(mac);

-- Targets (tracked entities)
CREATE TABLE targets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    description TEXT,
    target_type TEXT,
    mac TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    status TEXT DEFAULT 'active',
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Target position history (for triangulation)
CREATE TABLE target_positions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    target_id UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    accuracy_m DOUBLE PRECISION,
    source TEXT,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_target_positions_target_id ON target_positions(target_id);

-- Geofences
CREATE TABLE geofences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    description TEXT,
    polygon JSONB NOT NULL, -- GeoJSON polygon coordinates
    action geofence_action DEFAULT 'ALERT',
    enabled BOOLEAN DEFAULT true,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Alert rules
CREATE TABLE alert_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    description TEXT,
    condition JSONB NOT NULL, -- Rule condition definition
    severity alert_severity DEFAULT 'MEDIUM',
    enabled BOOLEAN DEFAULT true,
    cooldown_seconds INTEGER DEFAULT 300,
    last_triggered TIMESTAMPTZ,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Alert events (log of triggered alerts)
CREATE TABLE alert_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    rule_id UUID REFERENCES alert_rules(id) ON DELETE SET NULL,
    severity alert_severity DEFAULT 'MEDIUM',
    title TEXT NOT NULL,
    message TEXT,
    data JSONB,
    acknowledged BOOLEAN DEFAULT false,
    acknowledged_by UUID REFERENCES users(id),
    acknowledged_at TIMESTAMPTZ,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_alert_events_created_at ON alert_events(created_at);

-- Webhooks
CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    method webhook_method DEFAULT 'POST',
    headers JSONB DEFAULT '{}',
    secret TEXT, -- HMAC signing secret
    events TEXT[] DEFAULT '{}', -- Event types to trigger on
    enabled BOOLEAN DEFAULT true,
    last_triggered TIMESTAMPTZ,
    last_status INTEGER,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Command queue
CREATE TABLE commands (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    target_node BIGINT NOT NULL,
    command_type TEXT NOT NULL,
    payload JSONB,
    status command_status DEFAULT 'PENDING',
    sent_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    result JSONB,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_commands_status ON commands(status);

-- Chat messages
CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    from_node BIGINT,
    to_node BIGINT, -- NULL = broadcast
    channel INTEGER DEFAULT 0,
    message TEXT NOT NULL,
    is_emoji BOOLEAN DEFAULT false,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_chat_messages_timestamp ON chat_messages(timestamp);

-- FAA aircraft registry cache
CREATE TABLE faa_registry (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    serial_number TEXT UNIQUE,
    registration TEXT,
    manufacturer TEXT,
    model TEXT,
    registrant_name TEXT,
    registrant_city TEXT,
    registrant_state TEXT,
    data JSONB,
    imported_at TIMESTAMPTZ DEFAULT NOW()
);

-- Firewall rules
CREATE TABLE firewall_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    rule_type TEXT NOT NULL, -- 'ip', 'country', 'cidr'
    value TEXT NOT NULL,
    action TEXT DEFAULT 'block',
    reason TEXT,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Alarm configurations
CREATE TABLE alarm_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    alarm_type TEXT NOT NULL, -- 'audio', 'visual', 'both'
    sound_file TEXT,
    trigger_events TEXT[] DEFAULT '{}',
    enabled BOOLEAN DEFAULT true,
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Audit log
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource TEXT,
    resource_id TEXT,
    details JSONB,
    ip_address TEXT,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_log_timestamp ON audit_log(timestamp);
CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
