-- Per-node "what PSK is this node currently running?" tracking.
--
-- The retirement gate for a staged rotation checks: every managed-trust
-- row's current_psk_fp matches the new PRIMARY's fingerprint. Without
-- this column we'd have no way to confirm "every fleet member has
-- migrated" without re-verifying the whole fleet at retirement time.
--
-- Populated whenever GetTrust succeeds (PKC GetConfig SECURITY round-
-- tripping at the channel layer is proof the node is on the same PSK as
-- the local Pi-Heltec at that moment). The fingerprint stored is the
-- Pi-Heltec's PRIMARY-channel fingerprint at the time of the successful
-- verify, NOT something the remote node sends back -- the remote
-- doesn't expose its channel PSK over admin (PKC AdminGetChannel is
-- firmware-broken, see reference_meshtastic_pkc_admin_quirks.md).
--
-- NULL for any node we haven't successfully verified since the migration
-- ran. Trust roster reads NULL as "psk version unknown" and surfaces a
-- "verify required" pill.
ALTER TABLE fleet_node_trust
    ADD COLUMN current_psk_fp TEXT;
