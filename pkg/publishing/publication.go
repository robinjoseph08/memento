package publishing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func (m *Module) publicationReview(ctx context.Context, db bun.IDB, album models.Album) (PublicationReview, error) {
	result := PublicationReview{Title: album.Title, Audience: []PublicationAudience{}, Blockers: []string{}, Warnings: []string{}}
	if strings.TrimSpace(album.Title) == "" {
		result.Blockers = append(result.Blockers, "Enter an Album title.")
	}
	if album.ImportStatus != "complete" {
		result.Blockers = append(result.Blockers, "Finish importing the Album before publishing.")
	}
	var stats struct {
		Photos      int
		Videos      int
		Unassigned  int
		Unavailable int
	}
	err := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("count(*) FILTER (WHERE item.kind = 'IMAGE') AS photos, count(*) FILTER (WHERE item.kind = 'VIDEO') AS videos").
		ColumnExpr("count(*) FILTER (WHERE entry.moment_id IS NULL) AS unassigned, count(*) FILTER (WHERE item.offline OR item.trashed) AS unavailable").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").Where("entry.album_id = ? AND entry.removed_at IS NULL", album.ID).Scan(ctx, &stats)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	result.PhotoCount, result.VideoCount = stats.Photos, stats.Videos
	if stats.Photos+stats.Videos == 0 {
		result.Blockers = append(result.Blockers, "Add at least one photo or video.")
	}
	if stats.Unassigned > 0 {
		result.Blockers = append(result.Blockers, "Assign every item to a Moment.")
	}
	if stats.Unavailable > 0 {
		result.Warnings = append(result.Warnings, "Some media is unavailable in Immich. Viewers will see an unavailable tile.")
	}
	var people []models.Person
	if err := db.NewSelect().Model(&people).Where("person.deactivated_at IS NULL AND NOT person.is_curator").OrderExpr("lower(person.display_name), person.id").Scan(ctx); err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	for _, person := range people {
		var counts struct {
			Photos int
			Videos int
		}
		err := viewerEntries(db, viewerContext{personID: person.ID.String(), preview: true}).
			ColumnExpr("count(*) FILTER (WHERE item.kind = 'IMAGE') AS photos, count(*) FILTER (WHERE item.kind = 'VIDEO') AS videos").
			Where("entry.album_id = ?", album.ID).Scan(ctx, &counts)
		if err != nil {
			return result, errorstack.CaptureContext(ctx, err)
		}
		if counts.Photos+counts.Videos > 0 {
			result.Audience = append(result.Audience, PublicationAudience{PersonID: person.ID.String(), DisplayName: person.DisplayName, PhotoCount: counts.Photos, VideoCount: counts.Videos})
		}
	}
	if len(result.Audience) == 0 {
		result.Warnings = append(result.Warnings, "No ordinary Person has access yet.")
	}
	unlinked, err := db.NewSelect().TableExpr("album_entries AS entry").Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("LEFT JOIN immich_face_links AS link ON link.source_id = face.source_face_id").Where("entry.album_id = ? AND entry.removed_at IS NULL AND link.person_id IS NULL AND NOT coalesce(link.ignored,false)", album.ID).Exists(ctx)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	if unlinked {
		result.Warnings = append(result.Warnings, "Some detected faces are not linked to a Person. They do not grant access.")
	}
	structure, err := m.loadStructure(ctx, db, album.ID.String(), false)
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(struct {
		AlbumID     string            `json:"album_id"`
		Description string            `json:"description"`
		Review      PublicationReview `json:"review"`
		Structure   reviewedFacts     `json:"structure"`
	}{album.ID.String(), album.Description, result, structure.reviewed()})
	if err != nil {
		return result, errorstack.Capture(err)
	}
	digest := sha256.Sum256(data)
	result.ReviewToken = hex.EncodeToString(digest[:])
	return result, nil
}

// ReviewPublication reports blockers and the current ordinary audience. It does
// not publish, enqueue work, or record a notification baseline.
func (m *Module) ReviewPublication(ctx context.Context, albumID string) (PublicationReview, error) {
	var result PublicationReview
	err := m.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		album, err := albumRow(ctx, tx, albumID, false)
		if err != nil {
			return err
		}
		result, err = m.publicationReview(ctx, tx, album)
		return err
	})
	return result, transactionError(ctx, err)
}

// PublishAlbum confirms the reviewed audience and commits publication alone.
func (m *Module) PublishAlbum(ctx context.Context, albumID string, request PublishRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		album, err := albumRow(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		review, err := m.publicationReview(ctx, tx, album)
		if err != nil {
			return err
		}
		if len(review.Blockers) > 0 {
			return errcodes.ValidationError(strings.Join(review.Blockers, " "))
		}
		if request.ReviewToken == "" || request.ReviewToken != review.ReviewToken {
			return &errcodes.Error{HTTPCode: 409, Code: "publication_changed", Message: "The Album or its audience changed. Review it again before publishing."}
		}
		_, err = tx.NewUpdate().Model((*models.Album)(nil)).Set("published_at = coalesce(published_at, ?)", time.Now().UTC()).Where("id = ?", albumID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

// DeleteAlbum permanently removes this Album's curation, never Immich media.
// Shared Media Items remain intact, including metadata owned by later features.
func (m *Module) DeleteAlbum(ctx context.Context, albumID string, request DeleteAlbumRequest) error {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		album, err := albumRow(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		if request.Title != album.Title || request.Title == "" {
			return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"title": "Type the Album title exactly to confirm deletion."})
		}
		_, err = tx.NewDelete().Model((*models.Album)(nil)).Where("id = ?", albumID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	return transactionError(ctx, err)
}

// UnpublishAlbum retains all curation and history while hiding ordinary access.
func (m *Module) UnpublishAlbum(ctx context.Context, albumID string) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*models.Album)(nil)).Set("published_at = NULL").Where("id = ?", albumID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
