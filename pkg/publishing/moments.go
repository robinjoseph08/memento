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
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func generatedMomentLabel(start, end string) string {
	first, err := time.Parse("2006-01-02", start)
	if err != nil {
		return start
	}
	label := first.Format("January 2, 2006")
	if end == "" || end == start {
		return label
	}
	last, err := time.Parse("2006-01-02", end)
	if err != nil {
		return label
	}
	return label + " to " + last.Format("January 2, 2006")
}

func momentRow(ctx context.Context, db bun.IDB, albumID, momentID string, lock bool) (models.Moment, error) {
	var row models.Moment
	if _, err := uuid.Parse(albumID); err != nil {
		return row, errcodes.NotFound("Album")
	}
	if _, err := uuid.Parse(momentID); err != nil {
		return row, errcodes.NotFound("Moment")
	}
	query := db.NewSelect().Model(&row).Where("moment.id = ? AND moment.album_id = ?", momentID, albumID)
	if lock {
		query = query.For("UPDATE")
	}
	err := query.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return row, errcodes.NotFound("Moment")
	}
	return row, errorstack.CaptureContext(ctx, err)
}

func (m *Module) UpdateMoment(ctx context.Context, albumID, momentID string, request UpdateMomentRequest) (AlbumDetail, error) {
	title := strings.TrimSpace(request.Title)
	if utf8.RuneCountInString(title) > 200 {
		return AlbumDetail{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"title": "Enter a Moment title of 200 characters or fewer."})
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		moment, err := momentRow(ctx, tx, albumID, momentID, true)
		if err != nil {
			return err
		}
		moment.Title = nil
		if title != "" {
			moment.Title = &title
		}
		_, err = tx.NewUpdate().Model(&moment).Column("title").WherePK().Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

func (m *Module) SetMomentCover(ctx context.Context, albumID, momentID string, request SetMomentCoverRequest) (AlbumDetail, error) {
	if _, err := uuid.Parse(request.EntryID); err != nil {
		return AlbumDetail{}, errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"entry_id": "Choose media from this Moment."})
	}
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		moment, err := momentRow(ctx, tx, albumID, momentID, true)
		if err != nil {
			return err
		}
		valid, err := tx.NewSelect().Model((*models.AlbumEntry)(nil)).
			Where("id = ? AND album_id = ? AND moment_id = ? AND removed_at IS NULL", request.EntryID, albumID, momentID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if !valid {
			return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"entry_id": "Choose media from this Moment."})
		}
		entryID, _ := uuid.Parse(request.EntryID)
		moment.CoverEntryID = models.UUID(entryID)
		_, err = tx.NewUpdate().Model(&moment).Column("cover_entry_id").WherePK().Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
