-- BLE fingerprint targets extend the existing targets table with an
-- alternate identity shape. WiFi/MAC/OUI/SSID targets stay rows where
-- ble_short_id IS NULL; BLE rows are joined to firmware-issued hits via
-- the T-B-#### identifier in ble_short_id. The legacy mac column stays
-- usable for non-BLE rows; for BLE rows mac is typically NULL because
-- the device rotates address.
--
-- Match algebra:
--   ble_match_mode='ALL' (default) requires every present field to match
--   ble_match_mode='ANY' fires on any one present field matching
-- Empty/NULL fields are wildcards.
--
-- The compact key=value wire frame for CONFIG_TARGETS_BLE is built by the
-- C2 from these columns (see internal/targets/service.go
-- BuildConfigTargetsBLEWireFrame). Operator never sees the wire shape;
-- they pick fields via the Mark-as-target dialog on the BLE Detections page.

ALTER TABLE targets
    ADD COLUMN ble_short_id TEXT,
    ADD COLUMN ble_manufacturer_id INTEGER,
    ADD COLUMN ble_service_uuids_16 INTEGER[],
    ADD COLUMN ble_service_uuids_128 TEXT[],
    ADD COLUMN ble_local_name_glob TEXT,
    ADD COLUMN ble_appearance_min INTEGER,
    ADD COLUMN ble_appearance_max INTEGER,
    ADD COLUMN ble_tx_power_min INTEGER,
    ADD COLUMN ble_tx_power_max INTEGER,
    ADD COLUMN ble_match_mode TEXT NOT NULL DEFAULT 'ALL';

-- Unique only when set so non-BLE targets keep ble_short_id=NULL freely.
CREATE UNIQUE INDEX targets_ble_short_id_unique_idx
    ON targets (ble_short_id)
    WHERE ble_short_id IS NOT NULL;

-- Sequence to allocate the next T-B-#### identifier. Starts at 1001 to
-- leave numeric room for any future test-fixture or static targets a
-- developer might want to inject manually.
CREATE SEQUENCE IF NOT EXISTS ble_target_short_id_seq
    START WITH 1001
    INCREMENT BY 1
    NO MAXVALUE
    NO CYCLE;

-- Index for "list BLE targets only" queries from the targets page filter.
CREATE INDEX targets_ble_only_idx ON targets (ble_short_id) WHERE ble_short_id IS NOT NULL;
