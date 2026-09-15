package publishing_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
)

func TestViewerPagesUseLocalCaptureTimeAndEntryIDWithoutPrivateMetadata(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.assets = nil
	for i := range 502 {
		source.assets = append(source.assets, immich.Asset{ID: fmt.Sprintf("photo-%03d", i), Kind: "IMAGE", Filename: fmt.Sprintf("photo-%03d.jpg", i), Checksum: "YQ==", LocalDateTime: "2026-07-05T00:01:00+14:00", FileCreatedAt: "2026-07-04T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"})
	}
	width, height := 4032, 3024
	source.assets[0].Width, source.assets[0].Height = &width, &height
	source.assets = append(source.assets, immich.Asset{ID: "clip", Kind: "VIDEO", Filename: "clip.mp4", Checksum: "Yg==", LocalDateTime: "2026-07-05T00:01:00+14:00", FileCreatedAt: "2026-07-04T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"})
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Count: 503}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)
	first, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, first.Entries, 500)
	require.NotEmpty(t, first.NextCursor)
	second, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Entries, 2)
	require.Empty(t, second.NextCursor)
	viewed, err := module.ViewAlbum(t.Context(), curator.ID.String(), "", album.ID)
	require.NoError(t, err)
	require.Len(t, viewed.Days, 1)
	require.Len(t, viewed.Days[0].PhotoRatios, 502)
	require.Contains(t, viewed.Days[0].PhotoRatios, 1.333)
	require.Contains(t, viewed.Days[0].PhotoRatios, 1.5)
	// Day bounds select by local capture day, the end exclusive, and a cursor
	// continues inside them.
	bounded, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{From: "2026-07-05", To: "2026-07-06"})
	require.NoError(t, err)
	require.Len(t, bounded.Entries, 500)
	rest, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{From: "2026-07-05", To: "2026-07-06", Cursor: bounded.NextCursor})
	require.NoError(t, err)
	require.Len(t, rest.Entries, 2)
	for _, page := range []publishing.EntryPageRequest{{To: "2026-07-05"}, {From: "2026-07-06"}} {
		empty, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", page)
		require.NoError(t, err)
		require.Empty(t, empty.Entries)
	}
	_, err = module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{From: "yesterday"})
	require.Error(t, err)
	previous := ""
	for _, entry := range append(first.Entries, second.Entries...) {
		require.Greater(t, entry.ID, previous)
		require.Equal(t, "2026-07-05T00:01:00", entry.CapturedAt)
		require.Equal(t, "/api/media/viewer/"+curator.ID.String()+"/entries/"+entry.ID+"/original?v="+strings.Split(entry.ThumbnailURL, "?v=")[1], entry.DownloadURL)
		previous = entry.ID
	}
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	for _, forbidden := range []string{"source_id", "media_id", "moment_id", "decision", "faces", "exif"} {
		require.NotContains(t, string(encoded), forbidden)
	}
	videos, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "VIDEO", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, videos.Entries, 1)
	clip := videos.Entries[0]
	clipVersion := strings.Split(clip.ThumbnailURL, "?v=")[1]
	require.Equal(t, "/api/media/viewer/"+curator.ID.String()+"/entries/"+clip.ID+"/original?v="+clipVersion, clip.DownloadURL)
	require.Equal(t, "/api/media/viewer/"+curator.ID.String()+"/entries/"+clip.ID+"/playback?v="+clipVersion, clip.PlaybackURL)
	require.Equal(t, "pending", clip.ChapterStatus, "an unprobed video is pending, never failed")
	require.Empty(t, clip.Chapters)
	require.Empty(t, first.Entries[0].PlaybackURL, "photos have no playback")
	require.Empty(t, first.Entries[0].ChapterStatus)
	_, err = module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{Cursor: strings.Repeat("x", 5000)})
	require.Error(t, err)

	libraryFirst, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", publishing.EntryPageRequest{From: "2026-07-05", To: "2026-07-06"})
	require.NoError(t, err)
	require.Len(t, libraryFirst.Entries, 500)
	require.NotEmpty(t, libraryFirst.NextCursor)
	librarySecond, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", publishing.EntryPageRequest{From: "2026-07-05", To: "2026-07-06", Cursor: libraryFirst.NextCursor})
	require.NoError(t, err)
	require.Len(t, librarySecond.Entries, 2)
	require.Empty(t, librarySecond.NextCursor)
	ascending := append(first.Entries, second.Entries...)
	descending := append(libraryFirst.Entries, librarySecond.Entries...)
	for i, entry := range descending {
		require.Equal(t, ascending[len(ascending)-1-i], entry)
	}
	library, err := module.ViewLibrary(t.Context(), curator.ID.String())
	require.NoError(t, err)
	require.Equal(t, 502, library.PhotoCount)
	require.Equal(t, 1, library.VideoCount)
	require.Len(t, library.Days, 1)
	for i, ratio := range library.Days[0].PhotoRatios {
		require.Equal(t, viewed.Days[0].PhotoRatios[501-i], ratio)
	}
	libraryVideos, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "VIDEO", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Equal(t, videos, libraryVideos)
	for _, page := range []publishing.EntryPageRequest{{To: "2026-07-05"}, {From: "2026-07-06"}} {
		empty, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", page)
		require.NoError(t, err)
		require.Empty(t, empty.Entries)
	}
	for _, page := range []publishing.EntryPageRequest{{From: "yesterday"}, {To: "tomorrow"}, {Cursor: strings.Repeat("x", 5000)}} {
		_, err := module.ViewLibraryEntries(t.Context(), curator.ID.String(), "IMAGE", page)
		require.Error(t, err)
	}
	_, err = module.ViewLibraryEntries(t.Context(), curator.ID.String(), "invalid", publishing.EntryPageRequest{})
	require.Error(t, err)
	_, err = module.ViewEntries(t.Context(), curator.ID.String(), "", "", "IMAGE", publishing.EntryPageRequest{})
	require.Error(t, err, "an empty Album ID must not expose the library")
}

func TestViewerListingAndMediaAuthorizationRespectPublicationAndAlbumEntry(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveMomentRules(t.Context(), album.ID, album.Moments[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	listed, err := module.ViewAlbums(t.Context(), person.ID.String())
	require.NoError(t, err)
	require.Empty(t, listed)
	allowedEntry := album.Moments[1].Entries[0].ID
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", allowedEntry))
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), allowedEntry))
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), album.Moments[0].Entries[0].ID))
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	listed, err = module.ViewAlbums(t.Context(), person.ID.String())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, 2, listed[0].PhotoCount)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", allowedEntry))
	_, err = db.NewUpdate().Model(&person).Set("deactivated_at = ?", time.Now().UTC()).WherePK().Exec(t.Context())
	require.NoError(t, err)
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), person.ID.String(), "", allowedEntry))
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), allowedEntry))
	// A deactivated Person sees nothing, and cannot be previewed either.
	_, err = module.ViewAlbum(t.Context(), person.ID.String(), "", album.ID)
	require.Error(t, err)
	_, err = module.ViewAlbums(t.Context(), person.ID.String())
	require.Error(t, err)
	_, err = module.ViewEntries(t.Context(), person.ID.String(), "", album.ID, "IMAGE", publishing.EntryPageRequest{})
	require.Error(t, err)
	_, err = module.ViewAlbum(t.Context(), curator.ID.String(), person.ID.String(), album.ID)
	require.Error(t, err)
}

func TestConfiguredCoversSkipDeniedEntriesAndNeverPickAnArbitraryPhoto(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.assets[2].Kind = "VIDEO"
	source.assets[2].Filename = "first.mp4"
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	firstCover := album.Moments[0].CoverEntryID
	videoCover := album.Moments[1].CoverEntryID
	_, err = module.SaveEntryRules(t.Context(), album.ID, firstCover, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	view, err := module.ViewAlbum(t.Context(), curator.ID.String(), person.ID.String(), album.ID)
	require.NoError(t, err)
	require.Contains(t, view.CoverURL, videoCover)
	require.Equal(t, 1, view.PhotoCount)
	require.Equal(t, 1, view.VideoCount)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), videoCover))
	_, err = module.SaveEntryRules(t.Context(), album.ID, videoCover, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	view, err = module.ViewAlbum(t.Context(), curator.ID.String(), person.ID.String(), album.ID)
	require.NoError(t, err)
	require.Equal(t, 1, view.PhotoCount)
	require.Empty(t, view.CoverURL, "an accessible non-cover photo cannot become the Album cover")
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), videoCover))
	page, err := module.ViewEntries(t.Context(), curator.ID.String(), person.ID.String(), album.ID, "IMAGE", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.NotEqual(t, firstCover, page.Entries[0].ID)
	require.NotEmpty(t, page.Entries[0].PreviewURL)
	require.Empty(t, page.Entries[0].DownloadURL, "Curator preview cannot download")
	videos, err := module.ViewEntries(t.Context(), curator.ID.String(), person.ID.String(), album.ID, "VIDEO", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Empty(t, videos.Entries)
	_, err = module.SaveEntryRules(t.Context(), album.ID, videoCover, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionInherit}}})
	require.NoError(t, err)
	videos, err = module.ViewEntries(t.Context(), curator.ID.String(), person.ID.String(), album.ID, "VIDEO", publishing.EntryPageRequest{})
	require.NoError(t, err)
	require.Len(t, videos.Entries, 1)
	require.Contains(t, videos.Entries[0].PlaybackURL, "/api/media/preview/"+person.ID.String()+"/entries/"+videoCover+"/playback?v=", "preview plays through the selected Person's context")
	require.Empty(t, videos.Entries[0].DownloadURL, "preview keeps downloads off for videos too")
}

func TestViewerInheritanceMatchesEveryScopedDecisionCombination(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	entry := album.Moments[0].Entries[0]
	// Rows are independent expected results, ordered entry inherit/allow/deny,
	// then Moment inherit/allow/deny, then Album absent/allow.
	expected := []bool{false, true, false, true, true, false, false, true, false, true, true, false, true, true, false, false, true, false}
	index := 0
	for _, broad := range []publishing.Decision{publishing.DecisionInherit, publishing.DecisionAllow} {
		_, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: broad == publishing.DecisionAllow}}})
		require.NoError(t, err)
		for _, moment := range []publishing.Decision{publishing.DecisionInherit, publishing.DecisionAllow, publishing.DecisionDeny} {
			_, err := module.SaveMomentRules(t.Context(), album.ID, album.Moments[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: moment}}})
			require.NoError(t, err)
			for _, narrow := range []publishing.Decision{publishing.DecisionInherit, publishing.DecisionAllow, publishing.DecisionDeny} {
				_, err := module.SaveEntryRules(t.Context(), album.ID, entry.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: narrow}}})
				require.NoError(t, err)
				allowed := module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), entry.ID) == nil
				require.Equal(t, expected[index], allowed, "Album %s Moment %s Entry %s", broad, moment, narrow)
				index++
			}
		}
	}
}

func TestViewerAndPreviewUseSelectedPersonsAccess(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	sam := models.Person{ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, alex, sam}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveMomentRules(t.Context(), album.ID, album.Moments[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID.String(), Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	_, err = module.ViewAlbum(t.Context(), alex.ID.String(), "", album.ID)
	require.Error(t, err, "ordinary access remains denied before publication")
	preview, err := module.ViewAlbum(t.Context(), curator.ID.String(), alex.ID.String(), album.ID)
	require.NoError(t, err)
	require.Equal(t, 2, preview.PhotoCount)
	require.Equal(t, "2026-07-05", preview.StartDate)
	require.Contains(t, preview.CoverURL, album.Moments[1].CoverEntryID)
	require.Contains(t, preview.CoverURL, "/preview/"+alex.ID.String()+"/")
	require.Contains(t, preview.CoverPreviewURL, "/entries/"+album.Moments[1].CoverEntryID+"/preview?", "the header cover uses the larger variant")
	_, err = module.ViewAlbum(t.Context(), curator.ID.String(), sam.ID.String(), album.ID)
	require.Error(t, err, "preview must not use Curator bypass")
	_, err = module.ViewAlbum(t.Context(), alex.ID.String(), alex.ID.String(), album.ID)
	require.Error(t, err, "a Person cannot forge a preview context")
	admin, err := module.ViewAlbum(t.Context(), curator.ID.String(), "", album.ID)
	require.NoError(t, err)
	require.Equal(t, 3, admin.PhotoCount)
}
