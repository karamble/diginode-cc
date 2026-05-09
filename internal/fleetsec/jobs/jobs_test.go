package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karamble/diginode-cc/internal/database"
)

// requireDB returns a connection to the integration-test postgres or
// skips. Mirrors fleetsec's requireDB pattern.
func requireDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("FLEETSEC_TEST_DB")
	if dsn == "" {
		t.Skip("FLEETSEC_TEST_DB not set; skipping jobs integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return &database.DB{Pool: pool}
}

func resetJobs(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE fleet_jobs CASCADE`); err != nil {
		t.Fatalf("reset fleet_jobs: %v", err)
	}
}

func TestStore_EnqueueAndLeaseFIFO(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	id1, err := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: map[string]any{"first": true}})
	if err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	// Force ordering by enqueued_at: small sleep so the second enqueue
	// has a strictly later timestamp.
	time.Sleep(10 * time.Millisecond)
	id2, err := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseB, Payload: map[string]any{"second": true}})
	if err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}

	first, err := s.LeaseNext(ctx, "test-worker-1")
	if err != nil {
		t.Fatalf("lease 1: %v", err)
	}
	if first.ID != id1 {
		t.Errorf("FIFO violated: first lease = %s, want %s (id1)", first.ID, id1)
	}
	if first.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", first.Attempts)
	}

	second, err := s.LeaseNext(ctx, "test-worker-1")
	if err != nil {
		t.Fatalf("lease 2: %v", err)
	}
	if second.ID != id2 {
		t.Errorf("FIFO violated: second lease = %s, want %s (id2)", second.ID, id2)
	}

	// Queue exhausted; LeaseNext should return ErrNoWork.
	if _, err := s.LeaseNext(ctx, "test-worker-1"); !errors.Is(err, ErrNoWork) {
		t.Errorf("third lease err = %v, want ErrNoWork", err)
	}
}

func TestStore_LeaseConcurrent_NoDoubleGrab(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	const N = 20
	for i := 0; i < N; i++ {
		if _, err := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: map[string]any{"i": i}}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	const Workers = 4
	var (
		wg       sync.WaitGroup
		seen     sync.Map
		double   atomic.Int32
		leasedCt atomic.Int32
	)
	for w := 0; w < Workers; w++ {
		wg.Add(1)
		go func(workerNum int) {
			defer wg.Done()
			for {
				job, err := s.LeaseNext(ctx, fmt.Sprintf("w-%d", workerNum))
				if errors.Is(err, ErrNoWork) {
					return
				}
				if err != nil {
					t.Errorf("lease err: %v", err)
					return
				}
				if _, alreadySeen := seen.LoadOrStore(job.ID, true); alreadySeen {
					double.Add(1)
				}
				leasedCt.Add(1)
			}
		}(w)
	}
	wg.Wait()

	if double.Load() != 0 {
		t.Errorf("double-grabbed %d jobs (FOR UPDATE SKIP LOCKED broken)", double.Load())
	}
	if leasedCt.Load() != int32(N) {
		t.Errorf("leased %d jobs, want %d", leasedCt.Load(), N)
	}
}

func TestStore_MarkDoneAndFailed(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	idDone, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})
	idFail, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})

	j1, _ := s.LeaseNext(ctx, "w")
	j2, _ := s.LeaseNext(ctx, "w")
	if j1.ID != idDone || j2.ID != idFail {
		t.Fatalf("lease order off: got %s, %s", j1.ID, j2.ID)
	}

	if err := s.MarkDone(ctx, j1.ID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if err := s.MarkFailed(ctx, j2.ID, "oops"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	got1, _ := s.GetByID(ctx, j1.ID)
	if got1.State != StateDone {
		t.Errorf("done job state = %s, want done", got1.State)
	}
	if got1.LastError != "" {
		t.Errorf("done job last_error = %q, want empty", got1.LastError)
	}
	got2, _ := s.GetByID(ctx, j2.ID)
	if got2.State != StateFailed {
		t.Errorf("failed job state = %s, want failed", got2.State)
	}
	if got2.LastError != "oops" {
		t.Errorf("failed job last_error = %q, want oops", got2.LastError)
	}
}

func TestStore_HasPendingFor(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	const target int64 = 0x0409d4e4
	tt := target

	// Initially no pending jobs.
	pending, err := s.HasPendingFor(ctx, KindRecoverStrand, target)
	if err != nil || pending {
		t.Fatalf("initial: pending=%v err=%v", pending, err)
	}

	// Enqueue one. HasPendingFor should now return true.
	id, _ := s.Enqueue(ctx, EnqueueOpts{
		Kind:          KindRecoverStrand,
		TargetNodeNum: &tt,
		Payload:       map[string]any{"prevPSKFP": "aa:bb"},
	})
	pending, _ = s.HasPendingFor(ctx, KindRecoverStrand, target)
	if !pending {
		t.Error("after enqueue: HasPendingFor returned false, want true")
	}

	// Lease it -> still in_progress, still counts as pending.
	leased, _ := s.LeaseNext(ctx, "w")
	if leased.ID != id {
		t.Fatalf("lease id mismatch: got %s want %s", leased.ID, id)
	}
	pending, _ = s.HasPendingFor(ctx, KindRecoverStrand, target)
	if !pending {
		t.Error("in_progress: HasPendingFor returned false, want true")
	}

	// Mark done. No longer pending.
	_ = s.MarkDone(ctx, id)
	pending, _ = s.HasPendingFor(ctx, KindRecoverStrand, target)
	if pending {
		t.Error("after done: HasPendingFor returned true, want false")
	}
}

func TestStore_ResumeStaleLeases(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	id, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})
	_, _ = s.LeaseNext(ctx, "old-worker")

	// Force the started_at to be 10 minutes ago so the resume threshold
	// of 5 minutes catches it.
	if _, err := db.Pool.Exec(ctx, `
		UPDATE fleet_jobs SET started_at = now() - interval '10 minutes'
		 WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	resumed, err := s.ResumeStaleLeases(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed != 1 {
		t.Errorf("resumed = %d, want 1", resumed)
	}

	// Job should be available for lease again, with attempts NOT
	// incremented yet (will increment on next LeaseNext).
	relsd, err := s.LeaseNext(ctx, "new-worker")
	if err != nil {
		t.Fatalf("re-lease after resume: %v", err)
	}
	if relsd.ID != id {
		t.Errorf("re-lease id = %s, want %s", relsd.ID, id)
	}
	if relsd.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (1 from original + 1 from re-lease)", relsd.Attempts)
	}
}

// stubHandler implements Handler with a configurable run callback so
// loop integration tests can drive the worker through scenarios.
type stubHandler struct {
	kind Kind
	run  func(ctx context.Context, j *Job) error
}

func (s *stubHandler) Kind() Kind                              { return s.kind }
func (s *stubHandler) Run(ctx context.Context, j *Job) error  { return s.run(ctx, j) }

func TestLoop_RunsHandlerAndMarksDone(t *testing.T) {
	db := requireDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	loop := NewLoop(s, "test-loop-1", nil)
	loop.pollInterval = 50 * time.Millisecond

	var called atomic.Int32
	loop.Register(&stubHandler{
		kind: KindRotatePhaseA,
		run: func(_ context.Context, _ *Job) error {
			called.Add(1)
			return nil
		},
	})

	id, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})

	if err := loop.Start(ctx); err != nil {
		t.Fatalf("loop start: %v", err)
	}
	defer loop.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if called.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if called.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", called.Load())
	}

	// Job should be in done state.
	j, _ := s.GetByID(ctx, id)
	if j.State != StateDone {
		t.Errorf("post-run state = %s, want done", j.State)
	}
}

func TestLoop_HandlerErrorMarksFailed(t *testing.T) {
	db := requireDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	loop := NewLoop(s, "test-loop-2", nil)
	loop.pollInterval = 50 * time.Millisecond
	loop.Register(&stubHandler{
		kind: KindRotatePhaseA,
		run: func(_ context.Context, _ *Job) error {
			return errors.New("synthetic failure")
		},
	})

	id, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})

	if err := loop.Start(ctx); err != nil {
		t.Fatalf("loop start: %v", err)
	}
	defer loop.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.GetByID(ctx, id)
		if j != nil && j.State == StateFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	j, _ := s.GetByID(ctx, id)
	if j.State != StateFailed {
		t.Fatalf("state = %s, want failed", j.State)
	}
	if j.LastError != "synthetic failure" {
		t.Errorf("last_error = %q, want synthetic failure", j.LastError)
	}
}

func TestLoop_NoHandlerForKind_MarksFailed(t *testing.T) {
	db := requireDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resetJobs(ctx, t, db)
	s := NewStore(db)

	loop := NewLoop(s, "test-loop-3", nil)
	loop.pollInterval = 50 * time.Millisecond
	// No handlers registered.

	id, _ := s.Enqueue(ctx, EnqueueOpts{Kind: KindRotatePhaseA, Payload: nil})

	if err := loop.Start(ctx); err != nil {
		t.Fatalf("loop start: %v", err)
	}
	defer loop.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.GetByID(ctx, id)
		if j != nil && j.State == StateFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	j, _ := s.GetByID(ctx, id)
	if j.State != StateFailed {
		t.Fatalf("state = %s, want failed (unhandled kind)", j.State)
	}
}
