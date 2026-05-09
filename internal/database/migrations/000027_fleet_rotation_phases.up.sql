-- Two-channel staged PSK rotation -- per-rotation lifecycle tracking.
--
-- The previous rotation worker used a flat status enum (pending/in-flight/
-- acked/failed) and an implicit "local rotates last" ordering. The staged
-- rotation introduces a 5-phase lifecycle that requires explicit per-rotation
-- bookkeeping:
--
--   A. Pi-Heltec adds the new PSK as a SECONDARY channel on a chosen slot.
--   B. Each remote is taught the new PSK on the same SECONDARY slot.
--      Acks ride the still-shared PRIMARY (old PSK) -- reliable.
--   C. Each remote promotes the SECONDARY to PRIMARY. Both channels are
--      active on the remote, so acks can ride either -- still reliable.
--   D. Pi-Heltec promotes locally; both channels remain active on Pi.
--   E. Operator-paced retirement: when fleet_node_trust shows every managed
--      member's current_psk_fp matches the new PRIMARY fingerprint, the old
--      slot is DISABLED on Pi and broadcast to remotes. No automatic TTL --
--      a node offline for weeks rejoins cleanly via Retry.
--
-- staging_channel_index: which slot (1..7) holds the staging PSK during this
-- rotation. Pinned per-rotation so RetryRotation knows where the staging
-- already lives. NULL for legacy rotations created before this migration.
--
-- pi_local_phase: tracks where the Pi side is in the lifecycle. The remote
-- target phases live in the existing `targets` JSONB (a `phase` field is
-- added by the worker; the legacy `status` field is retained alongside for
-- back-compat reads from older clients/UIs).
--
-- retired_at: stamped when Phase E completes. NULL until the operator hits
-- the retirement endpoint and the gate (every node migrated) passes.
ALTER TABLE fleet_rotations
    ADD COLUMN staging_channel_index INT,
    ADD COLUMN pi_local_phase TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN retired_at TIMESTAMPTZ;

-- Backfill: every existing rotation predates the staged worker. Treat them
-- as having completed the legacy single-phase flow ("phase_d_promoted" is
-- the closest equivalent -- Pi rotated, fleet was supposed to follow).
-- Operator can run a fresh rotation if they need staged semantics.
UPDATE fleet_rotations
   SET pi_local_phase = 'phase_d_promoted'
 WHERE completed_at IS NOT NULL
   AND pi_local_phase = 'pending';

-- Constrain the phase enum at the column level so a buggy worker can't write
-- a typo and have it persist silently. Doing this AFTER the backfill so the
-- update above doesn't trip the check.
ALTER TABLE fleet_rotations
    ADD CONSTRAINT fleet_rotations_pi_local_phase_check
        CHECK (pi_local_phase IN ('pending', 'staging_added', 'phase_d_promoted', 'retired'));
