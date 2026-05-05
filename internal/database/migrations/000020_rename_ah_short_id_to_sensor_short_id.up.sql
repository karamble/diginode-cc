-- Rename ah_short_id column to sensor_short_id as part of the Halberd
-- rebrand (formerly AntiHunter). The column holds the sensor's
-- CONFIG_NODEID short identifier (HB<n> for Halberd-era sensors,
-- AH<n> for legacy AntiHunter-named units). The new name is sensor-
-- prefix-agnostic so it does not need another rename when the next
-- sensor family ships.
--
-- ALTER TABLE ... RENAME COLUMN preserves data, defaults, and the
-- NOT NULL constraint. The index needs to be renamed separately.

ALTER TABLE nodes RENAME COLUMN ah_short_id TO sensor_short_id;

ALTER INDEX IF EXISTS idx_nodes_ah_short_id RENAME TO idx_nodes_sensor_short_id;
