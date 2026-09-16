package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// EnqueueChapters commits durable extraction with the caller's transaction. It
// never runs ffprobe inline.
type EnqueueChapters func(ctx context.Context, tx bun.Tx, mediaItemID, checksum string) error

// Chapter result statuses. Queued and processing both read as pending to
// browsers; complete carries the chapters, which may be none.
const (
	ChapterStatusQueued     = "queued"
	ChapterStatusProcessing = "processing"
	ChapterStatusComplete   = "complete"
	ChapterStatusFailed     = "failed"
)

// PublicChapterStatus folds the stored status into the three states the
// browser shows: pending, complete, or failed. No row means pending.
func PublicChapterStatus(status string) string {
	switch status {
	case ChapterStatusComplete, ChapterStatusFailed:
		return status
	}
	return "pending"
}

const (
	chapterRetryMessage  = "Chapter extraction failed. Memento will retry automatically."
	chapterFailedMessage = "Chapter extraction failed. Playback still works. Retry it from the video details."
)

// RequestChapters records that a video needs extraction at its current
// checksum and enqueues the work in the same transaction. An existing result
// at the same checksum is kept, so repeated imports never redo finished work.
func (m *Module) RequestChapters(ctx context.Context, tx bun.Tx, mediaItemID models.UUID, checksum string) error {
	if m.EnqueueChapters == nil {
		return errorstack.CaptureContext(ctx, errors.New("chapter extraction is not configured"))
	}
	if checksum == "" {
		return errcodes.ValidationError("A source checksum is required for chapter extraction.")
	}
	row := models.MediaChapterResult{MediaItemID: mediaItemID, Checksum: checksum, Status: ChapterStatusQueued, Chapters: []models.Chapter{}, UpdatedAt: time.Now().UTC()}
	result, err := tx.NewInsert().Model(&row).On("CONFLICT (media_item_id) DO UPDATE").
		Set("checksum = EXCLUDED.checksum").Set("status = EXCLUDED.status").Set("message = ''").
		Set("chapters = '[]'::jsonb").Set("updated_at = EXCLUDED.updated_at").
		Where("chapter_result.checksum <> EXCLUDED.checksum").Exec(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if changed == 0 {
		return nil
	}
	return m.EnqueueChapters(ctx, tx, mediaItemID.String(), checksum)
}

// RetryChapters requeues a failed or stalled extraction at the current
// checksum. A complete result needs no work and is left alone.
func (m *Module) RetryChapters(ctx context.Context, mediaItemID string) error {
	if m.EnqueueChapters == nil {
		return errorstack.CaptureContext(ctx, errors.New("chapter extraction is not configured"))
	}
	if _, err := uuid.Parse(mediaItemID); err != nil {
		return errcodes.NotFound("Video")
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var item models.MediaItem
		err := tx.NewSelect().Model(&item).Where("media_item.id = ? AND media_item.kind = 'VIDEO'", mediaItemID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errcodes.NotFound("Video")
		}
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var row models.MediaChapterResult
		err = tx.NewSelect().Model(&row).Where("chapter_result.media_item_id = ?", mediaItemID).Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errorstack.CaptureContext(ctx, err)
		}
		if err == nil && row.Status == ChapterStatusComplete && row.Checksum == item.Checksum {
			return nil
		}
		if err != nil || row.Checksum != item.Checksum {
			return m.RequestChapters(ctx, tx, item.ID, item.Checksum)
		}
		if _, err := tx.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = ?", ChapterStatusQueued).Set("message = ''").
			Set("updated_at = ?", time.Now().UTC()).Where("media_item_id = ?", mediaItemID).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		return m.EnqueueChapters(ctx, tx, mediaItemID, item.Checksum)
	})
	if _, expected := errors.AsType[*errcodes.Error](err); expected {
		return err
	}
	return errorstack.CaptureContext(ctx, err)
}

// BackfillChapters queues extraction for every video that has never been
// probed, so enabling the capability covers import-era videos, and re-enqueues
// rows still queued or processing from a run that ended abruptly. The queue
// deduplicates a task that is still alive, so it is safe to run at every
// start. It reports how many videos were queued.
func (m *Module) BackfillChapters(ctx context.Context) (int, error) {
	queued := 0
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var items []struct {
			ID       models.UUID
			Checksum string
			Status   string
		}
		err := tx.NewSelect().TableExpr("media_items AS media_item").ColumnExpr("media_item.id, media_item.checksum, coalesce(chapter_result.status, '') AS status").
			Join("LEFT JOIN media_chapter_results AS chapter_result ON chapter_result.media_item_id = media_item.id").
			Where("media_item.kind = 'VIDEO' AND (chapter_result.media_item_id IS NULL OR chapter_result.status IN (?, ?))", ChapterStatusQueued, ChapterStatusProcessing).
			OrderExpr("media_item.id").Scan(ctx, &items)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, item := range items {
			if item.Status == "" {
				if err := m.RequestChapters(ctx, tx, item.ID, item.Checksum); err != nil {
					return err
				}
			} else if err := m.EnqueueChapters(ctx, tx, item.ID.String(), item.Checksum); err != nil {
				return err
			}
			queued++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return queued, nil
}

// ExtractChapters is the worker body. It probes outside any transaction and
// writes only while the row still names the checksum it was asked about, so a
// task that finishes late cannot replace a newer result. finalAttempt decides
// whether a failure is shown as retrying or as failed.
func (m *Module) ExtractChapters(ctx context.Context, mediaItemID, checksum string, finalAttempt bool) (returnErr error) {
	if _, err := uuid.Parse(mediaItemID); err != nil || checksum == "" {
		return nil
	}
	// Every failed attempt records its outcome, including one whose deadline
	// ran out before the probe began, so a task never leaves the row quietly
	// stuck behind a job that will not come back.
	defer func() {
		if returnErr == nil {
			return
		}
		status, message := ChapterStatusQueued, chapterRetryMessage
		if finalAttempt {
			status, message = ChapterStatusFailed, chapterFailedMessage+failureReason(returnErr)
		}
		// A shutdown cancels the task and River resumes it, so the row stays
		// quietly queued. A deadline is a real failure that follows the attempt.
		if errorstack.IsContextCancellation(ctx, returnErr) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, message = ChapterStatusQueued, ""
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := m.recordChapters(recovery, mediaItemID, checksum, status, message, nil); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	var row struct {
		models.MediaChapterResult `bun:"embed:"`
		SourceID                  string
	}
	err := m.db.NewSelect().TableExpr("media_chapter_results AS chapter_result").ColumnExpr("chapter_result.*, item.source_id").
		Join("JOIN media_items AS item ON item.id = chapter_result.media_item_id").
		Where("chapter_result.media_item_id = ? AND chapter_result.checksum = ?", mediaItemID, checksum).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		// The row moved to another checksum or the video is gone; that work has its own task.
		return nil
	}
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if row.Status == ChapterStatusComplete {
		return nil
	}
	if err := m.recordChapters(ctx, mediaItemID, checksum, ChapterStatusProcessing, "", nil); err != nil {
		return err
	}
	probed, err := m.source.Chapters(ctx, row.SourceID)
	if err != nil {
		return err
	}
	chapters := make([]models.Chapter, 0, len(probed))
	for _, chapter := range probed {
		chapters = append(chapters, models.Chapter{Title: chapter.Title, Start: chapter.Start, End: chapter.End})
	}
	return m.recordChapters(ctx, mediaItemID, checksum, ChapterStatusComplete, "", chapters)
}

// failureReason adds the safe explanation a failure already carries: adapter
// errors describe the Immich outcome without secrets, and ffprobe's detail is
// redacted. Anything else stays out of the Curator's view.
func failureReason(err error) string {
	reason := ""
	if coded, ok := errors.AsType[*errcodes.Error](err); ok {
		reason = coded.Message
	} else if probe, ok := errors.AsType[*ffprobe.Error](err); ok {
		reason = probe.Detail
	}
	if reason == "" {
		return ""
	}
	if len(reason) > 300 {
		reason = reason[:300] + "…"
	}
	return " " + reason
}

// recordChapters writes progress only for the checksum the task was given.
func (m *Module) recordChapters(ctx context.Context, mediaItemID, checksum, status, message string, chapters []models.Chapter) error {
	query := m.db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = ?", status).Set("message = ?", message).
		Set("updated_at = ?", time.Now().UTC()).Where("media_item_id = ? AND checksum = ?", mediaItemID, checksum)
	if chapters != nil {
		encoded, err := json.Marshal(chapters)
		if err != nil {
			return errorstack.Capture(err)
		}
		query = query.Set("chapters = ?::jsonb", string(encoded))
	}
	_, err := query.Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}
