package publishing

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// StartImport commits source identity and durable enqueueing together. Existing Albums
// can be reopened even when Immich is unavailable or its version is unsupported.
func (m *Module) StartImport(ctx context.Context, sourceID string) (AlbumDetail, error) {
	var existing models.Album
	err := m.db.NewSelect().Model(&existing).Where("source_id = ?", sourceID).Scan(ctx)
	if err == nil {
		return m.GetAlbum(ctx, existing.ID.String())
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AlbumDetail{}, errorstack.CaptureContext(ctx, err)
	}
	if err := m.source.CheckImport(ctx); err != nil {
		return AlbumDetail{}, err
	}
	source, err := m.source.GetAlbum(ctx, sourceID)
	if err != nil {
		return AlbumDetail{}, err
	}
	if source.ID != sourceID || source.Name == "" || source.Count < 0 {
		return AlbumDetail{}, sourceChanged()
	}
	row := models.Album{ID: models.NewUUIDv7(), SourceID: sourceID, Title: source.Name, Description: source.Description,
		ImportStatus: "queued", ImportTotal: source.Count, ImportUpdatedAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewInsert().Model(&row).On("CONFLICT (source_id) DO NOTHING").Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if n == 0 {
			return errorstack.CaptureContext(ctx, tx.NewSelect().Model(&row).Where("source_id = ?", sourceID).Scan(ctx))
		}
		return m.enqueue(ctx, tx, row.ID.String())
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, row.ID.String())
}

func (m *Module) RetryImport(ctx context.Context, id string) (AlbumDetail, error) {
	// A complete Album needs no Immich request or new job.
	row, err := albumRow(ctx, m.db, id, false)
	if err != nil {
		return AlbumDetail{}, err
	}
	if row.ImportStatus == "complete" {
		return m.GetAlbum(ctx, id)
	}
	if err := m.source.CheckImport(ctx); err != nil {
		return AlbumDetail{}, err
	}
	err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := albumRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ImportStatus == "complete" {
			return nil
		}
		if row.ImportStatus == "processing" {
			if time.Since(row.ImportUpdatedAt) < time.Minute {
				return nil
			}
			// Enqueue deduplicates a still-running attempt and replaces a discarded one.
			// Keep stale processing visible as interrupted until a worker reports progress.
			return m.enqueue(ctx, tx, id)
		}
		if _, err := tx.NewUpdate().Model((*models.Album)(nil)).Set("import_status = 'queued'").Set("import_message = ''").
			Set("import_updated_at = ?", time.Now().UTC()).Where("id = ?", id).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		return m.enqueue(ctx, tx, id)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, id)
}

func sourceChanged() error {
	return &errcodes.Error{HTTPCode: 409, Code: "source_changed", Message: "The Immich Album changed or some members could not be read. Check its contents and API permissions, then retry."}
}

// ExecuteImport reads outside transactions, then commits a complete reviewed Album
// atomically. Repeating a completed import never overwrites Curator edits.
func (m *Module) ExecuteImport(ctx context.Context, id string, finalAttempt ...bool) (returnErr error) {
	row, err := albumRow(ctx, m.db, id, false)
	if err != nil {
		return err
	}
	if row.ImportStatus == "complete" {
		return nil
	}
	if err := m.progress(ctx, id, "processing", 0, ""); err != nil {
		return err
	}
	defer func() {
		if returnErr == nil {
			return
		}
		status, message := "failed", "Import failed. Check the Immich connection and permissions, then retry."
		if len(finalAttempt) > 0 && !finalAttempt[0] {
			status, message = "queued", "The source read failed. Memento will retry automatically."
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			status, message = "interrupted", "Import was interrupted. Memento will resume it automatically, or you can retry."
		}
		if typed, ok := errors.AsType[*errcodes.Error](returnErr); ok {
			message = typed.Message
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := m.progress(recovery, id, status, 0, message); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	if err := m.source.CheckImport(ctx); err != nil {
		return err
	}
	source, err := m.source.GetAlbum(ctx, row.SourceID)
	if err != nil {
		return err
	}
	if source.ID != row.SourceID {
		return sourceChanged()
	}
	items := []models.MediaItem{}
	seen := map[string]bool{}
	for page := 1; page != 0; {
		members, next, err := m.source.ListMembers(ctx, row.SourceID, page)
		if err != nil {
			return err
		}
		if next != 0 && next <= page {
			return sourceChanged()
		}
		for _, member := range members {
			if seen[member.ID] {
				return sourceChanged()
			}
			seen[member.ID] = true
			asset, err := m.source.GetAsset(ctx, member.ID)
			if err != nil {
				return err
			}
			if asset.ID != member.ID {
				return sourceChanged()
			}
			item, err := importItem(asset)
			if err != nil {
				return err
			}
			items = append(items, item)
			if err := m.progress(ctx, id, "processing", len(items), ""); err != nil {
				return err
			}
		}
		page = next
	}
	// Immich's Album timestamp is not a membership revision. Verify the IDs again
	// because asset changes can shift offset pages without changing the total.
	verified := make(map[string]bool, len(seen))
	for page := 1; page != 0; {
		members, next, err := m.source.ListMembers(ctx, row.SourceID, page)
		if err != nil {
			return err
		}
		if next != 0 && next <= page {
			return sourceChanged()
		}
		for _, member := range members {
			if !seen[member.ID] || verified[member.ID] {
				return sourceChanged()
			}
			verified[member.ID] = true
		}
		page = next
	}
	if len(verified) != len(seen) {
		return sourceChanged()
	}
	after, err := m.source.GetAlbum(ctx, row.SourceID)
	if err != nil {
		return err
	}
	if len(items) != source.Count || after.Count != source.Count || after.UpdatedAt != source.UpdatedAt {
		return sourceChanged()
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CapturedAt.Equal(items[j].CapturedAt) {
			return items[i].SourceID < items[j].SourceID
		}
		return items[i].CapturedAt.Before(items[j].CapturedAt)
	})
	return m.finishImport(ctx, id, source, items)
}

func (m *Module) progress(ctx context.Context, id, status string, processed int, message string) error {
	query := m.db.NewUpdate().Model((*models.Album)(nil)).Set("import_status = ?", status).Set("import_message = ?", message).
		Set("import_updated_at = ?", time.Now().UTC()).Where("id = ? AND import_status <> 'complete'", id)
	if status == "processing" {
		query = query.Set("import_processed = ?", processed)
	}
	_, err := query.Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

func importItem(asset immich.Asset) (models.MediaItem, error) {
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: asset.ID, Checksum: asset.Checksum, Filename: asset.Filename, Kind: asset.Kind,
		Offline: asset.Offline, Trashed: asset.Trashed, Width: asset.Width, Height: asset.Height, Duration: asset.Duration,
		Thumbhash: asset.Thumbhash, LivePhotoVideoID: asset.LivePhotoVideoID, EXIF: asset.EXIF}
	if asset.Stack != nil {
		item.SourceStackID = &asset.Stack.ID
		item.SourceStackPrimaryID = &asset.Stack.PrimaryAssetID
		item.SourceStackCount = &asset.Stack.AssetCount
	}
	if asset.Kind != "IMAGE" && asset.Kind != "VIDEO" {
		return item, &errcodes.Error{HTTPCode: 422, Code: "unsupported_media", Message: "This Album contains media other than photos and videos. Remove those members in Immich before importing."}
	}
	local, err := time.Parse(time.RFC3339Nano, asset.LocalDateTime)
	if err != nil {
		return item, sourceChanged()
	}
	// Rebuild wall-clock components in UTC only as a transport for PostgreSQL's
	// timestamp without time zone. Do not convert the source instant to UTC.
	item.CapturedAt = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC).Truncate(time.Microsecond)
	item.SourceCreatedAt, err = time.Parse(time.RFC3339Nano, asset.FileCreatedAt)
	if err != nil {
		return item, sourceChanged()
	}
	item.SourceUpdatedAt, err = time.Parse(time.RFC3339Nano, asset.UpdatedAt)
	if err != nil {
		return item, sourceChanged()
	}
	item.ContentVersion = media.ContentVersion(asset)
	return item, nil
}

func (m *Module) finishImport(ctx context.Context, id string, source immich.Album, items []models.MediaItem) error {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := albumRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ImportStatus == "complete" {
			return nil
		}
		// Lock shared media in source-ID order so overlapping imports cannot deadlock.
		indices := make([]int, len(items))
		for i := range items {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool { return items[indices[i]].SourceID < items[indices[j]].SourceID })
		for _, i := range indices {
			item := &items[i]
			_, err := tx.NewInsert().Model(item).On("CONFLICT (source_id) DO UPDATE").
				Set("checksum = EXCLUDED.checksum").Set("filename = EXCLUDED.filename").Set("kind = EXCLUDED.kind").
				Set("captured_at = EXCLUDED.captured_at").Set("source_created_at = EXCLUDED.source_created_at").Set("source_updated_at = EXCLUDED.source_updated_at").
				Set("offline = EXCLUDED.offline").Set("trashed = EXCLUDED.trashed").Set("width = EXCLUDED.width").Set("height = EXCLUDED.height").Set("duration = EXCLUDED.duration").
				Set("thumbhash = EXCLUDED.thumbhash").Set("live_photo_video_id = EXCLUDED.live_photo_video_id").
				Set("source_stack_id = EXCLUDED.source_stack_id").Set("source_stack_primary_id = EXCLUDED.source_stack_primary_id").Set("source_stack_count = EXCLUDED.source_stack_count").
				Set("exif = EXCLUDED.exif").Set("content_version = EXCLUDED.content_version").
				Returning("id").Exec(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		moments := map[string]models.Moment{}
		for _, item := range items {
			date := item.CapturedAt.Format("2006-01-02")
			entry := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: row.ID, MediaItemID: item.ID}
			moment, ok := moments[date]
			if !ok {
				moment = models.Moment{ID: models.NewUUIDv7(), AlbumID: row.ID, CaptureDate: date, Label: item.CapturedAt.Format("January 2, 2006"), CoverEntryID: entry.ID}
				if _, err := tx.NewInsert().Model(&moment).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
				moments[date] = moment
			}
			entry.MomentID = &moment.ID
			if _, err := tx.NewInsert().Model(&entry).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if source.ThumbnailID != nil && *source.ThumbnailID == item.SourceID && moment.CoverEntryID != entry.ID {
				if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", entry.ID).Where("id = ?", moment.ID).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
				moment.CoverEntryID = entry.ID
				moments[date] = moment
			}
		}
		_, err = tx.NewUpdate().Model((*models.Album)(nil)).Set("description = ?", source.Description).Set("import_status = 'complete'").Set("import_message = ''").
			Set("import_processed = ?", len(items)).Set("import_total = ?", len(items)).Set("import_updated_at = ?", time.Now().UTC()).Where("id = ?", id).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	return transactionError(ctx, err)
}
