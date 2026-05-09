-- Store the raw new PSK on fleet_rotations while a rotation is in flight,
-- so the operator's Retry button doesn't have to re-supply 16 random bytes
-- they never saw. The runner clears the column once every target is acked
-- (see fleetsec.runPSKRotation -- ClearRotationPSK is the trailing call);
-- as long as one or more targets are still failed, the PSK stays so the
-- operator can resume the same rotation.
--
-- Threat model: this DB lives on the same Pi as the Heltec whose NVS
-- already holds the same PSK in cleartext. Storing it server-side here
-- doesn't widen the attack surface against a host-level adversary, while
-- it does fix the retry UX (the previous flow required the frontend to
-- carry the PSK in JS memory across page loads, which it never did).
--
-- Column is NULL by default for every pre-existing rotation row -- the
-- runner only fills it for fresh rotations. Retries against historical
-- rotations stay caller-supplied.
ALTER TABLE fleet_rotations
    ADD COLUMN new_psk BYTEA;
