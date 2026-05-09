// Package jobs is the durable work queue for fleet-security operations.
//
// Design goals:
//   - Durable: jobs survive container restart. Stale leases re-run.
//   - Async: HTTP enqueues and returns in ms; the worker runs out of band.
//   - Sequential: single in-process worker, FIFO. The radio is
//     exclusive, so a second concurrent worker would only contend.
//   - Idempotent handlers: re-running a job is safe; atomic firmware
//     transactions overwrite whatever the slot held.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karamble/diginode-cc/internal/database"
)

// Kind enumerates the job kinds the worker can dispatch on. Adding a
// new kind requires registering a Handler with that name.
type Kind string

const (
	KindRotatePhaseA   Kind = "rotate_phase_a"   // Pi local stage SECONDARY
	KindRotatePhaseB   Kind = "rotate_phase_b"   // per-remote atomic migrate
	KindRotatePhaseC   Kind = "rotate_phase_c"   // Pi atomic promote+wipe
	KindResetNode      Kind = "reset_node"       // operator-driven drift fix
	KindRecoverStrand  Kind = "recover_stranded" // one stranded node recovery
	KindScanForStrand  Kind = "scan_for_stranded"// periodic stranded-detector
)

// State tracks a job's lifecycle.
type State string

const (
	StateQueued     State = "queued"
	StateInProgress State = "in_progress"
	StateDone       State = "done"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

// Job is the durable record. payload is the kind-specific argument
// blob, decoded by the handler.
type Job struct {
	ID            string
	Kind          Kind
	RotationID    *string
	TargetNodeNum *int64
	State         State
	Attempts      int
	LastError     string
	Payload       []byte
	EnqueuedAt    time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	WorkerID      *string
}

// ErrNoWork is returned by LeaseNext when the queue has nothing
// available right now. The Loop treats it as a normal poll-and-sleep
// signal, not a failure.
var ErrNoWork = errors.New("jobs: no work available")

// Store wraps DB access for fleet_jobs. Mirrors fleetsec.Store style.
type Store struct {
	db *database.DB
}

// NewStore constructs a Store over the shared database handle.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// EnqueueOpts is what callers pass to enqueue a job. Required: Kind,
// Payload (already-marshalled JSON). RotationID + TargetNodeNum are
// optional indexing hints for the dispatcher debounce + UI grouping.
type EnqueueOpts struct {
	Kind          Kind
	RotationID    *string
	TargetNodeNum *int64
	Payload       any // marshalled to JSONB; pass struct or json.RawMessage
}

// Enqueue inserts a queued job and returns its id.
func (s *Store) Enqueue(ctx context.Context, opts EnqueueOpts) (string, error) {
	payload, err := json.Marshal(opts.Payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	var id string
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO fleet_jobs (kind, rotation_id, target_node_num, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text`,
		string(opts.Kind), opts.RotationID, opts.TargetNodeNum, payload,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

// LeaseNext atomically grabs the oldest queued job and marks it
// in_progress with workerID. Uses FOR UPDATE SKIP LOCKED so a future
// multi-worker setup is safe; v1 has a single worker so contention
// is moot but the SKIP LOCKED costs nothing.
func (s *Store) LeaseNext(ctx context.Context, workerID string) (*Job, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var j Job
	err = tx.QueryRow(ctx, `
		SELECT id::text, kind, rotation_id::text, target_node_num,
		       state, attempts, COALESCE(last_error, ''), payload,
		       enqueued_at, started_at, finished_at, worker_id
		  FROM fleet_jobs
		 WHERE state = 'queued'
		 ORDER BY enqueued_at ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED`,
	).Scan(&j.ID, &j.Kind, &j.RotationID, &j.TargetNodeNum,
		&j.State, &j.Attempts, &j.LastError, &j.Payload,
		&j.EnqueuedAt, &j.StartedAt, &j.FinishedAt, &j.WorkerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoWork
		}
		return nil, fmt.Errorf("scan candidate: %w", err)
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		UPDATE fleet_jobs
		   SET state       = 'in_progress',
		       attempts    = attempts + 1,
		       started_at  = $2,
		       worker_id   = $3,
		       finished_at = NULL,
		       last_error  = NULL
		 WHERE id = $1::uuid`,
		j.ID, now, workerID)
	if err != nil {
		return nil, fmt.Errorf("lease update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lease: %w", err)
	}

	j.State = StateInProgress
	j.Attempts++
	j.StartedAt = &now
	j.WorkerID = &workerID
	return &j, nil
}

// MarkDone closes the job successfully.
func (s *Store) MarkDone(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_jobs
		   SET state       = 'done',
		       finished_at = $2,
		       last_error  = NULL
		 WHERE id = $1::uuid`,
		id, now)
	if err != nil {
		return fmt.Errorf("mark done: %w", err)
	}
	return nil
}

// MarkFailed records the error and closes the job. The worker decides
// the failure is terminal -- we don't retry automatically here.
// Operator-triggered retry is a separate Enqueue.
func (s *Store) MarkFailed(ctx context.Context, id, errMsg string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_jobs
		   SET state       = 'failed',
		       finished_at = $2,
		       last_error  = $3
		 WHERE id = $1::uuid`,
		id, now, errMsg)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// Cancel marks a queued job as cancelled. In-progress jobs are
// best-effort -- the worker can poll cancellation but won't be
// interrupted mid-radio-transaction.
func (s *Store) Cancel(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_jobs
		   SET state       = 'cancelled',
		       finished_at = $2
		 WHERE id = $1::uuid AND state = 'queued'`,
		id, now)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	return nil
}

// HasPendingFor returns true if there's a queued or in_progress job
// of the given kind targeting the given node. Used by the dispatcher
// hook to debounce: don't enqueue a fresh recover_stranded if one is
// already in flight for that node.
func (s *Store) HasPendingFor(ctx context.Context, kind Kind, targetNodeNum int64) (bool, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM fleet_jobs
		 WHERE kind = $1 AND target_node_num = $2
		   AND state IN ('queued', 'in_progress')`,
		string(kind), targetNodeNum,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has-pending: %w", err)
	}
	return count > 0, nil
}

// ResumeStaleLeases scans for in_progress jobs whose worker died (no
// heartbeat in the last threshold). Marks them queued again so the
// next LeaseNext picks them up. Handlers must be idempotent.
//
// Called on Loop startup. If the threshold is too short we'll re-run
// jobs that are still legitimately running (next worker tick LeaseNext
// would race); pick threshold > the longest expected single-job
// runtime. With the long-admin-timeout migration helpers, single-job
// max is ~150s; threshold = 5min is comfortable.
func (s *Store) ResumeStaleLeases(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold)
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE fleet_jobs
		   SET state       = 'queued',
		       worker_id   = NULL,
		       last_error  = COALESCE(last_error, '') || ' [worker died mid-run; re-queued]'
		 WHERE state       = 'in_progress'
		   AND started_at  < $1`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("resume stale: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// GetByID is for the UI's "show this job's status" view.
func (s *Store) GetByID(ctx context.Context, id string) (*Job, error) {
	var j Job
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id::text, kind, rotation_id::text, target_node_num,
		       state, attempts, COALESCE(last_error, ''), payload,
		       enqueued_at, started_at, finished_at, worker_id
		  FROM fleet_jobs
		 WHERE id = $1::uuid`, id,
	).Scan(&j.ID, &j.Kind, &j.RotationID, &j.TargetNodeNum,
		&j.State, &j.Attempts, &j.LastError, &j.Payload,
		&j.EnqueuedAt, &j.StartedAt, &j.FinishedAt, &j.WorkerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}

// ListByRotation returns all jobs for the given rotation, newest first.
// Used by the drawer's "Jobs" subview.
func (s *Store) ListByRotation(ctx context.Context, rotationID string) ([]Job, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id::text, kind, rotation_id::text, target_node_num,
		       state, attempts, COALESCE(last_error, ''), payload,
		       enqueued_at, started_at, finished_at, worker_id
		  FROM fleet_jobs
		 WHERE rotation_id = $1::uuid
		 ORDER BY enqueued_at DESC`, rotationID)
	if err != nil {
		return nil, fmt.Errorf("list by rotation: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Kind, &j.RotationID, &j.TargetNodeNum,
			&j.State, &j.Attempts, &j.LastError, &j.Payload,
			&j.EnqueuedAt, &j.StartedAt, &j.FinishedAt, &j.WorkerID); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, nil
}

// ErrNotFound mirrors fleetsec.ErrNotFound for the GetByID path.
var ErrNotFound = errors.New("jobs: not found")

// Handler runs one job. Implementations must be idempotent (the same
// job may be re-leased after a worker crash). Return nil for success,
// any non-nil error to mark failed.
type Handler interface {
	Kind() Kind
	Run(ctx context.Context, job *Job) error
}

// Loop is the polling worker. Single in-process goroutine: radio is
// exclusive so concurrent dispatch wouldn't help. Polls every
// PollInterval; when LeaseNext returns ErrNoWork, sleeps until the
// next tick.
type Loop struct {
	store        *Store
	handlers     map[Kind]Handler
	workerID     string
	pollInterval time.Duration
	jobTimeout   time.Duration
	logger       *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLoop constructs the polling worker. Register handlers via Register
// before calling Start.
func NewLoop(store *Store, workerID string, logger *slog.Logger) *Loop {
	if logger == nil {
		logger = slog.Default()
	}
	return &Loop{
		store:        store,
		handlers:     map[Kind]Handler{},
		workerID:     workerID,
		pollInterval: 2 * time.Second,
		// 8min covers the worst-case recover_stranded path (5min probe-retry
		// budget through a Heltec V3 USB-reboot window + ~30s migrate). All
		// other handlers complete in < 90s. ResumeStaleLeases threshold
		// matches in Loop.Start.
		jobTimeout: 8 * time.Minute,
		logger:     logger,
	}
}

// Register attaches a handler for the given kind. Idempotent.
func (l *Loop) Register(h Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handlers[h.Kind()] = h
}

// Start launches the polling goroutine. Returns immediately. Call
// Stop to drain + shutdown.
func (l *Loop) Start(parent context.Context) error {
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return errors.New("loop already started")
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancel = cancel
	l.done = make(chan struct{})
	l.mu.Unlock()

	// Resume any stale leases left over from a prior process.
	resumed, err := l.store.ResumeStaleLeases(ctx, 10*time.Minute)
	if err != nil {
		l.logger.Warn("jobs: failed to resume stale leases on startup",
			"error", err)
	} else if resumed > 0 {
		l.logger.Info("jobs: re-queued stale leases", "count", resumed)
	}

	go l.run(ctx)
	return nil
}

// Stop signals shutdown and waits for the polling goroutine to drain.
func (l *Loop) Stop() {
	l.mu.Lock()
	if l.cancel == nil {
		l.mu.Unlock()
		return
	}
	cancel := l.cancel
	done := l.done
	l.cancel = nil
	l.mu.Unlock()
	cancel()
	<-done
}

func (l *Loop) run(ctx context.Context) {
	defer close(l.done)
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := l.store.LeaseNext(ctx, l.workerID)
		if err != nil {
			if errors.Is(err, ErrNoWork) {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			l.logger.Warn("jobs: lease failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		l.dispatch(ctx, job)
	}
}

func (l *Loop) dispatch(parent context.Context, job *Job) {
	l.mu.Lock()
	h, ok := l.handlers[job.Kind]
	l.mu.Unlock()
	if !ok {
		err := fmt.Errorf("no handler registered for kind %q", job.Kind)
		l.logger.Error("jobs: dispatch", "job_id", job.ID, "kind", job.Kind, "error", err)
		_ = l.store.MarkFailed(parent, job.ID, err.Error())
		return
	}

	jobCtx, cancel := context.WithTimeout(parent, l.jobTimeout)
	defer cancel()

	l.logger.Info("jobs: running",
		"job_id", job.ID, "kind", job.Kind,
		"rotation_id", job.RotationID, "target_node", job.TargetNodeNum,
		"attempt", job.Attempts)

	if err := h.Run(jobCtx, job); err != nil {
		l.logger.Warn("jobs: handler failed",
			"job_id", job.ID, "kind", job.Kind, "error", err)
		_ = l.store.MarkFailed(parent, job.ID, err.Error())
		return
	}
	if err := l.store.MarkDone(parent, job.ID); err != nil {
		l.logger.Warn("jobs: mark done failed",
			"job_id", job.ID, "error", err)
	}
}
