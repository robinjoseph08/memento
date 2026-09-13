package publishing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func countLabel(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

func (m *Module) publicationReview(ctx context.Context, db bun.IDB, album models.Album) (PublicationReview, error) {
	result := PublicationReview{Title: album.Title, Audience: []PublicationAudience{}, Blockers: []string{}, Warnings: []string{}}
	if strings.TrimSpace(album.Title) == "" {
		result.Blockers = append(result.Blockers, "Add an album title.")
	}
	if album.ImportStatus != "complete" {
		result.Blockers = append(result.Blockers, "Finish importing the Album before publishing.")
	}
	var stats struct {
		Photos      int
		Videos      int
		Moments     int
		Unassigned  int
		Unavailable int
	}
	err := db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("count(*) FILTER (WHERE item.kind = 'IMAGE') AS photos, count(*) FILTER (WHERE item.kind = 'VIDEO') AS videos").
		ColumnExpr("count(DISTINCT entry.moment_id) AS moments").
		ColumnExpr("count(*) FILTER (WHERE entry.moment_id IS NULL) AS unassigned, count(*) FILTER (WHERE item.offline OR item.trashed) AS unavailable").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").Where("entry.album_id = ? AND entry.removed_at IS NULL", album.ID).Scan(ctx, &stats)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	result.PhotoCount, result.VideoCount, result.MomentCount = stats.Photos, stats.Videos, stats.Moments
	if stats.Photos+stats.Videos == 0 {
		result.Blockers = append(result.Blockers, "Import at least one photo or video.")
	}
	if stats.Unassigned > 0 {
		result.Blockers = append(result.Blockers, "Assign every item to a Moment.")
	}
	if stats.Unavailable > 0 {
		result.Warnings = append(result.Warnings, "Some media is unavailable in Immich. Viewers will see an unavailable tile.")
	}
	people, err := accessPeople(ctx, db)
	if err != nil {
		return result, err
	}
	structure, err := m.loadStructure(ctx, db, album.ID.String(), false)
	if err != nil {
		return result, err
	}
	// The token covers what publication exposes: the title, the blockers, and
	// the membership and rules that decide the audience. Names, avatars, and
	// warning counts may change without another review.
	data, err := json.Marshal(struct {
		AlbumID   string        `json:"album_id"`
		Title     string        `json:"title"`
		Blockers  []string      `json:"blockers"`
		Structure reviewedFacts `json:"structure"`
	}{album.ID.String(), album.Title, result.Blockers, structure.reviewed()})
	if err != nil {
		return result, errorstack.Capture(err)
	}
	digest := sha256.Sum256(data)
	result.ReviewToken = hex.EncodeToString(digest[:])
	for _, person := range people {
		accessible := len(visibleEntries(structure.facts(), person.ID.String()))
		if accessible == 0 {
			continue
		}
		audience := PublicationAudience{PersonID: person.ID.String(), DisplayName: person.DisplayName, AccessibleCount: accessible}
		if person.AvatarFaceID != nil {
			audience.AvatarURL = media.AvatarURL(person.ID.String(), *person.AvatarFaceID, person.AvatarVersion)
		}
		result.Audience = append(result.Audience, audience)
	}
	// Suggestions are Moment-level: a linked Person detected in a Moment they
	// cannot see and have no rule for. They never block publication.
	var suggestions int
	err = db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("count(DISTINCT (entry.moment_id, link.person_id))").
		Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("JOIN immich_face_links AS link ON link.source_id = face.source_face_id AND link.person_id IS NOT NULL AND NOT link.ignored").
		Join("JOIN persons AS person ON person.id = link.person_id AND person.deactivated_at IS NULL AND NOT person.is_curator").
		Join("LEFT JOIN moment_access_decisions AS moment_access ON moment_access.moment_id = entry.moment_id AND moment_access.person_id = link.person_id").
		Join("LEFT JOIN album_access_decisions AS album_access ON album_access.album_id = entry.album_id AND album_access.person_id = link.person_id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL AND entry.moment_id IS NOT NULL AND moment_access.person_id IS NULL AND album_access.person_id IS NULL", album.ID).
		Scan(ctx, &suggestions)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	if suggestions > 0 {
		result.Warnings = append(result.Warnings, countLabel(suggestions, "access suggestion", "access suggestions")+" still waiting. Optional to review.")
	}
	var unlinked int
	err = db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("count(DISTINCT face.source_face_id)").
		Join("JOIN media_face_associations AS face ON face.media_item_id = entry.media_item_id").
		Join("LEFT JOIN immich_face_links AS link ON link.source_id = face.source_face_id").
		Where("entry.album_id = ? AND entry.removed_at IS NULL AND link.person_id IS NULL AND NOT coalesce(link.ignored,false)", album.ID).Scan(ctx, &unlinked)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	if unlinked > 0 {
		result.Warnings = append(result.Warnings, countLabel(unlinked, "unlinked face", "unlinked faces")+". Optional to link.")
	}
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
		// The binder already requires a title; module callers get the same guard.
		if request.Title == "" || request.Title != album.Title {
			return structureField("title", deleteTitleMessage)
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
