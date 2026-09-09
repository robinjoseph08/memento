package publishing_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
)

func TestImportCoverFallsBackWhenSourceCoverIsAbsentOrNotAMember(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for _, name := range []string{"omitted", "empty", "outside"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := fixture()
			album := source.albums["source"]
			album.ID = name
			switch name {
			case "empty":
				value := ""
				album.ThumbnailID = &value
			case "outside":
				value := "not-a-member"
				album.ThumbnailID = &value
			}
			source.albums = map[string]immich.Album{name: album}
			module := publishing.New(db, source, noQueue)
			pending, err := module.StartImport(t.Context(), name)
			require.NoError(t, err)
			require.NoError(t, module.ExecuteImport(t.Context(), pending.ID))
			detail, err := module.GetAlbum(t.Context(), pending.ID)
			require.NoError(t, err)
			for _, moment := range detail.Moments {
				require.Equal(t, moment.Entries[0].ID, moment.CoverEntryID)
			}
		})
	}
}

func TestSourceCoverInFirstMomentSuppliesDerivedAlbumCover(t *testing.T) {
	t.Parallel()
	source := fixture()
	selected := "b"
	album := source.albums["source"]
	album.ThumbnailID = &selected
	source.albums["source"] = album
	source.assets[0].LocalDateTime = "2026-07-04T23:59:01-10:00"
	module := publishing.New(testdb.New(t), source, noQueue)
	pending, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), pending.ID))
	detail, err := module.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	chosen := detail.Moments[0].Entries[1]
	require.Equal(t, "second.jpg", chosen.Filename)
	require.Equal(t, chosen.ID, detail.Moments[0].CoverEntryID)
	require.Equal(t, chosen.ThumbnailURL, detail.CoverURL)
}

func TestImportSeedsSourceCoverOnlyInItsContainingMoment(t *testing.T) {
	t.Parallel()
	source := fixture()
	selected := "b"
	album := source.albums["source"]
	album.ThumbnailID = &selected
	source.albums["source"] = album
	module := publishing.New(testdb.New(t), source, noQueue)
	pending, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), pending.ID))
	detail, err := module.GetAlbum(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Len(t, detail.Moments, 2)
	require.Equal(t, detail.Moments[0].Entries[0].ID, detail.Moments[0].CoverEntryID)
	require.Equal(t, "second.jpg", detail.Moments[1].Entries[1].Filename)
	require.Equal(t, detail.Moments[1].Entries[1].ID, detail.Moments[1].CoverEntryID)
	require.Equal(t, detail.Moments[0].Entries[0].ThumbnailURL, detail.CoverURL)
	selected = "a"
	require.NoError(t, module.ExecuteImport(t.Context(), pending.ID))
	after, err := module.RetryImport(t.Context(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, detail, after, "replaying a completed import must not replace its configured covers")
}
