package publishing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// videoMediaID resolves the Media Item behind an active video Album Entry of
// this Album. Photos and removed or foreign entries are not found.
func videoMediaID(ctx context.Context, db bun.IDB, albumID, entryID string) (string, error) {
	if _, err := uuid.Parse(albumID); err != nil {
		return "", errcodes.NotFound("Album")
	}
	if _, err := uuid.Parse(entryID); err != nil {
		return "", errcodes.NotFound("Video")
	}
	var mediaID string
	err := db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("item.id").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.id = ? AND entry.album_id = ? AND entry.removed_at IS NULL AND item.kind = 'VIDEO'", entryID, albumID).Scan(ctx, &mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errcodes.NotFound("Video")
	}
	return mediaID, errorstack.CaptureContext(ctx, err)
}

// UpdateVideo sets or clears the Media Item's global video title. The title
// belongs to the Media Item, so every Album showing that video changes.
func (m *Module) UpdateVideo(ctx context.Context, albumID, entryID string, request UpdateVideoRequest) (AlbumDetail, error) {
	title := strings.TrimSpace(request.Title)
	if utf8.RuneCountInString(title) > 200 {
		return AlbumDetail{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"title": "Use 200 characters or fewer."})
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		mediaID, err := videoMediaID(ctx, tx, albumID, entryID)
		if err != nil {
			return err
		}
		var value *string
		if title != "" {
			value = &title
		}
		_, err = tx.NewUpdate().Model((*models.MediaItem)(nil)).Set("video_title = ?", value).Where("id = ?", mediaID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

// RetryChapters requeues extraction for one video after a failure. Playback
// and publication never wait for it.
func (m *Module) RetryChapters(ctx context.Context, albumID, entryID string) (AlbumDetail, error) {
	if m.Chapters == nil {
		return AlbumDetail{}, errorstack.Capture(errors.New("chapter extraction is not configured on the publishing module"))
	}
	mediaID, err := videoMediaID(ctx, m.db, albumID, entryID)
	if err != nil {
		return AlbumDetail{}, err
	}
	if err := m.Chapters.RetryChapters(ctx, mediaID); err != nil {
		return AlbumDetail{}, err
	}
	return m.GetAlbum(ctx, albumID)
}
