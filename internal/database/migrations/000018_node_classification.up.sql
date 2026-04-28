-- Persist sensor-class metadata derived from observed mesh traffic so it
-- survives container restarts. Without these columns, every CC image
-- redeploy blanks out the antihunter / gatesensor badges in the node
-- list until each node emits another sensor frame — silent or offline
-- nodes stay tagged "gotailme" indefinitely.
--
-- node_type:   gotailme | antihunter | gatesensor (TEXT — see
--              nodes.NodeType in internal/nodes/service.go).
-- ah_short_id: AntiHunter CONFIG_NODEID prefix used to address commands
--              like "@AH64". 2-5 alphanumeric chars when known.
-- Empty string is the sentinel for "unknown" — matches the existing
-- Go zero-value semantics, no NULL juggling.

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS node_type    TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS ah_short_id  TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_nodes_node_type    ON nodes(node_type);
CREATE INDEX IF NOT EXISTS idx_nodes_ah_short_id  ON nodes(ah_short_id) WHERE ah_short_id <> '';
