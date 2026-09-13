package worker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func noImport(context.Context, string) error { return nil }

func TestEnqueueChaptersUsesTheFFprobeQueueAndTransaction(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, noImport, Chapters(func(context.Context, string, string) error {
		t.Error("enqueue executed work")
		return nil
	}, 1))
	require.NoError(t, err)
	rollback := errors.New("rollback")
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		require.NoError(t, runtime.EnqueueChapters(ctx, tx, "item", "sha-1"))
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	assertJobCount(t, ctx, db, 0)
	enqueueChapters(t, ctx, db, runtime, "item", "sha-1")
	var row struct {
		Args        string
		MaxAttempts int
		Queue       string
		Kind        string
	}
	require.NoError(t, db.NewSelect().Table("river_job").Column("args", "max_attempts", "queue", "kind").Scan(ctx, &row))
	assert.JSONEq(t, `{"media_item_id":"item","checksum":"sha-1"}`, row.Args)
	assert.Equal(t, 3, row.MaxAttempts)
	assert.Equal(t, "ffprobe", row.Queue)
	assert.Equal(t, "extract_video_chapters", row.Kind)
	// The same item at the same checksum is one unit of work; a new checksum is another.
	enqueueChapters(t, ctx, db, runtime, "item", "sha-1")
	assertJobCount(t, ctx, db, 1)
	enqueueChapters(t, ctx, db, runtime, "item", "sha-2")
	assertJobCount(t, ctx, db, 2)
	_, err = db.NewUpdate().Table("river_job").Set("state = 'completed'").Set("finalized_at = CURRENT_TIMESTAMP").Where("true").Exec(ctx)
	require.NoError(t, err)
	enqueueChapters(t, ctx, db, runtime, "item", "sha-1")
	assertJobCount(t, ctx, db, 3)
}

func TestEnqueueChaptersRequiresConfiguration(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	runtime, err := New(db, noImport)
	require.NoError(t, err)
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return runtime.EnqueueChapters(ctx, tx, "item", "sha")
	})
	require.Error(t, err)
	_, err = New(db, noImport, Chapters(func(context.Context, string, string) error { return nil }, 0))
	require.Error(t, err, "concurrency must be positive")
}

func TestChapterQueueBoundsConcurrencyIndependentlyOfImports(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	entered := make(chan string, 8)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	blockUntilReleased := func(ctx context.Context) error {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, current) {
				break
			}
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	runtime, err := New(db, func(ctx context.Context, albumID string) error {
		entered <- "import:" + albumID
		return blockUntilReleased(ctx)
	}, Chapters(func(ctx context.Context, id, checksum string) error {
		entered <- "chapters:" + id + ":" + checksum
		return blockUntilReleased(ctx)
	}, 1))
	require.NoError(t, err)
	for i := range 3 {
		enqueueChapters(t, ctx, db, runtime, fmt.Sprintf("item-%d", i), "sha")
	}
	enqueue(t, ctx, db, runtime, "album")
	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	first := receive(t, ctx, entered)
	second := receive(t, ctx, entered)
	assert.ElementsMatch(t, []string{"import:album", "chapters:item-0:sha"}, []string{first, second}, "one ffprobe task runs beside imports")
	running, err := db.NewSelect().Table("river_job").Where("state = 'running' AND queue = 'ffprobe'").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, running)
	close(release)
	for range 4 {
		receive(t, ctx, completed)
	}
	assert.Equal(t, int32(2), maximum.Load())
}

func TestChapterFailuresRetryThenDiscardWithoutUpstreamDetail(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	finalAttempts := make(chan bool, 3)
	runtime, err := New(db, noImport, Chapters(func(ctx context.Context, _, _ string) error {
		finalAttempts <- FinalAttempt(ctx)
		return errors.New("ffprobe stderr with private-key")
	}, 1))
	require.NoError(t, err)
	enqueueChapters(t, ctx, db, runtime, "item", "sha")
	events, unsubscribe := runtime.client.Subscribe(river.EventKindJobFailed)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	for attempt := 1; attempt <= 3; attempt++ {
		event := receive(t, ctx, events)
		assert.Equal(t, attempt, event.Job.Attempt)
		assert.Equal(t, attempt == 3, receive(t, ctx, finalAttempts))
		require.Len(t, event.Job.Errors, attempt)
		assert.Equal(t, "extract chapters failed", event.Job.Errors[attempt-1].Error)
		if attempt == 3 {
			assert.Equal(t, rivertype.JobStateDiscarded, event.Job.State)
		}
	}
	// A retryable job is reactivated by an explicit request instead of waiting.
	enqueueChapters(t, ctx, db, runtime, "item", "sha")
	assertJobCount(t, ctx, db, 2)
}

func TestChapterRetryableJobIsReactivatedByEnqueue(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, noImport, Chapters(func(context.Context, string, string) error { return nil }, 1))
	require.NoError(t, err)
	enqueueChapters(t, ctx, db, runtime, "item", "sha")
	var id int64
	require.NoError(t, db.NewSelect().Table("river_job").Column("id").Scan(ctx, &id))
	_, err = db.NewUpdate().Table("river_job").Set("state = 'retryable'").Set("attempt = 1").Set("scheduled_at = ?", time.Now().Add(time.Hour)).Where("id = ?", id).Exec(ctx)
	require.NoError(t, err)
	enqueueChapters(t, ctx, db, runtime, "item", "sha")
	row, err := runtime.client.JobGet(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, rivertype.JobStateAvailable, row.State)
	assertJobCount(t, ctx, db, 1)
}

func TestStopCancelsRunningChapterTaskAndFreshRuntimeResumes(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	entered := make(chan struct{}, 1)
	runtime, err := New(db, noImport, Chapters(func(ctx context.Context, _, _ string) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}, 1))
	require.NoError(t, err)
	enqueueChapters(t, ctx, db, runtime, "item", "sha")
	interrupted, unsubscribe := runtime.client.Subscribe(river.EventKindJobInterrupted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, entered)
	require.NoError(t, runtime.client.StopAndCancel(ctx))
	event := receive(t, ctx, interrupted)
	assert.Equal(t, rivertype.JobStateAvailable, event.Job.State)
	resumed := make(chan string, 1)
	fresh, err := New(db, noImport, Chapters(func(_ context.Context, id, checksum string) error {
		resumed <- id + ":" + checksum
		return nil
	}, 1))
	require.NoError(t, err)
	completed, unsubscribeFresh := fresh.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribeFresh)
	startRuntime(t, ctx, fresh)
	assert.Equal(t, "item:sha", receive(t, ctx, resumed))
	done := receive(t, ctx, completed)
	assert.Equal(t, event.Job.ID, done.Job.ID)
}

func enqueueChapters(t *testing.T, ctx context.Context, db *bun.DB, runtime *Runtime, id, checksum string) {
	t.Helper()
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return runtime.EnqueueChapters(ctx, tx, id, checksum)
	}))
}
