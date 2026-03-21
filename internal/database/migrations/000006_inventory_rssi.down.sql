ALTER TABLE inventory_devices DROP COLUMN IF EXISTS last_longitude;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS last_latitude;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS last_node_id;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS rssi_avg;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS rssi_max;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS rssi_min;
ALTER TABLE inventory_devices DROP COLUMN IF EXISTS hits;
