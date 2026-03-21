-- Additional tables from CC PRO schema analysis

-- Triangulation results (lines of bearing)
CREATE TABLE triangulation_results (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    target_id UUID REFERENCES targets(id) ON DELETE CASCADE,
    node_id UUID REFERENCES nodes(id) ON DELETE SET NULL,
    bearing DOUBLE PRECISION,
    distance_m DOUBLE PRECISION,
    rssi INTEGER,
    confidence DOUBLE PRECISION,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_triangulation_target_id ON triangulation_results(target_id);

-- User preferences
CREATE TABLE user_preferences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE UNIQUE,
    theme TEXT DEFAULT 'dark',
    density TEXT DEFAULT 'compact',
    time_format TEXT DEFAULT '24h',
    notifications_enabled BOOLEAN DEFAULT true,
    addons JSONB DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- User permissions (feature-level access control)
CREATE TABLE user_permissions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    granted BOOLEAN DEFAULT true,
    UNIQUE(user_id, feature)
);

-- User site access (multi-site RBAC)
CREATE TABLE user_site_access (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    access_level TEXT DEFAULT 'VIEW', -- VIEW, MANAGE
    UNIQUE(user_id, site_id)
);

-- Coverage configuration
CREATE TABLE coverage_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    default_radius_m DOUBLE PRECISION DEFAULT 50,
    dynamic_model BOOLEAN DEFAULT false,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Per-node coverage overrides
CREATE TABLE node_coverage_overrides (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE UNIQUE,
    radius_m DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Serial configuration (singleton)
CREATE TABLE serial_config (
    id TEXT PRIMARY KEY DEFAULT 'serial',
    device_path TEXT,
    baud INTEGER DEFAULT 115200,
    data_bits INTEGER DEFAULT 8,
    parity TEXT DEFAULT 'none',
    stop_bits INTEGER DEFAULT 1,
    delimiter TEXT DEFAULT '\n',
    reconnect_base_ms INTEGER DEFAULT 500,
    reconnect_max_ms INTEGER DEFAULT 15000,
    reconnect_jitter DOUBLE PRECISION DEFAULT 0.2,
    reconnect_max_attempts INTEGER DEFAULT 0,
    enabled BOOLEAN DEFAULT true,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Visual configuration (singleton)
CREATE TABLE visual_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pulse_frequency DOUBLE PRECISION DEFAULT 1.0,
    blink_timing DOUBLE PRECISION DEFAULT 0.5,
    stroke_width DOUBLE PRECISION DEFAULT 2.0,
    theme TEXT DEFAULT 'dark',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Alarm sounds
CREATE TABLE alarm_sounds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    level TEXT NOT NULL, -- INFO, NOTICE, ALERT, CRITICAL
    sound_file TEXT NOT NULL,
    volume DOUBLE PRECISION DEFAULT 1.0,
    alarm_config_id UUID REFERENCES alarm_configs(id) ON DELETE CASCADE
);

-- MQTT config per site
CREATE TABLE mqtt_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE UNIQUE,
    broker_url TEXT,
    username TEXT,
    password TEXT,
    client_id TEXT,
    tls_enabled BOOLEAN DEFAULT false,
    tls_cert TEXT,
    tls_key TEXT,
    tls_ca TEXT,
    qos INTEGER DEFAULT 1,
    enabled BOOLEAN DEFAULT false,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- TAK configuration
CREATE TABLE tak_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    server_url TEXT,
    protocol TEXT DEFAULT 'TCP', -- UDP, TCP, HTTPS
    tls_enabled BOOLEAN DEFAULT false,
    tls_cert TEXT,
    tls_key TEXT,
    stream_nodes BOOLEAN DEFAULT true,
    stream_targets BOOLEAN DEFAULT true,
    stream_alerts BOOLEAN DEFAULT true,
    stream_commands BOOLEAN DEFAULT false,
    enabled BOOLEAN DEFAULT false,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- OUI cache (vendor MAC prefix lookup)
CREATE TABLE oui_cache (
    prefix TEXT PRIMARY KEY, -- XX:XX:XX
    vendor TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Command templates (reusable per site)
CREATE TABLE command_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    site_id UUID REFERENCES sites(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    command_type TEXT NOT NULL,
    parameters JSONB DEFAULT '{}',
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Webhook deliveries (delivery log)
CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    webhook_id UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    status_code INTEGER,
    attempt INTEGER DEFAULT 1,
    request_payload JSONB,
    response_body TEXT,
    error TEXT,
    delivered_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_webhook_deliveries_webhook_id ON webhook_deliveries(webhook_id);

-- Update log (system update history)
CREATE TYPE update_status AS ENUM ('CHECKING', 'RUNNING', 'SUCCESS', 'FAILED', 'ROLLED_BACK');
CREATE TYPE update_phase AS ENUM (
    'PREFLIGHT', 'GIT_UPDATE', 'DEPENDENCIES', 'DATABASE',
    'BUILD', 'RESTART', 'VALIDATION', 'COMPLETE', 'FAILED', 'ROLLING_BACK'
);

CREATE TABLE update_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    status update_status DEFAULT 'CHECKING',
    phase update_phase DEFAULT 'PREFLIGHT',
    from_commit TEXT,
    to_commit TEXT,
    duration_ms INTEGER,
    error TEXT,
    started_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

-- Add origin_site_id to nodes and drones for multi-site mesh bridging
ALTER TABLE nodes ADD COLUMN origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;
ALTER TABLE drones ADD COLUMN origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;
ALTER TABLE commands ADD COLUMN origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;

-- Add more fields to users for auth security
ALTER TABLE users ADD COLUMN failed_login_attempts INTEGER DEFAULT 0;
ALTER TABLE users ADD COLUMN locked_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN locked_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN last_login_ip TEXT;
ALTER TABLE users ADD COLUMN last_login_country TEXT;
ALTER TABLE users ADD COLUMN anomaly_flag BOOLEAN DEFAULT false;
ALTER TABLE users ADD COLUMN two_factor_recovery_codes TEXT[];

-- Add idempotency key to commands
ALTER TABLE commands ADD COLUMN idempotency_key TEXT UNIQUE;
ALTER TABLE commands ADD COLUMN user_agent TEXT;
ALTER TABLE commands ADD COLUMN request_ip TEXT;

-- Add more fields to inventory devices to match CC PRO
ALTER TABLE inventory_devices ADD COLUMN channel INTEGER;
ALTER TABLE inventory_devices ADD COLUMN rssi_min INTEGER;
ALTER TABLE inventory_devices ADD COLUMN rssi_max INTEGER;
ALTER TABLE inventory_devices ADD COLUMN rssi_avg DOUBLE PRECISION;
ALTER TABLE inventory_devices ADD COLUMN last_node_id UUID REFERENCES nodes(id) ON DELETE SET NULL;
ALTER TABLE inventory_devices ADD COLUMN last_latitude DOUBLE PRECISION;
ALTER TABLE inventory_devices ADD COLUMN last_longitude DOUBLE PRECISION;

-- Add alarm level enum to match CC PRO
CREATE TYPE alarm_level AS ENUM ('INFO', 'NOTICE', 'ALERT', 'CRITICAL');

-- Add geofence improvements to match CC PRO
ALTER TABLE geofences ADD COLUMN alarm_enabled BOOLEAN DEFAULT false;
ALTER TABLE geofences ADD COLUMN alarm_level TEXT DEFAULT 'INFO';
ALTER TABLE geofences ADD COLUMN trigger_on_entry BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN trigger_on_exit BOOLEAN DEFAULT false;
ALTER TABLE geofences ADD COLUMN applies_to_adsb BOOLEAN DEFAULT false;
ALTER TABLE geofences ADD COLUMN applies_to_drones BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN applies_to_targets BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN applies_to_devices BOOLEAN DEFAULT false;

-- Add site enrichment fields
ALTER TABLE sites ADD COLUMN region TEXT;
ALTER TABLE sites ADD COLUMN country TEXT;
ALTER TABLE sites ADD COLUMN city TEXT;
ALTER TABLE sites ADD COLUMN color TEXT;
ALTER TABLE sites ADD COLUMN geojson JSONB;
