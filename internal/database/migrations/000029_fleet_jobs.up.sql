-- fleet_jobs: durable work queue for the rotation worker. Each job is
-- one unit of radio work (Phase A staging on Pi, Phase B atomic
-- migrate on one remote, Phase C Pi atomic, recovery on one stranded
-- node, periodic stranded-scan, drift-recovery reset on one node).
-- A single in-process worker leases jobs FIFO and runs them; the
-- stale-lease scan on startup re-leases anything left in_progress
-- by a previous container.

CREATE TABLE fleet_jobs (
    id              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    kind            TEXT            NOT NULL,
    rotation_id     UUID            REFERENCES fleet_rotations(id) ON DELETE CASCADE,
    target_node_num BIGINT,
    state           TEXT            NOT NULL DEFAULT 'queued',
    attempts        INT             NOT NULL DEFAULT 0,
    last_error      TEXT,
    payload         JSONB           NOT NULL DEFAULT '{}'::jsonb,
    enqueued_at     TIMESTAMPTZ     NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    worker_id       TEXT,
    CONSTRAINT fleet_jobs_state_check CHECK (
        state IN ('queued', 'in_progress', 'done', 'failed', 'cancelled')
    )
);

-- The lease query runs every poll tick. Index for fast SKIP LOCKED scan
-- of queued jobs in FIFO order.
CREATE INDEX idx_fleet_jobs_state_enqueued
    ON fleet_jobs (state, enqueued_at)
    WHERE state = 'queued';

-- For "is a recover_stranded job already pending for nodeNum X?" debounce
-- queries from the dispatcher hook.
CREATE INDEX idx_fleet_jobs_kind_target
    ON fleet_jobs (kind, target_node_num)
    WHERE state IN ('queued', 'in_progress');

-- For "show jobs for this rotation" UI queries.
CREATE INDEX idx_fleet_jobs_rotation
    ON fleet_jobs (rotation_id, enqueued_at DESC)
    WHERE rotation_id IS NOT NULL;

-- For "did this worker die mid-job?" startup scan.
CREATE INDEX idx_fleet_jobs_in_progress_started
    ON fleet_jobs (started_at)
    WHERE state = 'in_progress';
