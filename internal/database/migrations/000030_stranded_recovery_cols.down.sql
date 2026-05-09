DROP INDEX IF EXISTS idx_fleet_node_trust_stranded;
ALTER TABLE fleet_node_trust DROP COLUMN IF EXISTS last_recovery_at;
ALTER TABLE fleet_node_trust DROP COLUMN IF EXISTS recovery_attempts;
ALTER TABLE fleet_node_trust DROP COLUMN IF EXISTS stranded_since;
ALTER TABLE fleet_node_trust DROP COLUMN IF EXISTS previous_psk_fp;
ALTER TABLE fleet_node_trust DROP COLUMN IF EXISTS previous_psk_b64;
