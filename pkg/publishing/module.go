// Package publishing owns reviewed Albums and their initial read-only import.
package publishing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	// ImmichURL is the browser-reachable Immich origin used for "Open in
	// Immich" links. Empty hides those links.
	ImmichURL string
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

type albumProjection struct {
	models.Album `bun:"embed:"`
	PhotoCount   int
	VideoCount   int
	StartDate    string
	EndDate      string
	CoverURL     string
}

// albumSummaries keeps list cards and detail headers on the same projection.
// Only configured Moment covers are candidates, in capture-day order.
func albumSummaries(db bun.IDB) *bun.SelectQuery {
	stats := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.album_id, count(*) FILTER (WHERE item.kind = 'IMAGE') AS photo_count, count(*) FILTER (WHERE item.kind = 'VIDEO') AS video_count").
		ColumnExpr("min(item.captured_at)::date AS start_date, max(item.captured_at)::date AS end_date").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.removed_at IS NULL").Group("entry.album_id")
	covers := db.NewSelect().TableExpr("moments AS moment").DistinctOn("moment.album_id").
		ColumnExpr("moment.album_id, '/api/media/entries/' || entry.id || '/thumbnail?v=' || item.content_version AS cover_url").
		Join("JOIN album_entries AS entry ON entry.id = moment.cover_entry_id AND entry.album_id = moment.album_id AND entry.moment_id = moment.id").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.removed_at IS NULL AND NOT item.offline AND NOT item.trashed").
		OrderExpr("moment.album_id, (SELECT min(order_item.captured_at) FROM album_entries AS order_entry JOIN media_items AS order_item ON order_item.id = order_entry.media_item_id WHERE order_entry.moment_id = moment.id AND order_entry.removed_at IS NULL), moment.sort_order, moment.id")
	return db.NewSelect().Model((*models.Album)(nil)).Column("album.*").
		ColumnExpr("coalesce(summary.photo_count, 0) AS photo_count, coalesce(summary.video_count, 0) AS video_count").
		ColumnExpr("coalesce(to_char(summary.start_date, 'YYYY-MM-DD'), '') AS start_date, coalesce(to_char(summary.end_date, 'YYYY-MM-DD'), '') AS end_date").
		ColumnExpr("coalesce(cover.cover_url, '') AS cover_url").
		Join("LEFT JOIN (?) AS summary ON summary.album_id = album.id AND album.import_status = 'complete'", stats).
		Join("LEFT JOIN (?) AS cover ON cover.album_id = album.id AND album.import_status = 'complete'", covers)
}

func projectAlbum(row albumProjection) Album {
	status, message := row.ImportStatus, row.ImportMessage
	if status == "processing" && time.Since(row.ImportUpdatedAt) > time.Minute {
		status = "interrupted"
		message = "Import stopped reporting progress. Memento will recover it automatically after the worker timeout."
	}
	return Album{ID: row.ID.String(), SourceID: row.SourceID, Title: row.Title, Description: row.Description,
		Published: row.PublishedAt != nil, Status: status, Message: message, Processed: row.ImportProcessed, Total: row.ImportTotal,
		PhotoCount: row.PhotoCount, VideoCount: row.VideoCount, StartDate: row.StartDate, EndDate: row.EndDate, CoverURL: row.CoverURL}
}

func (m *Module) ListAlbums(ctx context.Context, search string) ([]Album, error) {
	rows := []albumProjection{}
	if err := albumSummaries(m.db).Where("strpos(lower(album.title), lower(?)) > 0", strings.TrimSpace(search)).
		OrderExpr("summary.start_date DESC NULLS LAST, album.id").Scan(ctx, &rows); err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]Album, 0, len(rows))
	for _, row := range rows {
		result = append(result, projectAlbum(row))
	}
	return result, nil
}

func (m *Module) GetAlbum(ctx context.Context, id string) (AlbumDetail, error) {
	var result AlbumDetail
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		result, err = getAlbum(ctx, tx, id, m.ImmichURL)
		return err
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return result, nil
}

func getAlbum(ctx context.Context, db bun.IDB, id, immichURL string) (AlbumDetail, error) {
	result := AlbumDetail{Moments: []Moment{}}
	if _, err := uuid.Parse(id); err != nil {
		return result, errcodes.NotFound("Album")
	}
	var row albumProjection
	err := albumSummaries(db).Where("album.id = ?", id).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return result, errcodes.NotFound("Album")
	}
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	result.Album = projectAlbum(row)
	if row.ImportStatus != "complete" {
		return result, nil
	}
	var moments []models.Moment
	if err := db.NewSelect().Model(&moments).Where("album_id = ?", id).Scan(ctx); err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	type galleryEntry struct {
		EntryID          models.UUID
		MomentID         models.UUID
		models.MediaItem `bun:"embed:"`
	}
	var entries []galleryEntry
	err = db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("entry.id AS entry_id, entry.moment_id, item.*").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").Where("entry.album_id = ? AND entry.removed_at IS NULL", id).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", entry.id").Scan(ctx, &entries)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	byMoment := map[models.UUID][]Entry{}
	momentDates := map[models.UUID][]string{}
	for _, e := range entries {
		byMoment[e.MomentID] = append(byMoment[e.MomentID], Entry{ID: e.EntryID.String(), MediaID: e.ID.String(), Filename: e.Filename, Kind: e.Kind,
			CapturedAt: e.CapturedAt.Format("2006-01-02T15:04:05.999999999"), Available: !e.Offline && !e.Trashed,
			ThumbnailURL: "/api/media/entries/" + e.EntryID.String() + "/thumbnail?v=" + e.ContentVersion})
		momentDates[e.MomentID] = append(momentDates[e.MomentID], e.CapturedAt.Format("2006-01-02"))
	}
	sort.Slice(moments, func(i, j int) bool {
		left, right := momentDates[moments[i].ID], momentDates[moments[j].ID]
		if len(left) > 0 && len(right) > 0 && left[0] != right[0] {
			return left[0] < right[0]
		}
		if moments[i].SortOrder != moments[j].SortOrder {
			return moments[i].SortOrder < moments[j].SortOrder
		}
		return moments[i].ID.String() < moments[j].ID.String()
	})
	access, err := accessByMoment(ctx, db, id, moments, immichURL)
	if err != nil {
		return result, err
	}
	anchorCounts := map[string]int{}
	for _, moment := range moments {
		anchorCounts[moment.CaptureDate]++
	}
	anchorRanks := map[string]int{}
	for _, moment := range moments {
		dates := momentDates[moment.ID]
		start, end := moment.CaptureDate, moment.CaptureDate
		if len(dates) > 0 {
			start, end = dates[0], dates[len(dates)-1]
		}
		title := ""
		if moment.Title != nil {
			title = *moment.Title
		}
		anchorRanks[moment.CaptureDate]++
		label := title
		if label == "" {
			label = generatedMomentLabel(start, end)
			if anchorCounts[moment.CaptureDate] > 1 {
				label = fmt.Sprintf("%s (%d)", label, anchorRanks[moment.CaptureDate])
			}
		}
		result.Moments = append(result.Moments, Moment{ID: moment.ID.String(), Title: title, Label: label, Date: start, EndDate: end,
			CoverEntryID: moment.CoverEntryID.String(), Entries: byMoment[moment.ID], Access: access[moment.ID]})
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
