ALTER INDEX IF EXISTS idx_nodes_sensor_short_id RENAME TO idx_nodes_ah_short_id;
ALTER TABLE nodes RENAME COLUMN sensor_short_id TO ah_short_id;
