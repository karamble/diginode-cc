-- target_hits records every Target: frame the firmware emits for an
-- operator-marked target. One row per advertisement / scan match, so
-- BLE fingerprint targets accumulate hits across MAC rotations under
-- the same target_id (resolved via TID:T-B-#### at insert time).
--
-- The triangulation snapshot table target_positions stays separate —
-- it tracks T_F fixes (computed positions), not raw observations.
CREATE TABLE target_hits (
    id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    target_id       UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    target_short_id TEXT,
    observed_mac    TEXT NOT NULL,
    observed_name   TEXT,
    rssi            SMALLINT,
    latitude        DOUBLE PRECISION,
    longitude       DOUBLE PRECISION,
    node_id         TEXT,
    raw_frame       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX target_hits_target_id_created_at_idx
    ON target_hits (target_id, created_at DESC);

CREATE INDEX target_hits_target_short_id_idx
    ON target_hits (target_short_id)
    WHERE target_short_id IS NOT NULL;
