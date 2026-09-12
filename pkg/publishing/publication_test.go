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

func TestPermanentAlbumDeletionKeepsSharedMediaAndRequiresTitle(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.albums["source-other"] = immich.Album{ID: "source-other", Name: "Other", Count: 3}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	// The fixture adapter serves the same media for another source Album.
	other, err := module.StartImport(t.Context(), "source-other")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), other.ID))
	other, err = module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&[]models.Person{curator, person}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SetAlbumAccess(t.Context(), other.ID, publishing.SetAlbumAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	other, err = module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), other.Moments[0].Entries[0].ID))
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), person.ID.String(), album.Moments[0].Entries[0].ID), "the same Media Item in another Album grants no access")
	err = module.DeleteAlbum(t.Context(), album.ID, publishing.DeleteAlbumRequest{Title: "wrong"})
	require.Error(t, err)
	_, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.NoError(t, module.DeleteAlbum(t.Context(), album.ID, publishing.DeleteAlbumRequest{Title: album.Title}))
	_, err = module.GetAlbum(t.Context(), album.ID)
	require.Error(t, err)
	surviving, err := module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	require.Equal(t, other, surviving)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), "", other.Moments[0].Entries[0].ID))
	require.Error(t, module.AuthorizeViewerEntry(t.Context(), curator.ID.String(), "", album.Moments[0].Entries[0].ID))
}

func TestPublicationBlocksMissingTitleAndUnassignedEntries(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	_, err := db.NewUpdate().Model((*models.Album)(nil)).Set("title = ?", "   ").Where("id = ?", album.ID).Exec(t.Context())
	require.NoError(t, err)
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Contains(t, review.Blockers, "Enter an Album title.")
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.Error(t, err)
	_, err = module.UpdateAlbum(t.Context(), album.ID, publishing.UpdateAlbumRequest{Title: "Summer"})
	require.NoError(t, err)
	// Deliberately violate membership's CHECK to exercise the defensive blocker.
	// The selected entry is not a configured cover, so cover ownership stays valid.
	_, err = db.ExecContext(t.Context(), "ALTER TABLE album_entries DROP CONSTRAINT album_entries_check")
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.AlbumEntry)(nil)).Set("moment_id = NULL").Where("id = ?", album.Moments[1].Entries[1].ID).Exec(t.Context())
	require.NoError(t, err)
	review, err = module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Contains(t, review.Blockers, "Assign every item to a Moment.")
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.Error(t, err)
	current, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.False(t, current.Published)
}

func TestPublicationBlocksEmptyAndIncompleteImportsButNotAbsentAudience(t *testing.T) {
	t.Parallel()
	source := fixture()
	source.assets = nil
	source.albums["source"] = immich.Album{ID: "source", Name: "Empty", Count: 0}
	module := publishing.New(testdb.New(t), source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Contains(t, review.Blockers, "Finish importing the Album before publishing.")
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	review, err = module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"Add at least one photo or video."}, review.Blockers)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.Error(t, err)
	_, readyModule, ready := importedAlbum(t)
	review, err = readyModule.ReviewPublication(t.Context(), ready.ID)
	require.NoError(t, err)
	require.Empty(t, review.Audience)
	require.Empty(t, review.Blockers)
	published, err := readyModule.PublishAlbum(t.Context(), ready.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	require.True(t, published.Published)
}

func TestPublicationIsExplicitAndUnpublishPreservesCuration(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Empty(t, review.Blockers)
	require.Contains(t, review.Warnings, "No ordinary Person has access yet.")
	_, err = module.SetMomentAccess(t.Context(), album.ID, album.Moments[0].ID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.Error(t, err, "changed audience requires another review")
	review, err = module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, review.Audience, 1)
	require.Equal(t, 1, review.Audience[0].PhotoCount)
	published, err := module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	require.True(t, published.Published)
	view, err := module.ViewAlbum(t.Context(), person.ID.String(), "", album.ID)
	require.NoError(t, err)
	require.Equal(t, 1, view.PhotoCount)
	hidden, err := module.UnpublishAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.False(t, hidden.Published)
	require.Equal(t, published.Moments, hidden.Moments)
	_, err = module.ViewAlbum(t.Context(), person.ID.String(), "", album.ID)
	require.Error(t, err)
	review, err = module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
}
