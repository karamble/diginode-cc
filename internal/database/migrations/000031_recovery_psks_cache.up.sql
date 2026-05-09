-- Persistent recovery channel cache. Pi-Heltec keeps the last N
-- historical PSKs alive as SECONDARY slots (slots 2-7, FIFO eviction)
-- so a stranded node coming back online days after a rotation can be
-- detected (matching channel hash) and re-rotated onto the current
-- PSK without operator intervention.
--
-- Slot is the actual Pi-Heltec slot index (2..7). Stays in sync with
-- the radio via a startup reconcile (read Pi slots 2-7, compare to
-- this table, fix mismatches).
--
-- The raw_psk is required because the recover_stranded job needs to
-- send AdminSetChannel(stagingIdx, PRIMARY, oldPSK) to wake a node
-- that's still on this PSK. Storage caveat as fleet_node_trust
-- previous_psk_b64.

CREATE TABLE fleet_recovery_psks (
    slot         INT             PRIMARY KEY CHECK (slot BETWEEN 2 AND 7),
    fp           TEXT            NOT NULL,
    raw_psk      BYTEA           NOT NULL,
    psk_hash     SMALLINT        NOT NULL,
    added_at     TIMESTAMPTZ     NOT NULL DEFAULT now(),
    rotation_id  UUID            REFERENCES fleet_rotations(id) ON DELETE SET NULL
);

-- Each PSK fingerprint must be unique across the cache (no two slots
-- holding the same PSK; the dispatcher hook would not know which to
-- pick). On collision we evict before adding.
CREATE UNIQUE INDEX fleet_recovery_psks_fp_idx ON fleet_recovery_psks (fp);

-- Channel-hash lookup index for the dispatcher hook's O(1) match.
-- One byte XOR over (PSK || name); collisions are rare but possible.
CREATE INDEX fleet_recovery_psks_hash_idx ON fleet_recovery_psks (psk_hash);
