-- Surface two Meshtastic NodeInfo fields the UI was previously hiding:
--   firmware_version: emitted by the local Heltec via FromRadio.metadata
--                     (only the locally-attached radio reports its firmware;
--                     remote nodes never broadcast theirs over the mesh).
--   is_licensed:      User.is_licensed bool from NodeInfo.User (field 6 in
--                     the protobuf). Marks ham-radio operators who run on
--                     amateur frequencies. Defaults to false; we infer it
--                     from the next NodeInfo each remote node sends.
--
-- firmware_version already existed on the table from the initial schema but
-- was never persisted by the application; this migration just adds the
-- new is_licensed column so the COALESCE write paths can reference it.

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS is_licensed BOOLEAN NOT NULL DEFAULT FALSE;
