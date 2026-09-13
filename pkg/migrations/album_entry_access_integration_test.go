package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestAlbumEntryAccessConstraintsAndCascades(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Now().UTC()
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: now}
	albums := []models.Album{
		{ID: models.NewUUIDv7(), SourceID: "one", Title: "One", ImportStatus: "complete", ImportTotal: 1, ImportProcessed: 1, ImportUpdatedAt: now, CreatedAt: now},
		{ID: models.NewUUIDv7(), SourceID: "two", Title: "Two", ImportStatus: "complete", ImportTotal: 1, ImportProcessed: 1, ImportUpdatedAt: now, CreatedAt: now},
	}
	media := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "shared", Checksum: "shared", Filename: "shared.jpg", Kind: "IMAGE", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "shared"}
	moments := []models.Moment{
		{ID: models.NewUUIDv7(), AlbumID: albums[0].ID, CaptureDate: "2026-09-12", SortOrder: 1, CoverEntryID: models.NewUUIDv7()},
		{ID: models.NewUUIDv7(), AlbumID: albums[1].ID, CaptureDate: "2026-09-12", SortOrder: 1, CoverEntryID: models.NewUUIDv7()},
	}
	entries := []models.AlbumEntry{
		{ID: moments[0].CoverEntryID, AlbumID: albums[0].ID, MediaItemID: media.ID, MomentID: &moments[0].ID},
		{ID: moments[1].CoverEntryID, AlbumID: albums[1].ID, MediaItemID: media.ID, MomentID: &moments[1].ID},
	}
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&albums).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&media).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&moments).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&entries).Exec(ctx)
		return err
	}))
	albumRule := models.AlbumAccessDecision{AlbumID: albums[0].ID, PersonID: person.ID, Decision: "deny", UpdatedAt: now}
	_, err := db.NewInsert().Model(&albumRule).Exec(t.Context())
	require.Error(t, err, "Album rules cannot deny")
	albumRule.Decision = "allow"
	_, err = db.NewInsert().Model(&albumRule).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewInsert().Model(&albumRule).Exec(t.Context())
	require.Error(t, err, "one Album decision per Person")
	entryRule := models.EntryAccessDecision{EntryID: entries[0].ID, AlbumID: albums[1].ID, PersonID: person.ID, Decision: "allow", UpdatedAt: now}
	_, err = db.NewInsert().Model(&entryRule).Exec(t.Context())
	require.Error(t, err, "an Entry decision cannot claim another Album")
	entryRule.AlbumID = albums[0].ID
	_, err = db.NewInsert().Model(&entryRule).Exec(t.Context())
	require.NoError(t, err)
	entryRule.Decision = "deny"
	_, err = db.NewInsert().Model(&entryRule).Exec(t.Context())
	require.Error(t, err, "one Entry decision per Person")
	entryRule.EntryID, entryRule.AlbumID = entries[1].ID, albums[1].ID
	_, err = db.NewInsert().Model(&entryRule).Exec(t.Context())
	require.NoError(t, err, "duplicate Media Items keep independent Album Entry decisions")
	_, err = db.NewDelete().Model(&albums[0]).WherePK().Exec(t.Context())
	require.NoError(t, err)
	count, err := db.NewSelect().Model((*models.AlbumAccessDecision)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
	var surviving []models.EntryAccessDecision
	require.NoError(t, db.NewSelect().Model(&surviving).Scan(t.Context()))
	require.Len(t, surviving, 1)
	assert.Equal(t, entries[1].ID, surviving[0].EntryID)
	_, err = db.NewDelete().Model(&person).WherePK().Exec(t.Context())
	require.NoError(t, err)
	count, err = db.NewSelect().Model((*models.EntryAccessDecision)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
}
