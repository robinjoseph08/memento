package publishing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/uptrace/bun"
)

// presentationTitle is the Curator's video title when set, otherwise the
// original filename without its extension.
func presentationTitle(filename string, videoTitle *string) string {
	if videoTitle != nil && *videoTitle != "" {
		return *videoTitle
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// projectChapters copies stored chapters into the public shape, never nil.
func projectChapters(stored []models.Chapter) []Chapter {
	chapters := make([]Chapter, 0, len(stored))
	for _, chapter := range stored {
		chapters = append(chapters, Chapter{Title: chapter.Title, Start: chapter.Start, End: chapter.End})
	}
	return chapters
}

// viewerContext can only be constructed after checking the actor and selected
// Person. Preview ignores publication, but never borrows the actor's bypass.
type viewerContext struct {
	personID string
	preview  bool
	curator  bool
}

func resolveViewer(ctx context.Context, db bun.IDB, actorID, previewPersonID string) (viewerContext, error) {
	var result viewerContext
	if _, err := uuid.Parse(actorID); err != nil {
		return result, errcodes.NotFound("Album")
	}
	var actor models.Person
	err := db.NewSelect().Model(&actor).Where("person.id = ? AND person.deactivated_at IS NULL", actorID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return result, errcodes.NotFound("Album")
	}
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	result = viewerContext{personID: actorID, curator: actor.IsCurator}
	if previewPersonID == "" {
		return result, nil
	}
	if !actor.IsCurator {
		return viewerContext{}, errcodes.NotFound("Album")
	}
	if _, err := uuid.Parse(previewPersonID); err != nil {
		return viewerContext{}, errcodes.NotFound("Album")
	}
	exists, err := db.NewSelect().Model((*models.Person)(nil)).Where("person.id = ? AND person.deactivated_at IS NULL", previewPersonID).Exists(ctx)
	if err != nil {
		return viewerContext{}, errorstack.CaptureContext(ctx, err)
	}
	if !exists {
		return viewerContext{}, errcodes.NotFound("Album")
	}
	return viewerContext{personID: previewPersonID, preview: true}, nil
}

// viewerEntries is shared by Album summaries, galleries, covers and media
// authorization. Membership always belongs to this Album Entry, not its media.
func viewerEntries(db bun.IDB, viewer viewerContext) *bun.SelectQuery {
	q := db.NewSelect().TableExpr("album_entries AS entry").
		Join("JOIN albums AS album ON album.id = entry.album_id").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.removed_at IS NULL AND entry.moment_id IS NOT NULL AND album.import_status = 'complete'")
	if !viewer.preview && !viewer.curator {
		q = q.Where("album.published_at IS NOT NULL")
	}
	if !viewer.curator {
		q = q.Join("LEFT JOIN moment_access_decisions AS moment_access ON moment_access.moment_id = entry.moment_id AND moment_access.person_id = ?", viewer.personID).
			Join("LEFT JOIN album_access_decisions AS album_access ON album_access.album_id = entry.album_id AND album_access.person_id = ?", viewer.personID).
			Join("LEFT JOIN entry_access_decisions AS entry_access ON entry_access.entry_id = entry.id AND entry_access.person_id = ?", viewer.personID).
			Where("coalesce(entry_access.decision, moment_access.decision, album_access.decision, 'deny') = 'allow'")
	}
	return q
}

// galleryEntries deduplicates library media only after checking each Entry's access.
func galleryEntries(db bun.IDB, viewer viewerContext, albumID string) *bun.SelectQuery {
	if albumID != "" {
		return viewerEntries(db, viewer).Where("entry.album_id = ?", albumID)
	}
	visible := viewerEntries(db, viewer).
		ColumnExpr("DISTINCT ON (entry.media_item_id) entry.id, entry.media_item_id").
		OrderExpr("entry.media_item_id, entry.id")
	return db.NewSelect().With("visible_entries", visible).
		TableExpr("visible_entries AS entry").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id")
}

func galleryOrder(albumID string) string {
	if albumID == "" {
		return "item.captured_at DESC, entry.id DESC"
	}
	return "item.captured_at, entry.id"
}

func galleryDays(ctx context.Context, db bun.IDB, viewer viewerContext, albumID string) ([]ViewerDay, error) {
	days := []ViewerDay{}
	order := "date"
	if albumID == "" {
		order = "date DESC"
	}
	err := galleryEntries(db, viewer, albumID).ColumnExpr("to_char(item.captured_at, 'YYYY-MM-DD') AS date").
		ColumnExpr("count(*) FILTER (WHERE item.kind = 'IMAGE') AS photo_count, count(*) FILTER (WHERE item.kind = 'VIDEO') AS video_count").
		ColumnExpr(ratioColumn("IMAGE", "photo_ratios", albumID)).
		ColumnExpr(ratioColumn("VIDEO", "video_ratios", albumID)).
		GroupExpr("to_char(item.captured_at, 'YYYY-MM-DD')").OrderExpr(order).Scan(ctx, &days)
	return days, errorstack.CaptureContext(ctx, err)
}

// ratioColumn aggregates one kind's width-to-height ratios in gallery order,
// truncated to three decimals like the browser, with 3:2 for media that has
// no dimensions.
func ratioColumn(kind, name, albumID string) string {
	return "coalesce(array_agg(CASE WHEN coalesce(item.width, 0) > 0 AND coalesce(item.height, 0) > 0 THEN trunc((item.width::float8 / item.height::float8) * 1000) / 1000 ELSE 1.5 END ORDER BY " +
		galleryOrder(albumID) + ") FILTER (WHERE item.kind = '" + kind + "'), ARRAY[]::float8[]) AS " + name
}

func (v viewerContext) thumbnailURL(entryID, version string) string {
	return v.mediaURL(entryID, "thumbnail", version)
}

func (v viewerContext) previewURL(entryID, version string) string {
	return v.mediaURL(entryID, "preview", version)
}

// downloadURL exists only for a Person's own media; preview has no download.
func (v viewerContext) downloadURL(entryID, version string) string {
	if v.preview {
		return ""
	}
	return v.mediaURL(entryID, "original", version)
}

// playbackURL is the ranged stream for a video, in preview as well.
func (v viewerContext) playbackURL(entryID, kind, version string) string {
	if kind != "VIDEO" {
		return ""
	}
	return v.mediaURL(entryID, "playback", version)
}

func (v viewerContext) mediaURL(entryID, variant, version string) string {
	mode := "viewer"
	if v.preview {
		mode = "preview"
	}
	return "/api/media/" + mode + "/" + v.personID + "/entries/" + entryID + "/" + variant + "?v=" + url.QueryEscape(version)
}

// viewAlbum projects one Album for a viewer. The day breakdown, with every
// photo ratio, is only gathered for the Album page; the list leaves Days
// empty.
func viewAlbum(ctx context.Context, db bun.IDB, viewer viewerContext, id string, days bool) (ViewerAlbum, error) {
	result := ViewerAlbum{Days: []ViewerDay{}}
	if _, err := uuid.Parse(id); err != nil {
		return result, errcodes.NotFound("Album")
	}
	err := viewerEntries(db, viewer).
		ColumnExpr("album.id, album.title, album.description").
		ColumnExpr("count(*) FILTER (WHERE item.kind = 'IMAGE') AS photo_count, count(*) FILTER (WHERE item.kind = 'VIDEO') AS video_count").
		ColumnExpr("to_char(min(item.captured_at), 'YYYY-MM-DD') AS start_date, to_char(max(item.captured_at), 'YYYY-MM-DD') AS end_date").
		Where("album.id = ?", id).Group("album.id").Scan(ctx, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return result, errcodes.NotFound("Album")
	}
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	var cover struct {
		ID      string
		Version string
	}
	err = viewerEntries(db, viewer).ColumnExpr("entry.id, item.content_version AS version").
		Join("JOIN moments AS moment ON moment.id = entry.moment_id AND moment.cover_entry_id = entry.id").
		Where("entry.album_id = ? AND NOT item.offline AND NOT item.trashed", id).
		OrderExpr("moment.cover_position ASC NULLS LAST, "+momentCaptureOrder).Limit(1).Scan(ctx, &cover)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, errorstack.CaptureContext(ctx, err)
	}
	if err == nil {
		result.CoverURL = viewer.thumbnailURL(cover.ID, cover.Version)
		result.CoverPreviewURL = viewer.previewURL(cover.ID, cover.Version)
	}
	if !days {
		return result, nil
	}
	result.Days, err = galleryDays(ctx, db, viewer, id)
	return result, err
}

type entryCursor struct {
	CapturedAt string `json:"at"`
	ID         string `json:"id"`
}

func parseEntryCursor(value string) (entryCursor, error) {
	var cursor entryCursor
	invalid := func() (entryCursor, error) {
		return cursor, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"cursor": "Reload the gallery to continue."})
	}
	if len(value) > 512 {
		return invalid()
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return invalid()
	}
	if err := json.Unmarshal(data, &cursor); err != nil {
		return invalid()
	}
	if _, err := time.Parse("2006-01-02T15:04:05.999999999", cursor.CapturedAt); err != nil {
		return invalid()
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return invalid()
	}
	return cursor, nil
}

// entryPageSize is the most entries one gallery page carries.
const entryPageSize = 500

func parseEntryDay(field, value string) error {
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{field: "Reload the gallery to continue."})
	}
	return nil
}

// ViewEntries returns separate cursor-paginated galleries without Curator metadata.
func (m *Module) ViewEntries(ctx context.Context, actorID, previewPersonID, albumID, kind string, page EntryPageRequest) (ViewerPage, error) {
	if _, err := uuid.Parse(albumID); err != nil {
		return ViewerPage{Entries: []ViewerEntry{}}, errcodes.NotFound("Album")
	}
	return m.viewEntries(ctx, actorID, previewPersonID, albumID, kind, page)
}

// ViewLibraryEntries returns newest-first media with one accessible Entry per item.
func (m *Module) ViewLibraryEntries(ctx context.Context, actorID, kind string, page EntryPageRequest) (ViewerPage, error) {
	return m.viewEntries(ctx, actorID, "", "", kind, page)
}

func (m *Module) viewEntries(ctx context.Context, actorID, previewPersonID, albumID, kind string, page EntryPageRequest) (ViewerPage, error) {
	result := ViewerPage{Entries: []ViewerEntry{}}
	if kind != "IMAGE" && kind != "VIDEO" {
		return result, errcodes.NotFound("Gallery")
	}
	var cursor entryCursor
	if page.Cursor != "" {
		var err error
		cursor, err = parseEntryCursor(page.Cursor)
		if err != nil {
			return result, err
		}
	}
	if page.From != "" {
		if err := parseEntryDay("from", page.From); err != nil {
			return result, err
		}
	}
	if page.To != "" {
		if err := parseEntryDay("to", page.To); err != nil {
			return result, err
		}
	}
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		viewer, err := resolveViewer(ctx, tx, actorID, previewPersonID)
		if err != nil {
			return err
		}
		if albumID != "" {
			exists, err := viewerEntries(tx, viewer).Where("entry.album_id = ?", albumID).Exists(ctx)
			if err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			if !exists {
				return errcodes.NotFound("Album")
			}
		}
		type galleryRow struct {
			ID            string
			Kind          string
			Filename      string
			VideoTitle    *string
			CapturedAt    time.Time
			Available     bool
			Version       string
			Width         int
			Height        int
			ChapterStatus string
			Chapters      []models.Chapter `bun:"chapters,type:jsonb"`
		}
		rows := []galleryRow{}
		query := galleryEntries(tx, viewer, albumID).ColumnExpr("entry.id, item.kind, item.filename, item.video_title, item.captured_at, NOT item.offline AND NOT item.trashed AS available, item.content_version AS version, coalesce(item.width,0) AS width, coalesce(item.height,0) AS height").
			ColumnExpr("coalesce(chapter_result.status, '') AS chapter_status, coalesce(chapter_result.chapters, '[]'::jsonb) AS chapters").
			Join("LEFT JOIN media_chapter_results AS chapter_result ON chapter_result.media_item_id = item.id").
			Where("item.kind = ?", kind).OrderExpr(galleryOrder(albumID)).Limit(entryPageSize + 1)
		if page.Cursor != "" {
			comparison := ">"
			if albumID == "" {
				comparison = "<"
			}
			query = query.Where("(item.captured_at, entry.id) "+comparison+" (?::timestamp, ?::uuid)", cursor.CapturedAt, cursor.ID)
		}
		if page.From != "" {
			query = query.Where("item.captured_at >= ?::timestamp", page.From)
		}
		if page.To != "" {
			query = query.Where("item.captured_at < ?::timestamp", page.To)
		}
		if err := query.Scan(ctx, &rows); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		more := len(rows) > entryPageSize
		if more {
			rows = rows[:entryPageSize]
		}
		for _, row := range rows {
			thumbnail, preview, download, playback := "", "", "", ""
			if row.Available {
				thumbnail = viewer.thumbnailURL(row.ID, row.Version)
				preview = viewer.previewURL(row.ID, row.Version)
				download = viewer.downloadURL(row.ID, row.Version)
				playback = viewer.playbackURL(row.ID, row.Kind, row.Version)
			}
			entry := ViewerEntry{ID: row.ID, Kind: row.Kind, Title: presentationTitle(row.Filename, row.VideoTitle), CapturedAt: row.CapturedAt.Format("2006-01-02T15:04:05.999999999"), Available: row.Available,
				ThumbnailURL: thumbnail, PreviewURL: preview, DownloadURL: download, PlaybackURL: playback, Width: row.Width, Height: row.Height, Chapters: []Chapter{}}
			if row.Kind == "VIDEO" {
				entry.ChapterStatus = media.PublicChapterStatus(row.ChapterStatus)
				entry.Chapters = projectChapters(row.Chapters)
			}
			result.Entries = append(result.Entries, entry)
		}
		if more {
			last := result.Entries[len(result.Entries)-1]
			data, err := json.Marshal(entryCursor{CapturedAt: last.CapturedAt, ID: last.ID})
			if err != nil {
				return errorstack.Capture(err)
			}
			result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
		}
		return nil
	})
	return result, transactionError(ctx, err)
}

// AuthorizeViewerEntry is the Media boundary. Every uncached thumbnail request
// checks current membership and access before resolving an Immich variant.
func (m *Module) AuthorizeViewerEntry(ctx context.Context, actorID, previewPersonID, entryID string) error {
	if _, err := uuid.Parse(entryID); err != nil {
		return errcodes.NotFound("Thumbnail")
	}
	viewer, err := resolveViewer(ctx, m.db, actorID, previewPersonID)
	if err != nil {
		return err
	}
	allowed, err := viewerEntries(m.db, viewer).Where("entry.id = ?", entryID).Exists(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if !allowed {
		return errcodes.NotFound("Thumbnail")
	}
	return nil
}

// ViewLibrary summarizes accessible media across Albums in newest-first order.
func (m *Module) ViewLibrary(ctx context.Context, actorID string) (ViewerLibrary, error) {
	result := ViewerLibrary{Days: []ViewerDay{}}
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		viewer, err := resolveViewer(ctx, tx, actorID, "")
		if err != nil {
			return err
		}
		result.Days, err = galleryDays(ctx, tx, viewer, "")
		if err != nil {
			return err
		}
		for _, day := range result.Days {
			result.PhotoCount += day.PhotoCount
			result.VideoCount += day.VideoCount
		}
		return nil
	})
	return result, transactionError(ctx, err)
}

func (m *Module) ViewAlbums(ctx context.Context, actorID string) ([]ViewerAlbum, error) {
	result := []ViewerAlbum{}
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		viewer, err := resolveViewer(ctx, tx, actorID, "")
		if err != nil {
			return err
		}
		ids := []string{}
		err = viewerEntries(tx, viewer).ColumnExpr("entry.album_id").Group("entry.album_id").OrderExpr("min(item.captured_at) DESC, entry.album_id").Scan(ctx, &ids)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		for _, id := range ids {
			album, err := viewAlbum(ctx, tx, viewer, id, false)
			if err != nil {
				return err
			}
			result = append(result, album)
		}
		return nil
	})
	return result, transactionError(ctx, err)
}

// ViewAlbum uses one snapshot for identity, effective counts and configured cover.
func (m *Module) ViewAlbum(ctx context.Context, actorID, previewPersonID, albumID string) (ViewerAlbum, error) {
	var result ViewerAlbum
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		viewer, err := resolveViewer(ctx, tx, actorID, previewPersonID)
		if err != nil {
			return err
		}
		result, err = viewAlbum(ctx, tx, viewer, albumID, true)
		return err
	})
	return result, transactionError(ctx, err)
}

// VisibleEntries lists every Album Entry the Person can view as an ordinary
// viewer: published Albums and allowing Access Decisions only. A Curator's
// administrative bypass is deliberately excluded so announcement baselines never
// treat unpublished or otherwise viewer-ineligible content as announced.
func (m *Module) VisibleEntries(ctx context.Context, db bun.IDB, personID string) ([]notifications.VisibleEntry, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return nil, errcodes.NotFound("Person")
	}
	type row struct {
		AlbumID    string
		AlbumTitle string
		EntryID    string
		Kind       string
	}
	rows := []row{}
	err := viewerEntries(db, viewerContext{personID: personID}).
		ColumnExpr("entry.album_id, album.title AS album_title, entry.id AS entry_id, item.kind").
		OrderExpr("entry.album_id, entry.id").Scan(ctx, &rows)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]notifications.VisibleEntry, 0, len(rows))
	for _, r := range rows {
		result = append(result, notifications.VisibleEntry{AlbumID: r.AlbumID, AlbumTitle: r.AlbumTitle, EntryID: r.EntryID, Kind: r.Kind})
	}
	return result, nil
}
