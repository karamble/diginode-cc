DROP INDEX IF EXISTS idx_firewall_rules_expires_at;
ALTER TABLE firewall_rules DROP COLUMN IF EXISTS expires_at;
DROP INDEX IF EXISTS idx_alarm_sounds_level;
