// Package publishing owns reviewed Albums and their initial read-only import.
package publishing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// EnqueueImport participates in the caller's transaction. It must not execute work inline.
type EnqueueImport func(context.Context, bun.Tx, string) error

type Module struct {
	db      *bun.DB
	source  immich.Library
	enqueue EnqueueImport
}

func New(db *bun.DB, source immich.Library, enqueue EnqueueImport) *Module {
	return &Module{db: db, source: source, enqueue: enqueue}
}

func transactionError(ctx context.Context, err error) error {
	if _, expected := errors.AsType[*errcodes.Error](err); expected {
		return err
	}
	return errorstack.CaptureContext(ctx, err)
}

func albumRow(ctx context.Context, db bun.IDB, id string, lock bool) (models.Album, error) {
	var row models.Album
	if _, err := uuid.Parse(id); err != nil {
		return row, errcodes.NotFound("Album")
	}
	q := db.NewSelect().Model(&row).Where("album.id = ?", id)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return row, errcodes.NotFound("Album")
	}
	return row, errorstack.CaptureContext(ctx, err)
}

func projectAlbum(row models.Album) Album {
	status, message := row.ImportStatus, row.ImportMessage
	if status == "processing" && time.Since(row.ImportUpdatedAt) > time.Minute {
		status = "interrupted"
		message = "Import stopped reporting progress. Memento will recover it automatically after the worker timeout."
	}
	return Album{ID: row.ID.String(), SourceID: row.SourceID, Title: row.Title, Description: row.Description,
		Published: row.PublishedAt != nil, Status: status, Message: message, Processed: row.ImportProcessed, Total: row.ImportTotal}
}

func (m *Module) ListAlbums(ctx context.Context) ([]Album, error) {
	rows := []models.Album{}
	if err := m.db.NewSelect().Model(&rows).OrderExpr("created_at DESC, id").Scan(ctx); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]Album, 0, len(rows))
	for _, row := range rows {
		result = append(result, projectAlbum(row))
	}
	return result, nil
}

func (m *Module) GetAlbum(ctx context.Context, id string) (AlbumDetail, error) {
	result := AlbumDetail{Moments: []Moment{}}
	row, err := albumRow(ctx, m.db, id, false)
	if err != nil {
		return result, err
	}
	result.Album = projectAlbum(row)
	if row.ImportStatus != "complete" {
		return result, nil
	}
	var moments []models.Moment
	if err := m.db.NewSelect().Model(&moments).Where("album_id = ?", id).Order("capture_date", "id").Scan(ctx); err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	type galleryEntry struct {
		EntryID          models.UUID
		MomentID         models.UUID
		models.MediaItem `bun:"embed:"`
	}
	var entries []galleryEntry
	err = m.db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("entry.id AS entry_id, entry.moment_id, item.*").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").Where("entry.album_id = ? AND entry.removed_at IS NULL", id).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", entry.id").Scan(ctx, &entries)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	byMoment := map[models.UUID][]Entry{}
	for _, e := range entries {
		byMoment[e.MomentID] = append(byMoment[e.MomentID], Entry{ID: e.EntryID.String(), MediaID: e.ID.String(), Filename: e.Filename, Kind: e.Kind,
			CapturedAt: e.CapturedAt.Format("2006-01-02T15:04:05.999999999"), Available: !e.Offline && !e.Trashed,
			ThumbnailURL: "/api/media/entries/" + e.EntryID.String() + "/thumbnail?v=" + e.ContentVersion})
	}
	for _, moment := range moments {
		result.Moments = append(result.Moments, Moment{ID: moment.ID.String(), Label: moment.Label, Date: moment.CaptureDate, CoverEntryID: moment.CoverEntryID.String(), Entries: byMoment[moment.ID]})
	}
	return result, nil
}

func (m *Module) UpdateAlbum(ctx context.Context, id string, request UpdateAlbumRequest) (AlbumDetail, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" || utf8.RuneCountInString(title) > 200 {
		return AlbumDetail{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"title": "Enter an album title of 1 to 200 characters."})
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := albumRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.ImportStatus != "complete" {
			return &errcodes.Error{HTTPCode: 409, Code: "import_incomplete", Message: "Wait for the import to finish before editing the Album."}
		}
		_, err = tx.NewUpdate().Model((*models.Album)(nil)).Set("title = ?", title).Where("id = ?", id).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, id)
}
