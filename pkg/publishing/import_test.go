package publishing_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type library struct {
	albums      map[string]immich.Album
	assets      []immich.Asset
	beforeAsset func(context.Context, string) error
	members     func(context.Context, string, int) ([]immich.Asset, int, error)
}

func (l *library) CheckImport(context.Context) error { return nil }
func (l *library) ListAlbums(context.Context) ([]immich.Album, error) {
	a := []immich.Album{}
	for _, v := range l.albums {
		a = append(a, v)
	}
	return a, nil
}
func (l *library) GetAlbum(_ context.Context, id string) (immich.Album, error) {
	return l.albums[id], nil
}
func (l *library) ListMembers(ctx context.Context, id string, page int) ([]immich.Asset, int, error) {
	if l.members != nil {
		return l.members(ctx, id, page)
	}
	return l.assets, 0, nil
}
func (*library) ListFaces(context.Context, string) ([]immich.Face, error) { return nil, nil }
func (l *library) GetAsset(ctx context.Context, id string) (immich.Asset, error) {
	if l.beforeAsset != nil {
		if err := l.beforeAsset(ctx, id); err != nil {
			return immich.Asset{}, err
		}
	}
	for _, a := range l.assets {
		if a.ID == id {
			return a, nil
		}
	}
	panic("unknown fixture asset")
}
func fixture() *library {
	return &library{albums: map[string]immich.Album{"source": {ID: "source", Name: "Summer", Description: "From Immich", Count: 3}}, assets: []immich.Asset{
		{ID: "b", Checksum: "Yg==", Filename: "second.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-05T00:01:00+14:00", FileCreatedAt: "2026-07-04T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
		{ID: "z", Checksum: "eg==", Filename: "last.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00-10:00", FileCreatedAt: "2026-07-05T09:59:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
		{ID: "a", Checksum: "YQ==", Filename: "first.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-05T00:01:00+14:00", FileCreatedAt: "2026-07-04T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"},
	}}
}
func noQueue(context.Context, bun.Tx, string) error { return nil }

func TestSourceSearchPagesAndMarksImportedAlbums(t *testing.T) {
	t.Parallel()
	source := fixture()
	for i := range 26 {
		id := fmt.Sprintf("other-%02d", i)
		source.albums[id] = immich.Album{ID: id, Name: fmt.Sprintf("Trip %02d", i), Count: 0}
	}
	m := publishing.New(testdb.New(t), source, noQueue)
	imported, err := m.StartImport(t.Context(), "other-25")
	require.NoError(t, err)
	page, err := m.ListSources(t.Context(), "trip", 2)
	require.NoError(t, err)
	require.Equal(t, 26, page.Total)
	require.Equal(t, 2, page.Pages)
	require.Len(t, page.Albums, 2)
	require.Equal(t, "Trip 25", page.Albums[1].Title)
	require.Equal(t, imported.ID, page.Albums[1].AlbumID)
}

func TestConcurrentImportsAndReplayKeepIdentities(t *testing.T) {
	t.Parallel()
	m := publishing.New(testdb.New(t), fixture(), noQueue)
	const callers = 6
	results := make(chan publishing.AlbumDetail, callers)
	errs := make(chan error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() { <-start; album, err := m.StartImport(t.Context(), "source"); results <- album; errs <- err })
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	id := ""
	for result := range results {
		if id == "" {
			id = result.ID
		}
		require.Equal(t, id, result.ID)
	}
	require.NoError(t, m.ExecuteImport(t.Context(), id))
	before, err := m.GetAlbum(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), id))
	after, err := m.GetAlbum(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestPartialFailureNeverExposesIncompleteGallery(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.beforeAsset = func(_ context.Context, id string) error {
		if id == "z" {
			return errors.New("controlled read failure")
		}
		return nil
	}
	m := publishing.New(testdb.New(t), source, noQueue)
	pending, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.Error(t, m.ExecuteImport(t.Context(), pending.ID))
	failed, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, 1, failed.Processed)
	require.Empty(t, failed.Moments)
	source.beforeAsset = nil
	retried, err := m.RetryImport(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, "queued", retried.Status)
	require.NoError(t, m.ExecuteImport(t.Context(), pending.ID))
	done, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", done.Status)
	require.Len(t, done.Moments, 2)
}

func TestEnqueueFailureRollsBackAlbumIntent(t *testing.T) {
	t.Parallel()
	m := publishing.New(testdb.New(t), fixture(), func(context.Context, bun.Tx, string) error { return errors.New("queue unavailable") })
	_, err := m.StartImport(t.Context(), "source")
	require.Error(t, err)
	albums, err := m.ListAlbums(t.Context(), "")
	require.NoError(t, err)
	require.Empty(t, albums)
}

func TestSharedMediaAndOwnedTitle(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.albums["second"] = immich.Album{ID: "second", Name: "Another", Description: "Another source", Count: 3}
	m := publishing.New(testdb.New(t), source, noQueue)
	first, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), first.ID))
	second, err := m.StartImport(t.Context(), "second")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), second.ID))
	first, err = m.UpdateAlbum(t.Context(), first.ID, publishing.UpdateAlbumRequest{Title: "Our summer"})
	require.NoError(t, err)
	second, err = m.GetAlbum(t.Context(), second.ID)
	require.NoError(t, err)
	require.Equal(t, first.Moments[0].Entries[0].MediaID, second.Moments[0].Entries[0].MediaID)
	require.NotEqual(t, first.Moments[0].Entries[0].ID, second.Moments[0].Entries[0].ID)
	require.NoError(t, m.ExecuteImport(t.Context(), first.ID))
	again, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.Equal(t, "Our summer", again.Title)
	require.Equal(t, "From Immich", again.Description)
}

func TestInterruptedImportReportsProgressAndCanRecover(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	db.SetMaxOpenConns(1)
	source := fixture()
	entered := make(chan struct{})
	source.beforeAsset = func(ctx context.Context, id string) error {
		if id == "z" {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	m := publishing.New(db, source, noQueue)
	pending, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- m.ExecuteImport(ctx, pending.ID) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("import never reached checkpoint")
	}
	// The sole connection remains available while the source request is paused.
	processing, err := m.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "processing", processing.Status)
	require.Equal(t, 1, processing.Processed)
	require.Empty(t, processing.Moments)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	interrupted, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", interrupted.Status)
	source.beforeAsset = nil
	require.NoError(t, m.ExecuteImport(t.Context(), pending.ID))
	completed, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", completed.Status)
	require.Equal(t, 3, completed.Total)
}

func TestDatabaseRetainsRemovedEntryAndEnforcesSameAlbumCovers(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.albums["second"] = immich.Album{ID: "second", Name: "Another", Count: 3}
	m := publishing.New(db, source, noQueue)
	a, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), a.ID))
	a, err = m.GetAlbum(t.Context(), a.ID)
	require.NoError(t, err)
	b, err := m.StartImport(t.Context(), "second")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), b.ID))
	b, err = m.GetAlbum(t.Context(), b.ID)
	require.NoError(t, err)
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", b.Moments[0].Entries[0].ID).Where("id = ?", a.Moments[0].ID).Exec(ctx)
		return err
	})
	require.Error(t, err, "a foreign Album entry cannot become the cover")
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = ?", b.Moments[0].ID).Where("id = ?", a.Moments[0].Entries[0].ID).Exec(ctx)
		return err
	})
	require.Error(t, err, "a Moment cannot own another Album's Entry")
	retainedID := a.Moments[0].Entries[0].ID
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = CURRENT_TIMESTAMP").Set("moment_id = NULL").Where("id = ?", retainedID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*models.Moment)(nil)).Where("id = ?", a.Moments[0].ID).Exec(ctx)
		return err
	})
	require.NoError(t, err)
	removed, err := m.GetAlbum(t.Context(), a.ID)
	require.NoError(t, err)
	require.Len(t, removed.Moments, 1)
	require.Len(t, removed.Moments[0].Entries, 2)
	require.Equal(t, 2, removed.PhotoCount)
	require.Zero(t, removed.VideoCount)
	require.Equal(t, "2026-07-05", removed.StartDate)
	require.Equal(t, "2026-07-05", removed.EndDate)
	require.Equal(t, removed.Moments[0].Entries[0].ThumbnailURL, removed.CoverURL)
	listed, err := m.ListAlbums(t.Context(), "Summer")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, removed.Album, listed[0])
	_, err = db.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = NULL").Set("moment_id = ?", removed.Moments[0].ID).Where("id = ?", retainedID).Exec(t.Context())
	require.NoError(t, err)
	restored, err := m.GetAlbum(t.Context(), a.ID)
	require.NoError(t, err)
	require.Len(t, restored.Moments[0].Entries, 3)
	require.Equal(t, retainedID, restored.Moments[0].Entries[0].ID)
	require.Equal(t, 3, restored.PhotoCount)
	require.Equal(t, "2026-07-04", restored.StartDate)
	require.Equal(t, removed.CoverURL, restored.CoverURL)
}

func TestMembershipDriftWithUnchangedAlbumCountAndTimestampIsRejected(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Count: 4, UpdatedAt: "2026-07-06T00:00:00Z"}
	for _, id := range []string{"d", "x"} {
		asset := source.assets[0]
		asset.ID = id
		asset.Filename = id + ".jpg"
		source.assets = append(source.assets, asset)
	}
	rounds := 0
	source.members = func(_ context.Context, _ string, page int) ([]immich.Asset, int, error) {
		if page == 1 {
			if rounds == 0 {
				return []immich.Asset{source.assets[2], source.assets[0]}, 2, nil
			}
			return []immich.Asset{source.assets[0], source.assets[1]}, 2, nil
		}
		// A member read on page one disappears and another is restored. Offset paging
		// now skips an unseen member, despite an unchanged total and Album timestamp.
		rounds++
		return source.assets[3:], 0, nil
	}
	m := publishing.New(testdb.New(t), source, noQueue)
	album, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.Error(t, m.ExecuteImport(t.Context(), album.ID))
	failed, err := m.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	require.Empty(t, failed.Moments)
	require.NoError(t, m.ExecuteImport(t.Context(), album.ID))
	recovered, err := m.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", recovered.Status)
	require.Equal(t, 4, recovered.Total)
}

func TestImportCreatesUnpublishedLocalDayMoments(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.assets[0].Stack = &immich.AssetStack{ID: "stack", PrimaryAssetID: "a", AssetCount: 2}
	source.assets[2].Stack = &immich.AssetStack{ID: "stack", PrimaryAssetID: "a", AssetCount: 2}
	videoID := "motion-not-an-album-member"
	source.assets[1].LivePhotoVideoID = &videoID
	m := publishing.New(testdb.New(t), source, noQueue)
	ctx := t.Context()
	pending, err := m.StartImport(ctx, "source")
	require.NoError(t, err)
	require.Equal(t, "queued", pending.Status)
	require.Empty(t, pending.Moments)
	require.NoError(t, m.ExecuteImport(ctx, pending.ID))
	album, err := m.GetAlbum(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", album.Status)
	require.False(t, album.Published)
	require.Equal(t, "Summer", album.Title)
	require.Equal(t, "From Immich", album.Description)
	require.Equal(t, 3, album.Processed)
	require.Len(t, album.Moments, 2)
	require.Equal(t, "July 4, 2026", album.Moments[0].Label)
	require.Equal(t, "2026-07-04", album.Moments[0].Date)
	require.Equal(t, "last.jpg", album.Moments[0].Entries[0].Filename)
	require.Equal(t, "2026-07-05", album.Moments[1].Date)
	require.Equal(t, "first.jpg", album.Moments[1].Entries[0].Filename)
	require.Equal(t, "second.jpg", album.Moments[1].Entries[1].Filename)
	require.Equal(t, album.Moments[1].Entries[0].ID, album.Moments[1].CoverEntryID)
	require.Contains(t, album.Moments[1].Entries[0].ThumbnailURL, "/api/media/entries/")
}
