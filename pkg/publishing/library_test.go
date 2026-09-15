package publishing_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
)

func TestLibraryOrdersDaysAndRatiosNewestFirst(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	wide, square, height := 4032, 3024, 3024
	source.assets[0].LocalDateTime = "2026-07-05T22:00:00+14:00"
	source.assets[0].Width, source.assets[0].Height = &wide, &height
	source.assets[2].Width, source.assets[2].Height = &square, &height
	source.assets = append(source.assets, immich.Asset{ID: "clip", Kind: "VIDEO", Filename: "clip.mp4", Checksum: "Yw==", LocalDateTime: "2026-07-06T00:01:00+14:00", FileCreatedAt: "2026-07-05T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"})
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Count: 4}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)

	summary, err := module.ViewLibrary(t.Context(), curator.ID.String())
	require.NoError(t, err)
	require.Equal(t, 3, summary.PhotoCount)
	require.Equal(t, 1, summary.VideoCount)
	require.Equal(t, []publishing.ViewerDay{
		{Date: "2026-07-06", PhotoCount: 0, VideoCount: 1, PhotoRatios: []float64{}},
		{Date: "2026-07-05", PhotoCount: 2, VideoCount: 0, PhotoRatios: []float64{1.333, 1}},
		{Date: "2026-07-04", PhotoCount: 1, VideoCount: 0, PhotoRatios: []float64{1.5}},
	}, summary.Days)
	photos, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, photos.Entries, 3)
	for i, title := range []string{"second", "first", "last"} {
		require.Equal(t, title, photos.Entries[i].Title)
	}
	bounded, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", publishing.EntryPageRequest{From: "2026-07-05", To: "2026-07-06"})
	require.NoError(t, err)
	require.Equal(t, photos.Entries[:2], bounded.Entries)
	videos, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "VIDEO", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, videos.Entries, 1)
	require.Equal(t, "clip", videos.Entries[0].Title)
	require.NotEmpty(t, videos.Entries[0].PlaybackURL)
	albumView, err := module.ViewAlbum(t.Context(), curator.ID.String(), "", album.ID)
	require.NoError(t, err)
	require.Equal(t, "2026-07-04", albumView.Days[0].Date)
	require.Equal(t, []float64{1, 1.333}, albumView.Days[1].PhotoRatios)
}

func TestLibraryDeduplicatesOnlyAccessibleEntries(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.albums["other"] = immich.Album{ID: "other", Name: "Other", Count: 3}
	module := publishing.New(db, source, noQueue)
	albums := []publishing.AlbumDetail{}
	for _, id := range []string{"source", "other"} {
		album, err := module.StartImport(t.Context(), id)
		require.NoError(t, err)
		require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
		album, err = module.GetAlbum(t.Context(), album.ID)
		require.NoError(t, err)
		albums = append(albums, album)
	}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	firstIDs, secondIDs := entryIDs(albums[0]), entryIDs(albums[1])
	for filename, id := range firstIDs {
		require.Less(t, id, secondIDs[filename])
	}
	check := func(expected map[string]string) {
		t.Helper()
		summary, err := module.ViewLibrary(t.Context(), person.ID.String())
		require.NoError(t, err)
		require.Equal(t, len(expected), summary.PhotoCount)
		require.Zero(t, summary.VideoCount)
		count := 0
		for _, day := range summary.Days {
			count += day.PhotoCount
			require.Len(t, day.PhotoRatios, day.PhotoCount)
		}
		require.Equal(t, len(expected), count)
		page, err := module.ViewLibraryEntries(t.Context(), person.ID.String(), "IMAGE", publishing.EntryPageRequest{})
		require.NoError(t, err)
		require.Len(t, page.Entries, len(expected))
		for _, entry := range page.Entries {
			require.Equal(t, expected[entry.Title+".jpg"], entry.ID)
			require.Contains(t, entry.ThumbnailURL, "/viewer/"+person.ID.String()+"/entries/"+entry.ID+"/")
			require.NotEmpty(t, entry.DownloadURL)
			require.NoError(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", entry.ID))
		}
		if len(expected) == 0 {
			require.NotNil(t, summary.Days)
			require.Empty(t, summary.Days)
			require.NotNil(t, page.Entries)
		}
	}
	allow := func(album publishing.AlbumDetail) {
		t.Helper()
		_, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
		require.NoError(t, err)
	}
	publish := func(album publishing.AlbumDetail) {
		t.Helper()
		review, err := module.ReviewPublication(t.Context(), album.ID)
		require.NoError(t, err)
		_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
		require.NoError(t, err)
	}
	entryDecision := func(album publishing.AlbumDetail, id string, decision publishing.Decision) {
		t.Helper()
		_, err := module.SaveEntryRules(t.Context(), album.ID, id, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: decision}}})
		require.NoError(t, err)
	}

	check(nil)
	allow(albums[1])
	check(nil)
	publish(albums[0])
	publish(albums[1])
	check(secondIDs)
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", firstIDs["second.jpg"]))
	allow(albums[0])
	check(firstIDs)
	entryDecision(albums[0], firstIDs["second.jpg"], publishing.DecisionDeny)
	check(map[string]string{"first.jpg": firstIDs["first.jpg"], "second.jpg": secondIDs["second.jpg"], "last.jpg": firstIDs["last.jpg"]})
	entryDecision(albums[1], secondIDs["second.jpg"], publishing.DecisionDeny)
	check(map[string]string{"first.jpg": firstIDs["first.jpg"], "last.jpg": firstIDs["last.jpg"]})
	entryDecision(albums[0], firstIDs["second.jpg"], publishing.DecisionInherit)
	entryDecision(albums[1], secondIDs["second.jpg"], publishing.DecisionInherit)
	check(firstIDs)

	moment := momentOf(albums[0], "second.jpg")
	_, err = module.SaveMomentRules(t.Context(), albums[0].ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	check(map[string]string{"first.jpg": secondIDs["first.jpg"], "second.jpg": secondIDs["second.jpg"], "last.jpg": firstIDs["last.jpg"]})
	entryDecision(albums[0], firstIDs["second.jpg"], publishing.DecisionAllow)
	check(map[string]string{"first.jpg": secondIDs["first.jpg"], "second.jpg": firstIDs["second.jpg"], "last.jpg": firstIDs["last.jpg"]})
	_, err = module.SaveMomentRules(t.Context(), albums[0].ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionInherit}}})
	require.NoError(t, err)
	check(firstIDs)

	for i, album := range albums {
		id := entryIDs(album)["second.jpg"]
		request := publishing.ExcludeEntriesRequest{EntryIDs: []string{id}}
		review, err := module.PreviewExclude(t.Context(), album.ID, momentOf(album, "second.jpg").ID, request)
		require.NoError(t, err)
		request.ReviewToken = review.ReviewToken
		_, err = module.ExcludeEntries(t.Context(), album.ID, momentOf(album, "second.jpg").ID, request)
		require.NoError(t, err)
		require.Error(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", id))
		if i == 0 {
			check(map[string]string{"first.jpg": firstIDs["first.jpg"], "second.jpg": secondIDs["second.jpg"], "last.jpg": firstIDs["last.jpg"]})
		}
	}
	check(map[string]string{"first.jpg": firstIDs["first.jpg"], "last.jpg": firstIDs["last.jpg"]})
	// One membership is excluded; the other is removed by source synchronization.
	_, err = db.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("excluded_at = NULL").Where("id = ?", secondIDs["second.jpg"]).Exec(t.Context())
	require.NoError(t, err)
	check(map[string]string{"first.jpg": firstIDs["first.jpg"], "last.jpg": firstIDs["last.jpg"]})
	_, err = module.UnpublishAlbum(t.Context(), albums[0].ID)
	require.NoError(t, err)
	check(map[string]string{"first.jpg": secondIDs["first.jpg"], "last.jpg": secondIDs["last.jpg"]})
	_, err = db.NewUpdate().Model((*models.Album)(nil)).Set("import_status = 'failed'").Set("published_at = NULL").Where("id = ?", albums[1].ID).Exec(t.Context())
	require.NoError(t, err)
	check(nil)
	// Curators still bypass publication, but cannot see an incomplete import.
	_, err = db.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("removed_at = NULL").Set("moment_id = ?", momentOf(albums[1], "second.jpg").ID).Where("id = ?", secondIDs["second.jpg"]).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model(&person).Set("is_curator = true").WherePK().Exec(t.Context())
	require.NoError(t, err)
	check(map[string]string{"first.jpg": firstIDs["first.jpg"], "last.jpg": firstIDs["last.jpg"]})
	_, err = db.NewUpdate().Model(&person).Set("deactivated_at = ?", time.Now().UTC()).WherePK().Exec(t.Context())
	require.NoError(t, err)
	_, err = module.ViewLibrary(t.Context(), person.ID.String())
	require.Error(t, err)
	_, err = module.ViewLibraryEntries(t.Context(), person.ID.String(), "IMAGE", publishing.EntryPageRequest{})
	require.Error(t, err)
	_, err = module.ViewLibrary(t.Context(), "invalid")
	require.Error(t, err)
}
