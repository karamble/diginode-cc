-- Stranded recovery state on fleet_node_trust. After a rotation a node
-- may end up offline when Pi disables the old PSK fleet-wide. We keep
-- a fingerprint pointer to the PSK the node was last known to be on,
-- plus stranded markers so the dispatcher hook + periodic scan can
-- attempt recovery via fleet_recovery_psks (which holds the actual
-- PSK bytes).
--
-- previous_psk_fp pairs with a row in fleet_recovery_psks (same fp).
-- If the recovery cache evicts that fp, this column stays as a record
-- of what the node was last on but recovery becomes impossible until
-- USB intervention.
--
-- The eviction GC clears these columns 30 days after stranded_since.

ALTER TABLE fleet_node_trust ADD COLUMN previous_psk_fp     TEXT;
ALTER TABLE fleet_node_trust ADD COLUMN stranded_since      TIMESTAMPTZ;
ALTER TABLE fleet_node_trust ADD COLUMN recovery_attempts   INT NOT NULL DEFAULT 0;
ALTER TABLE fleet_node_trust ADD COLUMN last_recovery_at    TIMESTAMPTZ;
ALTER TABLE fleet_node_trust ADD COLUMN last_recovery_error TEXT;

-- Stranded-detector index: query is "managed nodes whose stranded_since
-- is set" for the periodic scan + UI listing.
CREATE INDEX idx_fleet_node_trust_stranded
    ON fleet_node_trust (stranded_since)
    WHERE stranded_since IS NOT NULL;
