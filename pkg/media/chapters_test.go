package media_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type enqueued struct{ item, checksum string }

func chapterModule(t *testing.T, upstream *source) (*media.Module, *bun.DB, *[]enqueued) {
	t.Helper()
	db := testdb.New(t)
	module := media.New(db, upstream)
	calls := &[]enqueued{}
	module.EnqueueChapters = func(_ context.Context, tx bun.Tx, id, checksum string) error {
		require.NotNil(t, tx.Tx, "enqueueing joins the caller's transaction")
		*calls = append(*calls, enqueued{id, checksum})
		return nil
	}
	return module, db, calls
}

func insertVideo(t *testing.T, db *bun.DB, checksum string) models.MediaItem {
	t.Helper()
	now := time.Now().UTC()
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "clip-" + checksum, Checksum: checksum, Filename: "clip.mp4", Kind: "VIDEO",
		CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "v-" + checksum}
	_, err := db.NewInsert().Model(&item).Exec(t.Context())
	require.NoError(t, err)
	return item
}

func chapterRow(t *testing.T, db *bun.DB, id models.UUID) models.MediaChapterResult {
	t.Helper()
	var row models.MediaChapterResult
	require.NoError(t, db.NewSelect().Model(&row).Where("media_item_id = ?", id).Scan(t.Context()))
	return row
}

func TestRequestChaptersQueuesOncePerChecksum(t *testing.T) {
	t.Parallel()
	module, db, calls := chapterModule(t, &source{})
	item := insertVideo(t, db, "sha-1")
	request := func(checksum string) {
		require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			return module.RequestChapters(ctx, tx, item.ID, checksum)
		}))
	}
	request("sha-1")
	request("sha-1")
	require.Equal(t, []enqueued{{item.ID.String(), "sha-1"}}, *calls, "the same checksum is one unit of work")
	row := chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Equal(t, "sha-1", row.Checksum)
	require.Equal(t, []models.Chapter{}, row.Chapters)
	_, err := db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'complete'").Set("chapters = '[{\"title\":\"Old\",\"start\":0,\"end\":1}]'::jsonb").Where("media_item_id = ?", item.ID).Exec(t.Context())
	require.NoError(t, err)
	request("sha-2")
	require.Equal(t, []enqueued{{item.ID.String(), "sha-1"}, {item.ID.String(), "sha-2"}}, *calls, "a changed checksum queues fresh extraction")
	row = chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Equal(t, "sha-2", row.Checksum)
	require.Empty(t, row.Chapters, "stale chapters never show against new bytes")
	require.Equal(t, "pending", media.PublicChapterStatus(row.Status))
	unconfigured := media.New(db, &source{})
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return unconfigured.RequestChapters(ctx, tx, item.ID, "sha-3")
	})
	require.Error(t, err, "requesting work without a queue is a wiring mistake, not a silent skip")
}

func TestExtractChaptersRecordsResultsOnlyForTheCurrentChecksum(t *testing.T) {
	t.Parallel()
	upstream := &source{}
	module, db, _ := chapterModule(t, upstream)
	item := insertVideo(t, db, "sha-1")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, item.ID, "sha-1")
	}))
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) {
		return []ffprobe.Chapter{{Title: "Arrival", Start: 0, End: 2}, {Title: "Cake", Start: 2, End: 4.5}}, nil
	}
	require.NoError(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", false))
	require.Equal(t, "chapters:clip-sha-1", upstream.requested)
	row := chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusComplete, row.Status)
	require.Equal(t, []models.Chapter{{Title: "Arrival", Start: 0, End: 2}, {Title: "Cake", Start: 2, End: 4.5}}, row.Chapters)
	require.Empty(t, row.Message)
	// A repeated delivery of the same task finds finished work and stops.
	upstream.requested = ""
	require.NoError(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", false))
	require.Empty(t, upstream.requested)
	// An old-checksum task delivered after the checksum moved on does nothing.
	require.NoError(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-0", true))
	require.Empty(t, upstream.requested)
	require.Equal(t, media.ChapterStatusComplete, chapterRow(t, db, item.ID).Status)
	// Nonexistent and malformed identities are quietly finished as well.
	require.NoError(t, module.ExtractChapters(t.Context(), models.NewUUIDv7().String(), "sha", true))
	require.NoError(t, module.ExtractChapters(t.Context(), "not-a-uuid", "sha", true))

	// No chapters is a complete result with an empty list.
	plain := insertVideo(t, db, "sha-plain")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, plain.ID, "sha-plain")
	}))
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) { return []ffprobe.Chapter{}, nil }
	require.NoError(t, module.ExtractChapters(t.Context(), plain.ID.String(), "sha-plain", false))
	row = chapterRow(t, db, plain.ID)
	require.Equal(t, media.ChapterStatusComplete, row.Status)
	require.Equal(t, []models.Chapter{}, row.Chapters)
	require.Equal(t, "complete", media.PublicChapterStatus(row.Status))

	// A late finish for an old checksum cannot replace a newer queued row.
	moving := insertVideo(t, db, "sha-a")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, moving.ID, "sha-a")
	}))
	upstream.chapters = func(ctx context.Context, _ string) ([]ffprobe.Chapter, error) {
		require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return module.RequestChapters(ctx, tx, moving.ID, "sha-b")
		}))
		return []ffprobe.Chapter{{Title: "Stale", Start: 0, End: 1}}, nil
	}
	require.NoError(t, module.ExtractChapters(t.Context(), moving.ID.String(), "sha-a", false))
	row = chapterRow(t, db, moving.ID)
	require.Equal(t, "sha-b", row.Checksum)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Empty(t, row.Chapters, "the current checksum wins")
}

func TestExtractChaptersFailuresAreVisibleRetryableAndNeverFatal(t *testing.T) {
	t.Parallel()
	upstream := &source{}
	module, db, calls := chapterModule(t, upstream)
	item := insertVideo(t, db, "sha-1")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, item.ID, "sha-1")
	}))
	probeErr := &ffprobe.Error{Detail: "Server returned 403 Forbidden for [url]"}
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) { return nil, probeErr }
	require.ErrorIs(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", false), probeErr)
	row := chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Contains(t, row.Message, "retry automatically")
	require.NotContains(t, row.Message, "403", "a retrying attempt keeps the short message")
	require.ErrorIs(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", true), probeErr)
	row = chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusFailed, row.Status)
	require.Contains(t, row.Message, "Playback still works")
	require.Contains(t, row.Message, "Server returned 403 Forbidden for [url]", "the redacted cause tells the Curator what to fix")
	require.Equal(t, "failed", media.PublicChapterStatus(row.Status))
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) {
		return nil, &errcodes.Error{HTTPCode: 403, Code: "immich_permission_denied", Message: "The Immich API key is missing permission. Enable asset.download, then retry."}
	}
	require.Error(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", true))
	require.Contains(t, chapterRow(t, db, item.ID).Message, "Enable asset.download")
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) {
		return nil, errors.New("raw driver text with secret")
	}
	require.Error(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", true))
	require.NotContains(t, chapterRow(t, db, item.ID).Message, "secret", "unknown errors never reach the Curator")

	// Retry requeues the same checksum without inventing a new result.
	require.Len(t, *calls, 1)
	require.NoError(t, module.RetryChapters(t.Context(), item.ID.String()))
	require.Len(t, *calls, 2)
	require.Equal(t, enqueued{item.ID.String(), "sha-1"}, (*calls)[1])
	row = chapterRow(t, db, item.ID)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Empty(t, row.Message)
	upstream.chapters = func(context.Context, string) ([]ffprobe.Chapter, error) {
		return []ffprobe.Chapter{{Title: "Fixed", Start: 0, End: 1}}, nil
	}
	require.NoError(t, module.ExtractChapters(t.Context(), item.ID.String(), "sha-1", false))
	require.Equal(t, media.ChapterStatusComplete, chapterRow(t, db, item.ID).Status)
	require.NoError(t, module.RetryChapters(t.Context(), item.ID.String()))
	require.Len(t, *calls, 2, "a complete result needs no retry")
	require.ErrorIs(t, module.RetryChapters(t.Context(), models.NewUUIDv7().String()), errcodes.NotFound("Video"))
	require.ErrorIs(t, module.RetryChapters(t.Context(), "junk"), errcodes.NotFound("Video"))
	// A retry after the source checksum moved on probes the new bytes.
	_, err := db.NewUpdate().Model((*models.MediaItem)(nil)).Set("checksum = 'sha-2'").Where("id = ?", item.ID).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, module.RetryChapters(t.Context(), item.ID.String()))
	require.Equal(t, enqueued{item.ID.String(), "sha-2"}, (*calls)[2])
	require.Equal(t, "sha-2", chapterRow(t, db, item.ID).Checksum)

	// Cancellation leaves the row queued for the resumed task.
	cancelled := insertVideo(t, db, "sha-c")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, cancelled.ID, "sha-c")
	}))
	ctx, cancel := context.WithCancel(t.Context())
	upstream.chapters = func(ctx context.Context, _ string) ([]ffprobe.Chapter, error) {
		cancel()
		return nil, ctx.Err()
	}
	require.ErrorIs(t, module.ExtractChapters(ctx, cancelled.ID.String(), "sha-c", true), context.Canceled)
	row = chapterRow(t, db, cancelled.ID)
	require.Equal(t, media.ChapterStatusQueued, row.Status)
	require.Empty(t, row.Message)

	// A task deadline on the last attempt is a failure the Curator can retry,
	// not a task that quietly stays pending with no job behind it.
	stalled := insertVideo(t, db, "sha-d")
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, stalled.ID, "sha-d")
	}))
	timedOut, expire := context.WithTimeout(t.Context(), 50*time.Millisecond)
	t.Cleanup(expire)
	upstream.chapters = func(ctx context.Context, _ string) ([]ffprobe.Chapter, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	require.ErrorIs(t, module.ExtractChapters(timedOut, stalled.ID.String(), "sha-d", true), context.DeadlineExceeded)
	row = chapterRow(t, db, stalled.ID)
	require.Equal(t, media.ChapterStatusFailed, row.Status)
	require.Contains(t, row.Message, "Playback still works")
}

func TestBackfillChaptersQueuesOnlyUnprobedVideos(t *testing.T) {
	t.Parallel()
	module, db, calls := chapterModule(t, &source{})
	first := insertVideo(t, db, "sha-1")
	second := insertVideo(t, db, "sha-2")
	now := time.Now().UTC()
	photo := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "photo", Checksum: "p", Filename: "photo.jpg", Kind: "IMAGE",
		CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "p"}
	_, err := db.NewInsert().Model(&photo).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RequestChapters(ctx, tx, first.ID, "sha-1")
	}))
	// The first video's queued row is handed to the queue again, which
	// deduplicates a live task; the second video has no row and gets one.
	queued, err := module.BackfillChapters(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, queued)
	require.Equal(t, []enqueued{{first.ID.String(), "sha-1"}, {first.ID.String(), "sha-1"}, {second.ID.String(), "sha-2"}}, *calls)
	queued, err = module.BackfillChapters(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, queued, "a second start hands both queued rows to the queue again")
	require.Len(t, *calls, 5)
	// Finished and failed rows are left alone; a row stuck in processing after
	// a hard stop is requeued.
	_, err = db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'complete'").Where("media_item_id = ?", first.ID).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'processing'").Where("media_item_id = ?", second.ID).Exec(t.Context())
	require.NoError(t, err)
	queued, err = module.BackfillChapters(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, queued)
	require.Equal(t, enqueued{second.ID.String(), "sha-2"}, (*calls)[5])
	_, err = db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'failed'").Where("media_item_id = ?", second.ID).Exec(t.Context())
	require.NoError(t, err)
	queued, err = module.BackfillChapters(t.Context())
	require.NoError(t, err)
	require.Zero(t, queued, "a failed row waits for the Curator's retry")
}
