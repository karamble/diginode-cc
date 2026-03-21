-- CC PRO compatibility fixes

-- Fix drone status default (was 'ACTIVE', should be 'UNKNOWN')
ALTER TABLE drones ALTER COLUMN status SET DEFAULT 'UNKNOWN';

-- Add origin_site_id to nodes
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;

-- Add origin_site_id to drones
ALTER TABLE drones ADD COLUMN IF NOT EXISTS origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;

-- Add site metadata to sites
ALTER TABLE sites ADD COLUMN IF NOT EXISTS color TEXT DEFAULT '#1d4ed8';
ALTER TABLE sites ADD COLUMN IF NOT EXISTS region TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS country TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS city TEXT;

-- Add origin_site_id to geofences
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;

-- Add origin_site_id + idempotency to commands
ALTER TABLE commands ADD COLUMN IF NOT EXISTS origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;
ALTER TABLE commands ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE commands ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Node temperature + message fields for CC PRO compat
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS temperature_c DOUBLE PRECISION;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS temperature_f DOUBLE PRECISION;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS temperature_updated_at TIMESTAMPTZ;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS last_message TEXT;

-- Security fields on users
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INTEGER DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_ip TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_country TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS anomaly_flag BOOLEAN DEFAULT false;

-- Webhook delivery history
CREATE TABLE IF NOT EXISTS webhook_deliveries (
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
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook_id ON webhook_deliveries(webhook_id);

-- Audit logs
CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource TEXT,
    resource_id TEXT,
    details JSONB,
    ip_address TEXT,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs(timestamp);
