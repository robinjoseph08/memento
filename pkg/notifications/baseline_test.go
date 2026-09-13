package notifications_test

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type fixedContent struct{ entries []notifications.VisibleEntry }

func (f *fixedContent) VisibleEntries(context.Context, bun.IDB, string) ([]notifications.VisibleEntry, error) {
	return f.entries, nil
}

func TestBaselineRecordsOnlyCurrentlyVisibleContentOnce(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Now().UTC()
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: now}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	album := models.Album{ID: models.NewUUIDv7(), SourceID: "source", Title: "Summer", ImportStatus: "complete", ImportUpdatedAt: now, CreatedAt: now}
	_, err = db.NewInsert().Model(&album).Exec(t.Context())
	require.NoError(t, err)
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "asset-a", Checksum: "a", Filename: "a.jpg", Kind: "IMAGE", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "1"}
	itemB := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "asset-b", Checksum: "b", Filename: "b.jpg", Kind: "IMAGE", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "1"}
	_, err = db.NewInsert().Model(&[]models.MediaItem{item, itemB}).Exec(t.Context())
	require.NoError(t, err)
	moment := models.Moment{ID: models.NewUUIDv7(), AlbumID: album.ID, CaptureDate: "2026-01-01"}
	entryA := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: item.ID, MomentID: &moment.ID}
	entryB := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: itemB.ID, MomentID: &moment.ID}
	moment.CoverEntryID = entryA.ID
	// Membership and cover references are deferred, so both sides land in one transaction.
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&moment).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&[]models.AlbumEntry{entryA, entryB}).Exec(ctx)
		return err
	}))
	content := &fixedContent{entries: []notifications.VisibleEntry{{AlbumID: album.ID.String(), EntryID: entryA.ID.String()}}}
	module := notifications.New(db, nil, nil, content, nil)
	record := func() notifications.Baseline {
		var result notifications.Baseline
		require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			result, err = module.RecordBaseline(ctx, tx, person.ID.String())
			return err
		}))
		return result
	}
	assert.Equal(t, notifications.Baseline{Albums: 1, Entries: 1}, record())
	ids, err := module.AnnouncedEntryIDs(t.Context(), person.ID.String())
	require.NoError(t, err)
	assert.Equal(t, []string{entryA.ID.String()}, ids)
	// Entry B becomes visible later. Repeating the baseline call is the caller's
	// mistake to prevent; the storage itself only adds what it is given.
	content.entries = append(content.entries, notifications.VisibleEntry{AlbumID: album.ID.String(), EntryID: entryB.ID.String()})
	assert.Equal(t, notifications.Baseline{Albums: 1, Entries: 2}, record())
	announced, err := module.Announced(t.Context(), db, person.ID.String())
	require.NoError(t, err)
	assert.Equal(t, notifications.Baseline{Albums: 1, Entries: 2}, announced)
	_, err = module.Announced(t.Context(), db, "not-a-person")
	require.Error(t, err)
}
