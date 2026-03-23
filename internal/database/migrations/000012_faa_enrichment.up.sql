-- FAA enrichment: add Mode-S, FCC, and lookup indexes
ALTER TABLE faa_registry ADD COLUMN IF NOT EXISTS mode_s_code_hex TEXT;
ALTER TABLE faa_registry ADD COLUMN IF NOT EXISTS fcc_identifier TEXT;

CREATE INDEX IF NOT EXISTS idx_faa_registration ON faa_registry(registration);
CREATE INDEX IF NOT EXISTS idx_faa_mode_s ON faa_registry(mode_s_code_hex) WHERE mode_s_code_hex IS NOT NULL;
