package notifications

import (
	"context"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// RecordBaseline marks everything the Person can currently view as announced,
// including every Album in their "More albums" as already new to view, inside
// the caller's transaction so it commits with the owning transition. Existing
// associations are kept, so repeating the call never announces more.
func (m *Module) RecordBaseline(ctx context.Context, tx bun.Tx, personID string) (Baseline, error) {
	id, err := uuid.Parse(personID)
	if err != nil {
		return Baseline{}, errcodes.NotFound("Person")
	}
	visible, err := m.content.VisibleEntries(ctx, tx, personID)
	if err != nil {
		return Baseline{}, err
	}
	now := m.now().UTC()
	albums := map[string]bool{}
	albumRows := []models.AnnouncedAlbum{}
	entryRows := make([]models.AnnouncedEntry, 0, len(visible))
	for _, entry := range visible {
		albumID, err := uuid.Parse(entry.AlbumID)
		if err != nil {
			return Baseline{}, errorstack.Capture(err)
		}
		entryID, err := uuid.Parse(entry.EntryID)
		if err != nil {
			return Baseline{}, errorstack.Capture(err)
		}
		if !albums[entry.AlbumID] {
			albums[entry.AlbumID] = true
			albumRows = append(albumRows, models.AnnouncedAlbum{PersonID: models.UUID(id), AlbumID: models.UUID(albumID), AnnouncedAt: now})
		}
		entryRows = append(entryRows, models.AnnouncedEntry{PersonID: models.UUID(id), EntryID: models.UUID(entryID), AnnouncedAt: now})
	}
	if len(albumRows) > 0 {
		if _, err := tx.NewInsert().Model(&albumRows).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return Baseline{}, errorstack.CaptureContext(ctx, err)
		}
	}
	if len(entryRows) > 0 {
		if _, err := tx.NewInsert().Model(&entryRows).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return Baseline{}, errorstack.CaptureContext(ctx, err)
		}
	}
	offered, err := m.content.OfferedAlbums(ctx, tx, personID)
	if err != nil {
		return Baseline{}, err
	}
	offerRows := make([]models.AnnouncedOfferedAlbum, 0, len(offered))
	for _, album := range offered {
		albumID, err := uuid.Parse(album.AlbumID)
		if err != nil {
			return Baseline{}, errorstack.Capture(err)
		}
		offerRows = append(offerRows, models.AnnouncedOfferedAlbum{PersonID: models.UUID(id), AlbumID: models.UUID(albumID), AnnouncedAt: now})
	}
	if len(offerRows) > 0 {
		if _, err := tx.NewInsert().Model(&offerRows).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return Baseline{}, errorstack.CaptureContext(ctx, err)
		}
	}
	return m.Announced(ctx, tx, personID)
}

// RecordJoin marks an Album the Person just joined, and the offered Album
// Entries they joined with it, as announced, inside the Join's transaction.
// They chose that media themselves, so only media added after the Join is
// news, and the Album is never new to view again. Marking the Album itself
// announced means direct media still pending in it reads as an update.
func (m *Module) RecordJoin(ctx context.Context, tx bun.Tx, personID, albumID string, entryIDs []string) error {
	person, err := uuid.Parse(personID)
	if err != nil {
		return errcodes.NotFound("Person")
	}
	album, err := uuid.Parse(albumID)
	if err != nil {
		return errcodes.NotFound("Album")
	}
	// Take the lock approval takes, so a Join waits for an approval in
	// progress, and an approval after it sees the Join as a changed review.
	_, err = tx.NewSelect().Model((*models.Person)(nil)).Column("person.id").Where("person.id = ?", personID).For("UPDATE").Exec(ctx)
	if err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	now := m.now().UTC()
	offer := models.AnnouncedOfferedAlbum{PersonID: models.UUID(person), AlbumID: models.UUID(album), AnnouncedAt: now}
	if _, err := tx.NewInsert().Model(&offer).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if len(entryIDs) == 0 {
		return nil
	}
	own := models.AnnouncedAlbum{PersonID: models.UUID(person), AlbumID: models.UUID(album), AnnouncedAt: now}
	if _, err := tx.NewInsert().Model(&own).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	entryRows := make([]models.AnnouncedEntry, 0, len(entryIDs))
	for _, id := range entryIDs {
		entryID, err := uuid.Parse(id)
		if err != nil {
			return errorstack.Capture(err)
		}
		entryRows = append(entryRows, models.AnnouncedEntry{PersonID: models.UUID(person), EntryID: models.UUID(entryID), AnnouncedAt: now})
	}
	_, err = tx.NewInsert().Model(&entryRows).On("CONFLICT DO NOTHING").Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

// Announced counts the Person's baseline. Callers use it to show or assert
// that later visibility changes stay unannounced.
func (m *Module) Announced(ctx context.Context, db bun.IDB, personID string) (Baseline, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return Baseline{}, errcodes.NotFound("Person")
	}
	var result Baseline
	albums, err := db.NewSelect().Model((*models.AnnouncedAlbum)(nil)).Where("person_id = ?", personID).Count(ctx)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	entries, err := db.NewSelect().Model((*models.AnnouncedEntry)(nil)).Where("person_id = ?", personID).Count(ctx)
	if err != nil {
		return result, errorstack.CaptureContext(ctx, err)
	}
	return Baseline{Albums: albums, Entries: entries}, nil
}

// AnnouncedEntryIDs lists announced Album Entries for exact assertions.
func (m *Module) AnnouncedEntryIDs(ctx context.Context, personID string) ([]string, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return nil, errcodes.NotFound("Person")
	}
	ids := []string{}
	err := m.db.NewSelect().Model((*models.AnnouncedEntry)(nil)).ColumnExpr("entry_id::text").Where("person_id = ?", personID).Order("entry_id").Scan(ctx, &ids)
	return ids, errorstack.CaptureContext(ctx, err)
}
