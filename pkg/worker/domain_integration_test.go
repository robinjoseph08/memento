package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestRetryDuringFinalAttemptPreservesFailedImport(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	source := domainLibrary()
	var fail atomic.Bool
	fail.Store(true)
	source.beforeAsset = func(_ context.Context, id string) error {
		if id == "second" && fail.Load() {
			return errors.New("controlled source failure")
		}
		return nil
	}
	failedPersisted := make(chan struct{}, 1)
	release := make(chan struct{})
	var module *publishing.Module
	runtime, err := New(db, func(ctx context.Context, id string) error {
		err := module.ExecuteImport(ctx, id, FinalAttempt(ctx))
		if err != nil && FinalAttempt(ctx) {
			failedPersisted <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return err
	})
	require.NoError(t, err)
	module = publishing.New(db, source, runtime.EnqueueImport)
	pending, err := module.StartImport(ctx, "source")
	require.NoError(t, err)
	require.Equal(t, "queued", pending.Status)
	assertJobCount(t, ctx, db, 1)
	failedEvents, unsubscribe := runtime.client.Subscribe(river.EventKindJobFailed)
	t.Cleanup(unsubscribe)
	completedEvents, unsubscribeCompleted := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribeCompleted)
	startRuntime(t, ctx, runtime)
	for attempt := 1; attempt < 3; attempt++ {
		event := receive(t, ctx, failedEvents)
		require.Equal(t, attempt, event.Job.Attempt)
		require.Contains(t, []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateRetryable}, event.Job.State)
	}
	receive(t, ctx, failedPersisted)
	failed, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, 1, failed.Processed)
	require.Empty(t, failed.Moments)
	_, err = module.RetryImport(ctx, pending.ID)
	require.ErrorIs(t, err, &errcodes.Error{HTTPCode: 409, Code: "import_attempt_running", Message: "The previous import attempt is still finishing. Try again in a moment."})
	afterRetry, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, failed, afterRetry, "retry must roll back its queued status and message change")
	assertJobCount(t, ctx, db, 1)
	fail.Store(false)
	close(release)
	discarded := receive(t, ctx, failedEvents)
	require.Equal(t, rivertype.JobStateDiscarded, discarded.Job.State)
	require.Equal(t, 3, discarded.Job.Attempt)
	_, err = module.RetryImport(ctx, pending.ID)
	require.NoError(t, err)
	completed := receive(t, ctx, completedEvents)
	require.NotEqual(t, discarded.Job.ID, completed.Job.ID)
	require.Equal(t, 1, completed.Job.Attempt)
	assertJobCount(t, ctx, db, 2)
	assertCompletedDomainImport(t, ctx, module, pending.ID)
}

func TestDomainImportIntentAndQueueRollbackTogether(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	runtime, err := New(db, func(context.Context, string) error {
		t.Error("enqueue must not execute an import")
		return nil
	})
	require.NoError(t, err)
	rollback := errors.New("controlled failure after durable enqueue")
	module := publishing.New(db, domainLibrary(), func(ctx context.Context, tx bun.Tx, id string) error {
		if err := runtime.EnqueueImport(ctx, tx, id); err != nil {
			return err
		}
		return rollback
	})
	_, err = module.StartImport(ctx, "source")
	require.ErrorIs(t, err, rollback)
	albums, err := module.ListAlbums(ctx, "")
	require.NoError(t, err)
	require.Empty(t, albums)
	assertJobCount(t, ctx, db, 0)
}

func TestDomainImportCancellationAndFreshRuntimeRecovery(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	source, checkpoint := pausedDomainLibrary()
	module, runtime := domainRuntime(t, db, source)
	pending, err := module.StartImport(ctx, "source")
	require.NoError(t, err)
	require.Equal(t, "queued", pending.Status)
	assertJobCount(t, ctx, db, 1)
	interruptedEvents, unsubscribe := runtime.client.Subscribe(river.EventKindJobInterrupted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, checkpoint)
	processing, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "processing", processing.Status)
	require.Equal(t, 1, processing.Processed)
	require.Equal(t, 2, processing.Total)
	require.Empty(t, processing.Moments)
	require.NoError(t, runtime.client.StopAndCancel(ctx))
	interruptedEvent := receive(t, ctx, interruptedEvents)
	require.Equal(t, rivertype.JobStateAvailable, interruptedEvent.Job.State)
	require.Zero(t, interruptedEvent.Job.Attempt)
	require.Empty(t, interruptedEvent.Job.Errors)
	interrupted, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", interrupted.Status)
	require.Equal(t, processing.ID, interrupted.ID)
	require.Equal(t, processing.Processed, interrupted.Processed)
	require.Equal(t, processing.Total, interrupted.Total)
	require.Empty(t, interrupted.Moments)

	freshModule, fresh := domainRuntime(t, db, domainLibrary())
	completedEvents, unsubscribeFresh := fresh.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribeFresh)
	startRuntime(t, ctx, fresh)
	completed := receive(t, ctx, completedEvents)
	require.Equal(t, interruptedEvent.Job.ID, completed.Job.ID)
	require.Equal(t, 1, completed.Job.Attempt)
	assertCompletedDomainImport(t, ctx, freshModule, pending.ID)
	assertJobCount(t, ctx, db, 1)
}

func TestDomainImportRescuesStaleRunningStateFixture(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	source, checkpoint := pausedDomainLibrary()
	module, runtime := domainRuntime(t, db, source)
	pending, err := module.StartImport(ctx, "source")
	require.NoError(t, err)
	var jobID int64
	require.NoError(t, db.NewSelect().Table("river_job").Column("id").Scan(ctx, &jobID))

	// Reach actual domain progress before constructing a crash-state fixture.
	// This is not a SIGKILL test. No worker remains alive when rescue begins.
	attemptCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- module.ExecuteImport(attemptCtx, pending.ID) }()
	receive(t, ctx, checkpoint)
	processing, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "processing", processing.Status)
	require.Equal(t, 1, processing.Processed)
	require.Empty(t, processing.Moments)
	cancel()
	require.ErrorIs(t, receive(t, ctx, done), context.Canceled)

	stale := time.Now().Add(-17 * time.Minute)
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*models.Album)(nil)).
			Set("import_status = 'processing'").Set("import_message = ''").Set("import_updated_at = ?", stale).
			Where("id = ?", pending.ID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Table("river_job").Set("state = 'running'").Set("attempt = 1").
			Set("attempted_at = ?", stale).Where("id = ?", jobID).Exec(ctx)
		return err
	})
	require.NoError(t, err)
	stopped, err := module.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", stopped.Status)
	require.Equal(t, 1, stopped.Processed)
	require.Equal(t, 2, stopped.Total)
	require.Empty(t, stopped.Moments)
	row, err := runtime.client.JobGet(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, rivertype.JobStateRunning, row.State)

	freshModule, fresh := domainRuntime(t, db, domainLibrary())
	completedEvents, unsubscribe := fresh.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, fresh)
	completed := receive(t, ctx, completedEvents)
	require.Equal(t, jobID, completed.Job.ID)
	require.Equal(t, 2, completed.Job.Attempt)
	require.Len(t, completed.Job.Errors, 1)
	require.Equal(t, "Stuck job rescued by JobRescuer", completed.Job.Errors[0].Error)
	assertCompletedDomainImport(t, ctx, freshModule, pending.ID)
	assertJobCount(t, ctx, db, 1)
}

func domainRuntime(t *testing.T, db *bun.DB, source immich.Library) (*publishing.Module, *Runtime) {
	t.Helper()
	var module *publishing.Module
	runtime, err := New(db, func(ctx context.Context, id string) error {
		return module.ExecuteImport(ctx, id, FinalAttempt(ctx))
	})
	require.NoError(t, err)
	module = publishing.New(db, source, runtime.EnqueueImport)
	return module, runtime
}

func pausedDomainLibrary() (*controlledLibrary, <-chan struct{}) {
	source := domainLibrary()
	checkpoint := make(chan struct{}, 1)
	source.beforeAsset = func(ctx context.Context, id string) error {
		if id == "second" {
			checkpoint <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	return source, checkpoint
}

// These tests replace only Immich. Publishing and the durable queue execute normally.
type controlledLibrary struct {
	assets      []immich.Asset
	beforeAsset func(context.Context, string) error
}

func domainLibrary() *controlledLibrary {
	return &controlledLibrary{assets: []immich.Asset{
		{ID: "first", Checksum: "YQ==", Filename: "first.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
		{ID: "second", Checksum: "Yg==", Filename: "second.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-05T00:01:00Z", FileCreatedAt: "2026-07-05T00:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
	}}
}

func (*controlledLibrary) CheckImport(context.Context) error { return nil }
func (l *controlledLibrary) ListAlbums(ctx context.Context) ([]immich.Album, error) {
	album, err := l.GetAlbum(ctx, "source")
	return []immich.Album{album}, err
}
func (*controlledLibrary) GetAlbum(context.Context, string) (immich.Album, error) {
	return immich.Album{ID: "source", Name: "Summer", Description: "From Immich", Count: 2}, nil
}
func (l *controlledLibrary) ListMembers(context.Context, string, int) ([]immich.Asset, int, error) {
	return l.assets, 0, nil
}
func (*controlledLibrary) ListFaces(context.Context, string) ([]immich.Face, error) {
	return nil, nil
}
func (l *controlledLibrary) GetAsset(ctx context.Context, id string) (immich.Asset, error) {
	if l.beforeAsset != nil {
		if err := l.beforeAsset(ctx, id); err != nil {
			return immich.Asset{}, err
		}
	}
	for _, asset := range l.assets {
		if asset.ID == id {
			return asset, nil
		}
	}
	return immich.Asset{}, errors.New("unknown fixture asset")
}

func assertJobCount(t *testing.T, ctx context.Context, db *bun.DB, want int) {
	t.Helper()
	count, err := db.NewSelect().Table("river_job").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, count)
}

func assertCompletedDomainImport(t *testing.T, ctx context.Context, module *publishing.Module, id string) {
	t.Helper()
	album, err := module.GetAlbum(ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, album.ID)
	require.Equal(t, "complete", album.Status)
	require.Equal(t, 2, album.Processed)
	require.Equal(t, 2, album.Total)
	require.False(t, album.Published)
	require.Len(t, album.Moments, 2)
	for _, moment := range album.Moments {
		require.Len(t, moment.Entries, 1)
	}
	require.Equal(t, "first.jpg", album.Moments[0].Entries[0].Filename)
	require.Equal(t, "second.jpg", album.Moments[1].Entries[0].Filename)
	require.NotEqual(t, album.Moments[0].Entries[0].ID, album.Moments[1].Entries[0].ID)
	require.NotEqual(t, album.Moments[0].Entries[0].MediaID, album.Moments[1].Entries[0].MediaID)
	albums, err := module.ListAlbums(ctx, "")
	require.NoError(t, err)
	require.Len(t, albums, 1)
	// Reopening and retrying a completed Album must keep every gallery identity.
	reopened, err := module.StartImport(ctx, "source")
	require.NoError(t, err)
	require.Equal(t, album, reopened)
	retried, err := module.RetryImport(ctx, id)
	require.NoError(t, err)
	require.Equal(t, album, retried)
}
