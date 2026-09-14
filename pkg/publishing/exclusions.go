package publishing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// previewExclude describes keeping selected media out of the Album. The
// entries leave their Moment and the Album but are retained with their
// access rules and announcement history. A Moment that loses its cover gets
// its earliest remaining item; one that loses everything is removed.
func previewExclude(state structureState, momentID string, request ExcludeEntriesRequest) (StructurePreview, structureState, error) {
	source, ok := state.Moments[momentID]
	if !ok {
		return StructurePreview{}, state, errcodes.NotFound("Moment")
	}
	selected, remaining, err := selectedEntries(state, momentID, request.EntryIDs)
	if err != nil {
		return StructurePreview{}, state, err
	}
	after := state.clone()
	for entryID := range selected {
		delete(after.EntryMoments, entryID)
	}
	removes := remaining == 0
	if removes {
		delete(after.Moments, momentID)
		delete(after.Decisions, momentID)
	} else if selected[source.CoverID] {
		updated := after.Moments[momentID]
		updated.CoverID = state.firstEntry(state.remainingEntries(momentID, selected))
		after.Moments[momentID] = updated
	}
	canonical := request
	canonical.EntryIDs = canonicalEntries(request.EntryIDs)
	canonical.ReviewToken = ""
	preview := StructurePreview{Ready: true, RemovesMoment: removes, Changes: reviewedChanges(state, after), Conflicts: []AccessConflict{}}
	preview.ReviewToken, err = reviewToken("exclude", state, canonical, after)
	return preview, after, err
}

func (m *Module) PreviewExclude(ctx context.Context, albumID, momentID string, request ExcludeEntriesRequest) (StructurePreview, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return StructurePreview{}, err
	}
	state, err := m.loadStructure(ctx, m.db, albumID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	preview, _, err := previewExclude(state, momentID, request)
	return preview, err
}

// ExcludeEntries keeps the reviewed media out of the Album. A later check
// never offers it as new, and Add back returns it with its identity.
func (m *Module) ExcludeEntries(ctx context.Context, albumID, momentID string, request ExcludeEntriesRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		preview, after, err := previewExclude(state, momentID, request)
		if err != nil {
			return err
		}
		if request.ReviewToken == "" || request.ReviewToken != preview.ReviewToken {
			return staleReview()
		}
		now := time.Now().UTC()
		if cover := after.Moments[momentID].CoverID; !preview.RemovesMoment && cover != state.Moments[momentID].CoverID {
			if _, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", cover).Where("id = ?", momentID).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = ?", now).Set("excluded_at = ?", now).Set("moment_id = NULL").
			Where("album_id = ? AND moment_id = ? AND removed_at IS NULL AND id IN (?)", albumID, momentID, bun.List(request.EntryIDs)).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if count != int64(len(request.EntryIDs)) {
			return staleReview()
		}
		if preview.RemovesMoment {
			if _, err := tx.NewDelete().Model((*models.Moment)(nil)).Where("id = ? AND album_id = ?", momentID, albumID).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}

// excludedEntry loads the Media Item behind one retained, excluded Album
// Entry, locking the entry when asked.
func excludedEntry(ctx context.Context, db bun.IDB, albumID, entryID string, lock bool) (models.MediaItem, error) {
	var entry models.AlbumEntry
	var item models.MediaItem
	if _, err := uuid.Parse(albumID); err != nil {
		return item, errcodes.NotFound("Album")
	}
	if _, err := uuid.Parse(entryID); err != nil {
		return item, errcodes.NotFound("Excluded item")
	}
	query := db.NewSelect().Model(&entry).Where("album_entry.id = ? AND album_entry.album_id = ? AND album_entry.excluded_at IS NOT NULL", entryID, albumID)
	if lock {
		query = query.For("UPDATE")
	}
	err := query.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return item, errcodes.NotFound("Excluded item")
	}
	if err != nil {
		return item, errorstack.CaptureContext(ctx, err)
	}
	err = db.NewSelect().Model(&item).Where("media_item.id = ?", entry.MediaItemID).Scan(ctx)
	return item, errorstack.CaptureContext(ctx, err)
}

// previewInclude describes adding excluded media back: into an existing
// Moment, or into a new Moment for its capture day keyed "new:YYYY-MM-DD".
func previewInclude(state structureState, entryID, date string, request IncludeEntryRequest) (StructurePreview, structureState, error) {
	after := state.clone()
	key := request.MomentID
	if _, existing := state.Moments[key]; !existing {
		if key != newMomentPrefix+date {
			return StructurePreview{}, state, structureField("moment_id", "Choose a Moment in this Album.")
		}
		maxOrder := int64(0)
		for _, moment := range state.Moments {
			maxOrder = max(maxOrder, moment.SortOrder)
		}
		after.Moments[key] = structureMoment{ID: key, CaptureDate: date, SortOrder: maxOrder + 1, CoverID: entryID}
		after.Decisions[key] = map[string]Decision{}
	}
	after.EntryMoments[entryID] = key
	canonical := request
	canonical.ReviewToken = ""
	preview := StructurePreview{Ready: true, Changes: reviewedChanges(state, after), Conflicts: []AccessConflict{}}
	var err error
	preview.ReviewToken, err = reviewToken("include:"+entryID, state, canonical, after)
	return preview, after, err
}

func (m *Module) PreviewInclude(ctx context.Context, albumID, entryID string, request IncludeEntryRequest) (StructurePreview, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return StructurePreview{}, err
	}
	item, err := excludedEntry(ctx, m.db, albumID, entryID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	state, err := m.loadStructure(ctx, m.db, albumID, false)
	if err != nil {
		return StructurePreview{}, err
	}
	preview, _, err := previewInclude(state, entryID, item.CapturedAt.Format("2006-01-02"), request)
	return preview, err
}

// IncludeEntry returns excluded media to the Album in the reviewed Moment.
// A video that was kept out before it was ever probed gets its extraction now.
func (m *Module) IncludeEntry(ctx context.Context, albumID, entryID string, request IncludeEntryRequest) (AlbumDetail, error) {
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		item, err := excludedEntry(ctx, tx, albumID, entryID, true)
		if err != nil {
			return err
		}
		state, err := m.loadStructure(ctx, tx, albumID, true)
		if err != nil {
			return err
		}
		date := item.CapturedAt.Format("2006-01-02")
		preview, after, err := previewInclude(state, entryID, date, request)
		if err != nil {
			return err
		}
		if request.ReviewToken == "" || request.ReviewToken != preview.ReviewToken {
			return staleReview()
		}
		momentID := request.MomentID
		if strings.HasPrefix(momentID, newMomentPrefix) {
			created := after.Moments[momentID]
			row := models.Moment{ID: models.NewUUIDv7(), AlbumID: models.UUID(uuid.MustParse(albumID)), CaptureDate: created.CaptureDate, SortOrder: created.SortOrder, CoverEntryID: models.UUID(uuid.MustParse(entryID))}
			if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			momentID = row.ID.String()
		}
		updated, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = NULL").Set("excluded_at = NULL").Set("moment_id = ?", momentID).
			Where("id = ? AND album_id = ? AND excluded_at IS NOT NULL", entryID, albumID).Exec(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if err := affectedOne(ctx, updated); err != nil {
			return err
		}
		if item.Kind == "VIDEO" && m.Chapters != nil {
			return m.Chapters.RequestChapters(ctx, tx, item.ID, item.Checksum)
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
