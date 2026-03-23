DROP INDEX IF EXISTS idx_faa_mode_s;
DROP INDEX IF EXISTS idx_faa_registration;
ALTER TABLE faa_registry DROP COLUMN IF EXISTS fcc_identifier;
ALTER TABLE faa_registry DROP COLUMN IF EXISTS mode_s_code_hex;
