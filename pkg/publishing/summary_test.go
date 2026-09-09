package publishing_test

import (
	"fmt"
	"testing"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
)

func TestAlbumCoverUsesConfiguredAvailableMomentCoversOnly(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.assets[0].LocalDateTime = "2026-07-04T12:00:00+14:00"
	m := publishing.New(db, source, noQueue)
	pending, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), pending.ID))
	detail, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	configured := detail.Moments[0].Entries[1]
	nextCover := detail.Moments[1].Entries[0]
	// Cover editing is not yet public. Set up Curator-selected cover state directly.
	_, err = db.NewUpdate().Model((*models.Moment)(nil)).Set("cover_entry_id = ?", configured.ID).
		Where("id = ?", detail.Moments[0].ID).Exec(t.Context())
	require.NoError(t, err)
	assertCover := func(want string) {
		t.Helper()
		albums, err := m.ListAlbums(t.Context(), "")
		require.NoError(t, err)
		require.Len(t, albums, 1)
		require.Equal(t, want, albums[0].CoverURL)
		current, err := m.GetAlbum(t.Context(), pending.ID)
		require.NoError(t, err)
		require.Equal(t, albums[0], current.Album)
	}
	assertCover(configured.ThumbnailURL)
	_, err = db.NewUpdate().Model((*models.MediaItem)(nil)).Set("offline = TRUE").Where("id = ?", configured.MediaID).Exec(t.Context())
	require.NoError(t, err)
	assertCover(nextCover.ThumbnailURL)
	_, err = db.NewUpdate().Model((*models.MediaItem)(nil)).Set("trashed = TRUE").Where("id = ?", nextCover.MediaID).Exec(t.Context())
	require.NoError(t, err)
	assertCover("")
}

func TestIncompleteImportsNeverExposeSummaryOrGallery(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	m := publishing.New(db, fixture(), noQueue)
	pending, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), pending.ID))
	// Seed retained entries under incomplete statuses to verify the read boundary.
	for _, status := range []string{"queued", "processing", "failed", "interrupted"} {
		_, err := db.NewUpdate().Model((*models.Album)(nil)).Set("import_status = ?", status).
			Where("id = ?", pending.ID).Exec(t.Context())
		require.NoError(t, err)
		albums, err := m.ListAlbums(t.Context(), "Summer")
		require.NoError(t, err)
		require.Len(t, albums, 1)
		detail, err := m.GetAlbum(t.Context(), pending.ID)
		require.NoError(t, err)
		require.Equal(t, albums[0], detail.Album)
		require.Equal(t, status, detail.Status)
		require.Zero(t, detail.PhotoCount)
		require.Zero(t, detail.VideoCount)
		require.Empty(t, detail.StartDate)
		require.Empty(t, detail.EndDate)
		require.Empty(t, detail.CoverURL)
		require.Empty(t, detail.Moments)
	}
}

func TestAlbumSummaryMatchesDetailWithPhotoVideoCountsAndLocalDates(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.assets[0].Kind = "VIDEO"
	source.assets[0].Filename = "second.mp4"
	m := publishing.New(testdb.New(t), source, noQueue)
	pending, err := m.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, m.ExecuteImport(t.Context(), pending.ID))
	detail, err := m.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	albums, err := m.ListAlbums(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, albums, 1)
	want := publishing.Album{
		ID: pending.ID, SourceID: "source", Title: "Summer", Description: "From Immich",
		Status: "complete", Processed: 3, Total: 3, PhotoCount: 2, VideoCount: 1,
		StartDate: "2026-07-04", EndDate: "2026-07-05",
		CoverURL: detail.Moments[0].Entries[0].ThumbnailURL,
	}
	require.Equal(t, want, albums[0])
	require.Equal(t, want, detail.Album)
}

func TestSourcePagesOrderByNewestStartWithIDTiesAndUndatedLast(t *testing.T) {
	t.Parallel()
	source := &library{albums: map[string]immich.Album{}}
	for i := range 25 {
		id := fmt.Sprintf("source-%02d", i)
		source.albums[id] = immich.Album{ID: id, Name: fmt.Sprintf("Trip %02d", 25-i), StartDate: "2026-07-05T00:00:00Z"}
	}
	source.albums["older"] = immich.Album{ID: "older", Name: "A older", StartDate: "2025-01-01T00:00:00Z", EndDate: "2027-01-01T00:00:00Z"}
	source.albums["empty-a"] = immich.Album{ID: "empty-a", Name: "B empty"}
	source.albums["empty-b"] = immich.Album{ID: "empty-b", Name: "A empty"}
	m := publishing.New(testdb.New(t), source, noQueue)
	first, err := m.ListSources(t.Context(), "", 1)
	require.NoError(t, err)
	require.Equal(t, 28, first.Total)
	require.Equal(t, 2, first.Pages)
	require.Len(t, first.Albums, 24)
	require.Equal(t, "source-00", first.Albums[0].ID)
	require.Equal(t, "source-23", first.Albums[23].ID)
	second, err := m.ListSources(t.Context(), "", 2)
	require.NoError(t, err)
	ids := []string{}
	for _, album := range second.Albums {
		ids = append(ids, album.ID)
	}
	require.Equal(t, []string{"source-24", "older", "empty-a", "empty-b"}, ids)
}

func TestAlbumTitleSearchIsTrimmedCaseInsensitiveAndLiteralWithoutImmich(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	m := publishing.New(db, source, noQueue)
	for _, title := range []string{"Summer 100%_done", "Summer 100xxdone", `Path\Album`} {
		source.albums[title] = immich.Album{ID: title, Name: title}
		_, err := m.StartImport(t.Context(), title)
		require.NoError(t, err)
	}
	m = publishing.New(db, nil, noQueue)
	for _, scenario := range []struct {
		search string
		titles []string
	}{
		{"  sUMMer 100%_DONE  ", []string{"Summer 100%_done"}},
		{"%", []string{"Summer 100%_done"}},
		{"_", []string{"Summer 100%_done"}},
		{`\`, []string{`Path\Album`}},
		{"not present", []string{}},
		{"  ", []string{"Summer 100%_done", "Summer 100xxdone", `Path\Album`}},
	} {
		t.Run(scenario.search, func(t *testing.T) {
			t.Parallel()
			albums, err := m.ListAlbums(t.Context(), scenario.search)
			require.NoError(t, err)
			titles := []string{}
			for _, album := range albums {
				titles = append(titles, album.Title)
			}
			require.Equal(t, scenario.titles, titles)
		})
	}
}

func TestAlbumsOrderByNewestCaptureStartWithUndatedLast(t *testing.T) {
	t.Parallel()
	source := fixture()
	m := publishing.New(testdb.New(t), source, noQueue)
	ids := []string{}
	for _, scenario := range []struct {
		id, title string
		dates     []string
	}{
		{"newer", "Z newest start", []string{"2026-07-05T00:01:00+14:00"}},
		{"tie", "B tied start", []string{"2026-07-05T00:01:00-10:00"}},
		{"older", "A older start, newest end", []string{"2025-01-01T23:59:00-10:00", "2027-01-01T00:01:00+14:00"}},
		{"empty", "Empty", nil},
	} {
		source.assets = nil
		for i, date := range scenario.dates {
			asset := fixture().assets[i]
			asset.ID = scenario.id + asset.ID
			asset.LocalDateTime = date
			source.assets = append(source.assets, asset)
		}
		source.albums[scenario.id] = immich.Album{ID: scenario.id, Name: scenario.title, Count: len(source.assets)}
		album, err := m.StartImport(t.Context(), scenario.id)
		require.NoError(t, err)
		require.NoError(t, m.ExecuteImport(t.Context(), album.ID))
		ids = append(ids, album.ID)
	}
	albums, err := m.ListAlbums(t.Context(), "")
	require.NoError(t, err)
	got := []string{}
	for _, album := range albums {
		got = append(got, album.ID)
	}
	require.Equal(t, ids, got, "capture start, not title, import time, end date, or UTC conversion, determines order")
}
