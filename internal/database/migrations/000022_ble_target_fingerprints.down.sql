DROP INDEX IF EXISTS targets_ble_only_idx;
DROP INDEX IF EXISTS targets_ble_short_id_unique_idx;
DROP SEQUENCE IF EXISTS ble_target_short_id_seq;

ALTER TABLE targets
    DROP COLUMN IF EXISTS ble_short_id,
    DROP COLUMN IF EXISTS ble_manufacturer_id,
    DROP COLUMN IF EXISTS ble_service_uuids_16,
    DROP COLUMN IF EXISTS ble_service_uuids_128,
    DROP COLUMN IF EXISTS ble_local_name_glob,
    DROP COLUMN IF EXISTS ble_appearance_min,
    DROP COLUMN IF EXISTS ble_appearance_max,
    DROP COLUMN IF EXISTS ble_tx_power_min,
    DROP COLUMN IF EXISTS ble_tx_power_max,
    DROP COLUMN IF EXISTS ble_match_mode;
