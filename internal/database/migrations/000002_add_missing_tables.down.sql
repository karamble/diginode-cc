-- Reverse migration 000002

ALTER TABLE sites DROP COLUMN IF EXISTS geojson, DROP COLUMN IF EXISTS color,
    DROP COLUMN IF EXISTS city, DROP COLUMN IF EXISTS country, DROP COLUMN IF EXISTS region;

ALTER TABLE geofences DROP COLUMN IF EXISTS applies_to_devices, DROP COLUMN IF EXISTS applies_to_targets,
    DROP COLUMN IF EXISTS applies_to_drones, DROP COLUMN IF EXISTS applies_to_adsb,
    DROP COLUMN IF EXISTS trigger_on_exit, DROP COLUMN IF EXISTS trigger_on_entry,
    DROP COLUMN IF EXISTS alarm_level, DROP COLUMN IF EXISTS alarm_enabled;

DROP TYPE IF EXISTS alarm_level;

ALTER TABLE inventory_devices DROP COLUMN IF EXISTS last_longitude, DROP COLUMN IF EXISTS last_latitude,
    DROP COLUMN IF EXISTS last_node_id, DROP COLUMN IF EXISTS rssi_avg,
    DROP COLUMN IF EXISTS rssi_max, DROP COLUMN IF EXISTS rssi_min, DROP COLUMN IF EXISTS channel;

ALTER TABLE commands DROP COLUMN IF EXISTS request_ip, DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS idempotency_key;

ALTER TABLE users DROP COLUMN IF EXISTS two_factor_recovery_codes, DROP COLUMN IF EXISTS anomaly_flag,
    DROP COLUMN IF EXISTS last_login_country, DROP COLUMN IF EXISTS last_login_ip,
    DROP COLUMN IF EXISTS locked_until, DROP COLUMN IF EXISTS locked_at,
    DROP COLUMN IF EXISTS failed_login_attempts;

ALTER TABLE commands DROP COLUMN IF EXISTS origin_site_id;
ALTER TABLE drones DROP COLUMN IF EXISTS origin_site_id;
ALTER TABLE nodes DROP COLUMN IF EXISTS origin_site_id;

DROP TABLE IF EXISTS update_log CASCADE;
DROP TYPE IF EXISTS update_phase;
DROP TYPE IF EXISTS update_status;
DROP TABLE IF EXISTS webhook_deliveries CASCADE;
DROP TABLE IF EXISTS command_templates CASCADE;
DROP TABLE IF EXISTS oui_cache CASCADE;
DROP TABLE IF EXISTS tak_config CASCADE;
DROP TABLE IF EXISTS mqtt_config CASCADE;
DROP TABLE IF EXISTS alarm_sounds CASCADE;
DROP TABLE IF EXISTS visual_config CASCADE;
DROP TABLE IF EXISTS serial_config CASCADE;
DROP TABLE IF EXISTS node_coverage_overrides CASCADE;
DROP TABLE IF EXISTS coverage_config CASCADE;
DROP TABLE IF EXISTS user_site_access CASCADE;
DROP TABLE IF EXISTS user_permissions CASCADE;
DROP TABLE IF EXISTS user_preferences CASCADE;
DROP TABLE IF EXISTS triangulation_results CASCADE;
