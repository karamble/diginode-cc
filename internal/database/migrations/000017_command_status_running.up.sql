-- Add RUNNING to command_status enum. The Go code (StatusRunning =
-- "RUNNING" in internal/commands/service.go) has been writing this value
-- to the DB on every *_ACK:STARTED since long-running scan commands
-- shipped, but the persist failed silently with "invalid input value for
-- enum command_status". The followup *_DONE summary masked the issue for
-- SCAN/BASELINE/DRONE/DEAUTH/RANDOMIZATION; PROBE_START has no DONE frame
-- upstream so RUNNING never gets overwritten and the row is permanently
-- stuck at SENT. ALTER TYPE ... ADD VALUE is idempotent under IF NOT EXISTS.
ALTER TYPE command_status ADD VALUE IF NOT EXISTS 'RUNNING';
