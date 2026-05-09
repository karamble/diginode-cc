-- Recreate the dead audit_log (singular) table for rollback symmetry.
-- Mirrors the schema from 000001_initial_schema.up.sql exactly so a roll
-- back to the pre-000025 state matches what 000001 would have left behind.
-- Stays empty in practice -- nothing writes to this table.
CREATE TABLE IF NOT EXISTS audit_log (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT NOT NULL,
    resource    TEXT,
    resource_id TEXT,
    details     JSONB,
    ip_address  TEXT,
    timestamp   TIMESTAMPTZ DEFAULT NOW()
);
