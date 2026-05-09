ALTER TABLE fleet_rotations
    DROP CONSTRAINT IF EXISTS fleet_rotations_pi_local_phase_check;
ALTER TABLE fleet_rotations
    DROP COLUMN IF EXISTS staging_channel_index,
    DROP COLUMN IF EXISTS pi_local_phase,
    DROP COLUMN IF EXISTS retired_at;
