-- Add alarm config fields to geofences
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS color TEXT DEFAULT '#1d4ed8';
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alarm_enabled BOOLEAN DEFAULT false;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alarm_level TEXT DEFAULT 'ALERT';
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS alarm_message TEXT DEFAULT '{entity} entered geofence {geofence}';
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS trigger_on_entry BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS trigger_on_exit BOOLEAN DEFAULT false;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS applies_to_adsb BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS applies_to_drones BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS applies_to_targets BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS applies_to_devices BOOLEAN DEFAULT true;
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS origin_site_id UUID REFERENCES sites(id) ON DELETE SET NULL;
