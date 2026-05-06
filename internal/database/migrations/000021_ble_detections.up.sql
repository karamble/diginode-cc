-- ble_detections holds the classified output of the BLE lookupper for every
-- BLERAW: wire frame received from a Halberd sensor. inventory_devices keeps
-- existing summary state (MAC, RSSI, last name); ble_detections is the rich
-- per-advertisement record that lets operators correlate AirTags, FindMy
-- beacons, surveillance OUI hits, and so on.
--
-- raw_adv stores the AD-structures payload (everything after the BLE Link
-- Layer header). Capped at 31 bytes for BLE 4.x legacy advertising; nothing
-- enforces this in the schema since BLE 5.0 extended advertising could
-- arrive once Halberd grows that capability. The classifier reparses these
-- bytes at lookup time, so the raw_adv column is also the source of truth
-- for replaying a detection with a future improved classifier.

CREATE TABLE ble_detections (
    id BIGSERIAL PRIMARY KEY,
    mac TEXT NOT NULL,
    node_id TEXT NOT NULL,
    rssi INTEGER NOT NULL,
    channel INTEGER NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- classifier output, all nullable so a row can land even when the
    -- lookupper is unreachable or returned a partial classification.
    detection_type TEXT,
    manufacturer TEXT,
    manufacturer_id INTEGER,
    local_name TEXT,
    appearance INTEGER,
    service_uuids_16 INTEGER[],
    service_uuids_128 TEXT[],
    tx_power INTEGER,
    is_random_addr BOOLEAN NOT NULL DEFAULT false,
    raw_adv BYTEA NOT NULL,
    classification JSONB,
    findmy_score INTEGER,
    combined_score REAL,

    -- site_id ties the row to the site whose CC ingested it. NULL means the
    -- ingest happened before site assignment was wired (or the site was
    -- deleted post-ingest); rows still query usefully without it.
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL
);

CREATE INDEX idx_ble_detections_mac       ON ble_detections (mac);
CREATE INDEX idx_ble_detections_node_id   ON ble_detections (node_id);
CREATE INDEX idx_ble_detections_timestamp ON ble_detections (timestamp DESC);
CREATE INDEX idx_ble_detections_type      ON ble_detections (detection_type);
