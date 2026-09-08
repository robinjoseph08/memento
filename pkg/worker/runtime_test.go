package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestEnqueueCommitsAndRollsBackWithTransaction(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	runtime, err := New(db, func(context.Context, string) error { t.Error("enqueue executed work"); return nil })
	require.NoError(t, err)
	ctx := t.Context()
	// The durable queue is the PostgreSQL side of this adapter's public seam.
	count := func() int {
		n, err := db.NewSelect().Table("river_job").Count(ctx)
		require.NoError(t, err)
		return n
	}
	rollback := errors.New("rollback")
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		require.NoError(t, runtime.EnqueueImport(ctx, tx, "rollback"))
		assert.Zero(t, count(), "uncommitted work is invisible to other connections")
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	assert.Zero(t, count())
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return runtime.EnqueueImport(ctx, tx, "committed") }))
	assert.Equal(t, 1, count())
	var row struct {
		Args        string
		MaxAttempts int
		Queue       string
	}
	require.NoError(t, db.NewSelect().Table("river_job").Column("args", "max_attempts", "queue").Scan(ctx, &row))
	assert.JSONEq(t, `{"album_id":"committed"}`, row.Args)
	assert.Equal(t, 3, row.MaxAttempts)
	assert.Equal(t, "imports", row.Queue)
}

func TestEnqueueDeduplicatesConcurrentActiveImports(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	runtime, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	ctx := deadline(t)
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			results <- db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return runtime.EnqueueImport(ctx, tx, "same-album") })
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	count, err := db.NewSelect().Table("river_job").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	// A completed attempt must not prevent a later curator-requested import.
	_, err = db.NewUpdate().Table("river_job").Set("state = 'completed'").Set("finalized_at = CURRENT_TIMESTAMP").Where("true").Exec(ctx)
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "same-album")
	count, err = db.NewSelect().Table("river_job").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestExplicitRetryReactivatesExistingRetryableJob(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	attempts := make(chan string, 3)
	release := make(chan struct{})
	runtime, err := New(db, func(ctx context.Context, albumID string) error {
		attempts <- albumID
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "retry")
	var id int64
	require.NoError(t, db.NewSelect().Table("river_job").Column("id").Scan(ctx, &id))
	// A retryable-state fixture avoids racing River's automatic retry scheduler.
	_, err = db.NewUpdate().Table("river_job").Set("state = 'retryable'").Set("attempt = 1").Set("scheduled_at = ?", time.Now().Add(time.Hour)).Where("id = ?", id).Exec(ctx)
	require.NoError(t, err)
	rollback := errors.New("rollback retry")
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		require.NoError(t, runtime.EnqueueImport(ctx, tx, "retry"))
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	row, err := runtime.client.JobGet(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, rivertype.JobStateRetryable, row.State)
	enqueue(t, ctx, db, runtime, "retry")
	row, err = runtime.client.JobGet(ctx, id)
	require.NoError(t, err)
	require.Equal(t, rivertype.JobStateAvailable, row.State)
	events, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	assert.Equal(t, "retry", receive(t, ctx, attempts))
	running, err := runtime.client.JobGet(ctx, id)
	require.NoError(t, err)
	require.Equal(t, rivertype.JobStateRunning, running.State)
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return runtime.EnqueueImport(ctx, tx, "retry")
	})
	require.ErrorIs(t, err, &errcodes.Error{HTTPCode: 409, Code: "import_attempt_running", Message: "The previous import attempt is still finishing. Try again in a moment."})
	var tracer interface{ StackTrace() pkgerrors.StackTrace }
	assert.NotErrorAs(t, err, &tracer)
	row, err = runtime.client.JobGet(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, running, row, "rejected enqueue leaves the running job unchanged")
	close(release)
	event := receive(t, ctx, events)
	assert.Equal(t, id, event.Job.ID)
	assert.Equal(t, 2, event.Job.Attempt)
}

func TestFinalAttemptDefaultsToTrueOutsideRuntime(t *testing.T) {
	t.Parallel()
	assert.True(t, FinalAttempt(t.Context()))
}

func TestFailedImportRetriesAutomaticallyThenDiscards(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	finalAttempts := make(chan bool, 3)
	runtime, err := New(db, func(ctx context.Context, _ string) error {
		finalAttempts <- FinalAttempt(ctx)
		return errors.New("HTTP failure with secret-token")
	})
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "failing")
	events, unsubscribe := runtime.client.Subscribe(river.EventKindJobFailed)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	for attempt := 1; attempt <= 3; attempt++ {
		event := receive(t, ctx, events)
		assert.Equal(t, attempt, event.Job.Attempt)
		assert.Equal(t, attempt == 3, receive(t, ctx, finalAttempts))
		require.Len(t, event.Job.Errors, attempt)
		assert.Equal(t, "execute import failed", event.Job.Errors[attempt-1].Error)
		if attempt == 3 {
			assert.Equal(t, rivertype.JobStateDiscarded, event.Job.State)
		} else {
			assert.Contains(t, []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateRetryable}, event.Job.State)
		}
	}
}

func TestImportPanicDoesNotPersistUpstreamSecrets(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, func(context.Context, string) error { panic("secret-token in upstream panic") })
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "panics")
	events, unsubscribe := runtime.client.Subscribe(river.EventKindJobFailed)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	event := receive(t, ctx, events)
	require.Len(t, event.Job.Errors, 1)
	assert.Equal(t, "execute import failed", event.Job.Errors[0].Error)
	assert.Empty(t, event.Job.Errors[0].Trace)
}

func TestStopCancelsBlockedImportAndFreshRuntimeResumes(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	entered := make(chan string, 1)
	cancelled := make(chan struct{}, 1)
	runtime, err := New(db, func(ctx context.Context, albumID string) error {
		entered <- albumID
		<-ctx.Done()
		cancelled <- struct{}{}
		return ctx.Err()
	})
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "resume")
	events, unsubscribe := runtime.client.Subscribe(river.EventKindJobInterrupted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	assert.Equal(t, "resume", receive(t, ctx, entered))
	// Stop first drains, then its finite grace period cancels cooperative work.
	require.NoError(t, runtime.Stop(ctx))
	receive(t, ctx, cancelled)
	interrupted := receive(t, ctx, events)
	assert.Equal(t, rivertype.JobStateAvailable, interrupted.Job.State)
	assert.Zero(t, interrupted.Job.Attempt)
	assert.Empty(t, interrupted.Job.Errors)
	resumed := make(chan string, 1)
	fresh, err := New(db, func(_ context.Context, albumID string) error { resumed <- albumID; return nil })
	require.NoError(t, err)
	completed, unsubscribeFresh := fresh.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribeFresh)
	startRuntime(t, ctx, fresh)
	assert.Equal(t, "resume", receive(t, ctx, resumed))
	done := receive(t, ctx, completed)
	assert.Equal(t, interrupted.Job.ID, done.Job.ID)
	assert.Equal(t, 1, done.Job.Attempt)
	require.NoError(t, fresh.Stop(ctx))
	require.NoError(t, db.PingContext(ctx), "runtime does not own the shared pool")
}

func TestRuntimeRescuesStaleRunningCrashState(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "crash-state")
	// This is the persisted state of a dead worker, not a simulated JobRetry.
	_, err = db.NewUpdate().Table("river_job").Set("state = 'running'").Set("attempt = 1").Set("attempted_at = ?", time.Now().Add(-17*time.Minute)).Where("true").Exec(ctx)
	require.NoError(t, err)
	fresh, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	completed, unsubscribe := fresh.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, fresh)
	event := receive(t, ctx, completed)
	assert.Equal(t, 2, event.Job.Attempt)
	require.Len(t, event.Job.Errors, 1)
	assert.Equal(t, "Stuck job rescued by JobRescuer", event.Job.Errors[0].Error)
}

func TestRuntimeBoundsConcurrentImports(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	entered := make(chan string, 8)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	runtime, err := New(db, func(ctx context.Context, albumID string) error {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, current) {
				break
			}
		}
		entered <- albumID
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	for i := range 8 {
		enqueue(t, ctx, db, runtime, fmt.Sprintf("album-%d", i))
	}
	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, entered)
	receive(t, ctx, entered)
	count, err := db.NewSelect().Table("river_job").Where("state = 'running'").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	close(release)
	for range 8 {
		receive(t, ctx, completed)
	}
	assert.Equal(t, int32(2), maximum.Load())
}

func TestEnqueueFailureKeepsCauseAndStackWithoutExposingDetails(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	err = runtime.EnqueueImport(ctx, tx, "album")
	require.ErrorIs(t, err, sql.ErrTxDone)
	assert.Equal(t, "enqueue import failed", err.Error())
	var tracer interface{ StackTrace() pkgerrors.StackTrace }
	require.ErrorAs(t, err, &tracer)
	assert.Contains(t, fmt.Sprintf("%+v", tracer.StackTrace()), "worker.runtimeError")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = runtime.Start(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorAs(t, err, &tracer)
}

func TestWorkerExpectedErrorsRemainStackless(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for name, expected := range map[string]error{
		"missing album": errcodes.NotFound("Album"),
		"validation":    errcodes.ValidationFields("invalid", map[string]string{"album_id": "required"}),
		"wrapped":       fmt.Errorf("import: %w", errcodes.NotFound("Album")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			executor := rivertest.NewWorker(t, riverdatabasesql.New(db.DB), &river.Config{}, &importWorker{
				execute: func(context.Context, string) error { return expected },
			})
			tx, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = executor.Work(t.Context(), t, tx.Tx, importArgs{AlbumID: "album"}, nil)
			require.ErrorIs(t, err, expected)
			assert.Equal(t, "execute import failed", err.Error())
			var tracer interface{ StackTrace() pkgerrors.StackTrace }
			assert.NotErrorAs(t, err, &tracer)
		})
	}
}

func TestMigrationRollsBackRiverAndCanReapply(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	var versions []int
	require.NoError(t, db.NewSelect().Table("river_migration").Column("version").Order("version").Scan(ctx, &versions))
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7}, versions)
	_, err := migrations.BringUpToDate(ctx, db)
	require.NoError(t, err)
	_, err = migrations.Rollback(ctx, db)
	require.NoError(t, err)
	count, err := db.NewSelect().TableExpr("information_schema.tables").Where("table_schema = current_schema()").Where("table_name LIKE 'river_%'").Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
	_, err = migrations.BringUpToDate(ctx, db)
	require.NoError(t, err)
	runtime, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	enqueue(t, ctx, db, runtime, "after-reapply")
}

func enqueue(t *testing.T, ctx context.Context, db *bun.DB, runtime *Runtime, albumID string) {
	t.Helper()
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return runtime.EnqueueImport(ctx, tx, albumID) }))
}

func startRuntime(t *testing.T, ctx context.Context, runtime *Runtime) {
	t.Helper()
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		require.NoError(t, runtime.client.StopAndCancel(stopCtx))
	})
}

func receive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-ch:
		require.True(t, ok, "channel closed before checkpoint")
		return value
	case <-ctx.Done():
		t.Fatal("checkpoint deadline: ", ctx.Err())
		var zero T
		return zero
	}
}

func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx
}
