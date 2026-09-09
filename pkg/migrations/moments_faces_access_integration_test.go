package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestMomentsFacesAccessRollbackCollapsesSplitMoments(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Now().UTC()
	albumID := models.NewUUIDv7()
	entryIDs := []models.UUID{models.NewUUIDv7(), models.NewUUIDv7()}
	momentIDs := []models.UUID{models.NewUUIDv7(), models.NewUUIDv7()}
	media := []models.MediaItem{
		{ID: models.NewUUIDv7(), SourceID: "rollback-asset-1", Checksum: "one", Filename: "one.jpg", Kind: "IMAGE", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "one"},
		{ID: models.NewUUIDv7(), SourceID: "rollback-asset-2", Checksum: "two", Filename: "two.jpg", Kind: "IMAGE", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "two"},
	}
	err := db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		album := models.Album{ID: albumID, SourceID: "rollback-album", Title: "Rollback", ImportStatus: "complete", ImportTotal: 2, ImportProcessed: 2, ImportUpdatedAt: now, CreatedAt: now}
		if _, err := tx.NewInsert().Model(&album).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&media).Exec(ctx); err != nil {
			return err
		}
		moments := []models.Moment{
			{ID: momentIDs[0], AlbumID: albumID, CaptureDate: "2026-08-23", SortOrder: 1, CoverEntryID: entryIDs[0]},
			{ID: momentIDs[1], AlbumID: albumID, CaptureDate: "2026-08-23", SortOrder: 2, CoverEntryID: entryIDs[1]},
		}
		if _, err := tx.NewInsert().Model(&moments).Exec(ctx); err != nil {
			return err
		}
		entries := []models.AlbumEntry{
			{ID: entryIDs[0], AlbumID: albumID, MediaItemID: media[0].ID, MomentID: &momentIDs[0]},
			{ID: entryIDs[1], AlbumID: albumID, MediaItemID: media[1].ID, MomentID: &momentIDs[1]},
		}
		_, err := tx.NewInsert().Model(&entries).Exec(ctx)
		return err
	})
	require.NoError(t, err)

	registered := migrations.Migrations.Sorted()
	latest := registered[len(registered)-1]
	migrator := migrations.NewMigrator(db)
	require.Equal(t, "20260823000000", latest.Name)
	require.NoError(t, latest.Down(t.Context(), migrator, &latest))

	var moments []struct {
		ID           models.UUID
		CoverEntryID models.UUID
	}
	require.NoError(t, db.NewSelect().Table("moments").Column("id", "cover_entry_id").Scan(t.Context(), &moments))
	require.Len(t, moments, 1)
	var entryMomentIDs []models.UUID
	require.NoError(t, db.NewSelect().Table("album_entries").Column("moment_id").Order("id").Scan(t.Context(), &entryMomentIDs))
	assert.Equal(t, []models.UUID{moments[0].ID, moments[0].ID}, entryMomentIDs)
	assert.Contains(t, entryIDs, moments[0].CoverEntryID)

	require.NoError(t, latest.Up(t.Context(), migrator, &latest))
	count, err := db.NewSelect().Table("moments").Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
