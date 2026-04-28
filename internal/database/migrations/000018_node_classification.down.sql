DROP INDEX IF EXISTS idx_nodes_ah_short_id;
DROP INDEX IF EXISTS idx_nodes_node_type;
ALTER TABLE nodes DROP COLUMN IF EXISTS ah_short_id;
ALTER TABLE nodes DROP COLUMN IF EXISTS node_type;
