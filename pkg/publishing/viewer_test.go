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
	for i := range 102 {
		source.assets = append(source.assets, immich.Asset{ID: fmt.Sprintf("photo-%03d", i), Kind: "IMAGE", Filename: fmt.Sprintf("photo-%03d.jpg", i), Checksum: "YQ==", LocalDateTime: "2026-07-05T00:01:00+14:00", FileCreatedAt: "2026-07-04T10:01:00Z", UpdatedAt: "2026-07-06T00:00:00Z"})
	}
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Count: 102}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)
	first, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", "")
	require.NoError(t, err)
	require.Len(t, first.Entries, 100)
	require.NotEmpty(t, first.NextCursor)
	second, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Entries, 2)
	require.Empty(t, second.NextCursor)
	previous := ""
	for _, entry := range append(first.Entries, second.Entries...) {
		require.Greater(t, entry.ID, previous)
		require.Equal(t, "2026-07-05T00:01:00", entry.CapturedAt)
		previous = entry.ID
	}
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	for _, forbidden := range []string{"source_id", "media_id", "moment_id", "decision", "faces", "exif"} {
		require.NotContains(t, string(encoded), forbidden)
	}
	videos, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "VIDEO", "")
	require.NoError(t, err)
	require.Empty(t, videos.Entries)
	_, err = module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "IMAGE", strings.Repeat("x", 5000))
	require.Error(t, err)
}

func TestViewerListingAndMediaAuthorizationRespectPublicationAndAlbumEntry(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, album.Moments[1].ID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
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
	_, err = module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	firstCover := album.Moments[0].CoverEntryID
	videoCover := album.Moments[1].CoverEntryID
	_, err = module.SetEntryAccess(t.Context(), album.ID, firstCover, publishing.SetEntryAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	view, err := module.ViewAlbum(t.Context(), curator.ID.String(), person.ID.String(), album.ID)
	require.NoError(t, err)
	require.Contains(t, view.CoverURL, videoCover)
	require.Equal(t, 1, view.PhotoCount)
	require.Equal(t, 1, view.VideoCount)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), videoCover))
	_, err = module.SetEntryAccess(t.Context(), album.ID, videoCover, publishing.SetEntryAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	view, err = module.ViewAlbum(t.Context(), curator.ID.String(), person.ID.String(), album.ID)
	require.NoError(t, err)
	require.Equal(t, 1, view.PhotoCount)
	require.Empty(t, view.CoverURL, "an accessible non-cover photo cannot become the Album cover")
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), videoCover))
	page, err := module.ViewEntries(t.Context(), curator.ID.String(), person.ID.String(), album.ID, "IMAGE", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.NotEqual(t, firstCover, page.Entries[0].ID)
	videos, err := module.ViewEntries(t.Context(), curator.ID.String(), person.ID.String(), album.ID, "VIDEO", "")
	require.NoError(t, err)
	require.Empty(t, videos.Entries)
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
		_, err := module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: person.ID.String(), Decision: broad})
		require.NoError(t, err)
		for _, moment := range []publishing.Decision{publishing.DecisionInherit, publishing.DecisionAllow, publishing.DecisionDeny} {
			_, err := module.SetMomentAccess(t.Context(), album.ID, album.Moments[0].ID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: moment})
			require.NoError(t, err)
			for _, narrow := range []publishing.Decision{publishing.DecisionInherit, publishing.DecisionAllow, publishing.DecisionDeny} {
				_, err := module.SetEntryAccess(t.Context(), album.ID, entry.ID, publishing.SetEntryAccessRequest{PersonID: person.ID.String(), Decision: narrow})
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
	_, err = module.SetMomentAccess(t.Context(), album.ID, album.Moments[1].ID, publishing.SetMomentAccessRequest{PersonID: alex.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.ViewAlbum(t.Context(), alex.ID.String(), "", album.ID)
	require.Error(t, err, "ordinary access remains denied before publication")
	preview, err := module.ViewAlbum(t.Context(), curator.ID.String(), alex.ID.String(), album.ID)
	require.NoError(t, err)
	require.Equal(t, 2, preview.PhotoCount)
	require.Equal(t, "2026-07-05", preview.StartDate)
	require.Contains(t, preview.CoverURL, album.Moments[1].CoverEntryID)
	require.Contains(t, preview.CoverURL, "/preview/"+alex.ID.String()+"/")
	_, err = module.ViewAlbum(t.Context(), curator.ID.String(), sam.ID.String(), album.ID)
	require.Error(t, err, "preview must not use Curator bypass")
	_, err = module.ViewAlbum(t.Context(), alex.ID.String(), alex.ID.String(), album.ID)
	require.Error(t, err, "a Person cannot forge a preview context")
	admin, err := module.ViewAlbum(t.Context(), curator.ID.String(), "", album.ID)
	require.NoError(t, err)
	require.Equal(t, 3, admin.PhotoCount)
}
