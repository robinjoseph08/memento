// Package publishing owns Album import, curation, access and publication.
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
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// EnqueueImport participates in the caller's transaction. It must not execute work inline.
type EnqueueImport func(context.Context, bun.Tx, string) error

// ChapterService is Media's chapter extraction seam. Requests join the import
// transaction so a committed video always has its committed task.
type ChapterService interface {
	RequestChapters(ctx context.Context, tx bun.Tx, mediaItemID models.UUID, checksum string) error
	RetryChapters(ctx context.Context, mediaItemID string) error
}

type Module struct {
	db      *bun.DB
	source  immich.Library
	enqueue EnqueueImport
	// ImmichURL is the browser-reachable Immich origin used for "Open in
	// Immich" links. Empty hides those links.
	ImmichURL string
	// Chapters queues extraction for imported videos. Nil, as in tests that
	// never look at chapters, imports videos without probing them.
	Chapters ChapterService
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
	HasAudience  bool
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
	// An Album is ready to publish once at least one allowing decision exists
	// at any scope; until then publishing would show it to nobody.
	audience := db.NewSelect().TableExpr("album_access_decisions AS decision").ColumnExpr("1").Where("decision.album_id = album.id AND decision.decision = 'allow'").
		UnionAll(db.NewSelect().TableExpr("moment_access_decisions AS decision").ColumnExpr("1").Where("decision.album_id = album.id AND decision.decision = 'allow'")).
		UnionAll(db.NewSelect().TableExpr("entry_access_decisions AS decision").ColumnExpr("1").Where("decision.album_id = album.id AND decision.decision = 'allow'"))
	return db.NewSelect().Model((*models.Album)(nil)).Column("album.*").
		ColumnExpr("coalesce(summary.photo_count, 0) AS photo_count, coalesce(summary.video_count, 0) AS video_count").
		ColumnExpr("coalesce(to_char(summary.start_date, 'YYYY-MM-DD'), '') AS start_date, coalesce(to_char(summary.end_date, 'YYYY-MM-DD'), '') AS end_date").
		ColumnExpr("coalesce(cover.cover_url, '') AS cover_url").
		ColumnExpr("EXISTS (?) AS has_audience", audience).
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
		Published: row.PublishedAt != nil, Ready: row.PublishedAt == nil && row.ImportStatus == "complete" && row.HasAudience,
		Status: status, Message: message, Processed: row.ImportProcessed, Total: row.ImportTotal,
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
	result := AlbumDetail{Moments: []Moment{}, Excluded: []ExcludedEntry{}}
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
		ChapterStatus    string
		ChapterMessage   string
		Chapters         []models.Chapter `bun:"chapters,type:jsonb"`
	}
	var entries []galleryEntry
	err = db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("entry.id AS entry_id, entry.moment_id, item.*").
		ColumnExpr("coalesce(chapter_result.status, '') AS chapter_status, coalesce(chapter_result.message, '') AS chapter_message, coalesce(chapter_result.chapters, '[]'::jsonb) AS chapters").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Join("LEFT JOIN media_chapter_results AS chapter_result ON chapter_result.media_item_id = item.id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL", id).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", entry.id").Scan(ctx, &entries)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	byMoment := map[models.UUID][]Entry{}
	momentDates := map[models.UUID][]string{}
	for _, e := range entries {
		entry := Entry{ID: e.EntryID.String(), MediaID: e.ID.String(), Filename: e.Filename, Kind: e.Kind,
			CapturedAt: e.CapturedAt.Format("2006-01-02T15:04:05.999999999"), Available: !e.Offline && !e.Trashed,
			ThumbnailURL: "/api/media/entries/" + e.EntryID.String() + "/thumbnail?v=" + e.ContentVersion, Chapters: []Chapter{}}
		if e.Kind == "VIDEO" {
			if e.VideoTitle != nil {
				entry.Title = *e.VideoTitle
			}
			if entry.Available {
				entry.PlaybackURL = "/api/media/entries/" + e.EntryID.String() + "/playback?v=" + e.ContentVersion
			}
			entry.ChapterStatus = media.PublicChapterStatus(e.ChapterStatus)
			entry.ChapterMessage = e.ChapterMessage
			entry.Chapters = projectChapters(e.Chapters)
		}
		byMoment[e.MomentID] = append(byMoment[e.MomentID], entry)
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
	labels := momentLabels(moments, momentDates)
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
		result.Moments = append(result.Moments, Moment{ID: moment.ID.String(), Title: title, Label: labels[moment.ID], Date: start, EndDate: end,
			CoverEntryID: moment.CoverEntryID.String(), Entries: byMoment[moment.ID], Access: access[moment.ID]})
	}
	if err := attachAccess(ctx, db, &result); err != nil {
		return result, err
	}
	type excludedRow struct {
		EntryID          models.UUID
		ExcludedAt       time.Time
		models.MediaItem `bun:"embed:"`
	}
	var excluded []excludedRow
	err = db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("entry.id AS entry_id, entry.excluded_at, item.*").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.album_id = ? AND entry.excluded_at IS NOT NULL", id).
		OrderExpr("item.captured_at, item.source_id COLLATE \"C\", entry.id").Scan(ctx, &excluded)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	for _, e := range excluded {
		result.Excluded = append(result.Excluded, ExcludedEntry{ID: e.EntryID.String(), Filename: e.Filename, Kind: e.Kind,
			CapturedAt: e.CapturedAt.Format("2006-01-02T15:04:05.999999999"), ThumbnailURL: SourceAssetThumbnailURL(e.SourceID),
			Available: !e.Offline && !e.Trashed, ExcludedAt: e.ExcludedAt.UTC().Format(time.RFC3339)})
	}
	return result, nil
}

// momentLabels names Moments in display order: the Curator's title, or a
// generated date range. Only untitled Moments share a generated label, so
// only they are numbered.
func momentLabels(moments []models.Moment, momentDates map[models.UUID][]string) map[models.UUID]string {
	anchorCounts := map[string]int{}
	for _, moment := range moments {
		if moment.Title == nil {
			anchorCounts[moment.CaptureDate]++
		}
	}
	anchorRanks := map[string]int{}
	labels := make(map[models.UUID]string, len(moments))
	for _, moment := range moments {
		dates := momentDates[moment.ID]
		start, end := moment.CaptureDate, moment.CaptureDate
		if len(dates) > 0 {
			start, end = dates[0], dates[len(dates)-1]
		}
		if moment.Title != nil && *moment.Title != "" {
			labels[moment.ID] = *moment.Title
			continue
		}
		anchorRanks[moment.CaptureDate]++
		label := generatedMomentLabel(start, end)
		if anchorCounts[moment.CaptureDate] > 1 {
			label = fmt.Sprintf("%s (%d)", label, anchorRanks[moment.CaptureDate])
		}
		labels[moment.ID] = label
	}
	return labels
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
