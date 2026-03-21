-- Fix alert severity enum to match CC PRO (INFO/NOTICE/ALERT/CRITICAL)
ALTER TYPE alert_severity RENAME VALUE 'LOW' TO 'INFO';
ALTER TYPE alert_severity RENAME VALUE 'MEDIUM' TO 'NOTICE';
ALTER TYPE alert_severity RENAME VALUE 'HIGH' TO 'ALERT';
-- CRITICAL stays the same

-- Update column defaults to use new enum values
ALTER TABLE alert_rules ALTER COLUMN severity SET DEFAULT 'NOTICE';
ALTER TABLE alert_events ALTER COLUMN severity SET DEFAULT 'NOTICE';

-- Fix command status enum to match CC PRO (add OK/ERROR, keep existing)
-- CC PRO uses: PENDING, SENT, OK, ERROR
-- DigiNode has: PENDING, SENT, ACKED, FAILED, TIMEOUT
-- Keep all values for backward compat but add OK and ERROR
ALTER TYPE command_status ADD VALUE IF NOT EXISTS 'OK';
ALTER TYPE command_status ADD VALUE IF NOT EXISTS 'ERROR';
