-- Reverse of 000024_fleet_security.up.sql

DROP TABLE IF EXISTS fleet_policy;
DROP TABLE IF EXISTS fleet_rotations;
DROP TABLE IF EXISTS fleet_channels;
DROP TABLE IF EXISTS fleet_node_trust;
DROP TABLE IF EXISTS fleet_identities;

-- Demote the unique constraint on nodes.node_num back to a plain unique
-- index, matching the pre-000024 state from 000001_initial_schema.
ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_node_num_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_node_num ON nodes(node_num);
