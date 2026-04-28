-- Probe-request scanner SSID tracking. Pivots on SSID rather than MAC because
-- modern devices randomize MAC on every probe — the SSID is the stable identity
-- and per-(ssid, node_id) signal is what reveals location-history overlap
-- across the sensor mesh.

CREATE TABLE IF NOT EXISTS probe_ssids (
    ssid             TEXT NOT NULL,
    node_id          TEXT NOT NULL,
    first_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    hit_count        INTEGER NOT NULL DEFAULT 0,
    ghost_count      INTEGER NOT NULL DEFAULT 0,
    responded_count  INTEGER NOT NULL DEFAULT 0,
    dst_count        INTEGER NOT NULL DEFAULT 0,
    last_rssi        INTEGER,
    last_channel     INTEGER,
    last_mac         TEXT,
    PRIMARY KEY (ssid, node_id)
);

CREATE INDEX IF NOT EXISTS idx_probe_ssids_node      ON probe_ssids(node_id);
CREATE INDEX IF NOT EXISTS idx_probe_ssids_last_seen ON probe_ssids(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_probe_ssids_ghost     ON probe_ssids(ghost_count) WHERE ghost_count > 0;

-- Rolling MAC samples per (ssid, node_id) for "distinct devices probing this
-- SSID at this node in the last 24h". Pruned by Service.PruneMacSamples()
-- on a timer; no foreign key — ssid/node_id may exist here before the
-- summary row is committed in pathological cases.
CREATE TABLE IF NOT EXISTS probe_ssid_mac_samples (
    ssid       TEXT NOT NULL,
    node_id    TEXT NOT NULL,
    mac        TEXT NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (ssid, node_id, mac)
);

CREATE INDEX IF NOT EXISTS idx_probe_ssid_mac_samples_last_seen
    ON probe_ssid_mac_samples(last_seen);
