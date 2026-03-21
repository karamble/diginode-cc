-- Add expires_at to firewall_rules for temporary (jailed) blocks
ALTER TABLE firewall_rules ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- Index for efficient jailed IP lookups
CREATE INDEX IF NOT EXISTS idx_firewall_rules_expires_at ON firewall_rules(expires_at) WHERE expires_at IS NOT NULL;

-- Add unique constraint on alarm_sounds level for upsert support
CREATE UNIQUE INDEX IF NOT EXISTS idx_alarm_sounds_level ON alarm_sounds(level);
