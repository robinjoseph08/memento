package publishing_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExcludeAndIncludeExistingMedia(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	db, module, album, _ := syncedAlbums(t, source, nil)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	ids := entryIDs(album)
	shared := momentOf(album, "first.jpg")
	lonely := momentOf(album, "last.jpg")
	require.Equal(t, ids["first.jpg"], shared.CoverEntryID)

	// Keeping the cover out hands the cover to the earliest remaining item
	// and shows who loses media.
	exclude := publishing.ExcludeEntriesRequest{EntryIDs: []string{ids["first.jpg"]}}
	preview, err := module.PreviewExclude(t.Context(), album.ID, shared.ID, exclude)
	require.NoError(t, err)
	assert.True(t, preview.Ready)
	assert.False(t, preview.RemovesMoment)
	require.Len(t, preview.Changes, 1)
	assert.Equal(t, []string{ids["first.jpg"]}, preview.Changes[0].LostEntryIDs)
	_, err = module.ExcludeEntries(t.Context(), album.ID, shared.ID, exclude)
	requireCode(t, err, "audience_changed")
	exclude.ReviewToken = preview.ReviewToken
	detail, err := module.ExcludeEntries(t.Context(), album.ID, shared.ID, exclude)
	require.NoError(t, err)
	assert.False(t, hasEntry(detail, "first.jpg"))
	assert.Equal(t, ids["second.jpg"], momentOf(detail, "second.jpg").CoverEntryID)
	require.Len(t, detail.Excluded, 1)
	assert.Equal(t, ids["first.jpg"], detail.Excluded[0].ID)
	assert.Equal(t, "first.jpg", detail.Excluded[0].Filename)
	assert.True(t, detail.Excluded[0].Available)

	// Excluded media is not a change for Immich and never comes back as new.
	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.UpToDate)
	assert.Empty(t, review.Additions)

	// Keeping out a Moment's last item removes that Moment, with notice.
	exclude = publishing.ExcludeEntriesRequest{EntryIDs: []string{ids["last.jpg"]}}
	preview, err = module.PreviewExclude(t.Context(), album.ID, lonely.ID, exclude)
	require.NoError(t, err)
	assert.True(t, preview.RemovesMoment)
	exclude.ReviewToken = preview.ReviewToken
	detail, err = module.ExcludeEntries(t.Context(), album.ID, lonely.ID, exclude)
	require.NoError(t, err)
	assert.Len(t, detail.Moments, 1)
	assert.Len(t, detail.Excluded, 2)

	// Add back needs a destination: an existing Moment, or a new one for the
	// item's own capture day only.
	_, err = module.PreviewInclude(t.Context(), album.ID, ids["last.jpg"], publishing.IncludeEntryRequest{MomentID: "new:2026-07-09"})
	requireCode(t, err, "validation_error")
	_, err = module.PreviewInclude(t.Context(), album.ID, ids["second.jpg"], publishing.IncludeEntryRequest{MomentID: shared.ID})
	requireCode(t, err, "not_found")
	include := publishing.IncludeEntryRequest{MomentID: "new:2026-07-04"}
	preview, err = module.PreviewInclude(t.Context(), album.ID, ids["last.jpg"], include)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	assert.Equal(t, []string{ids["last.jpg"]}, preview.Changes[0].GainedEntryIDs)
	include.ReviewToken = preview.ReviewToken
	detail, err = module.IncludeEntry(t.Context(), album.ID, ids["last.jpg"], include)
	require.NoError(t, err)
	restored := momentOf(detail, "last.jpg")
	assert.Equal(t, "2026-07-04", restored.Date)
	assert.Equal(t, ids["last.jpg"], restored.CoverEntryID)
	assert.NotEqual(t, lonely.ID, restored.ID, "a removed Moment does not have to survive")
	include = publishing.IncludeEntryRequest{MomentID: shared.ID}
	preview, err = module.PreviewInclude(t.Context(), album.ID, ids["first.jpg"], include)
	require.NoError(t, err)
	include.ReviewToken = preview.ReviewToken
	detail, err = module.IncludeEntry(t.Context(), album.ID, ids["first.jpg"], include)
	require.NoError(t, err)
	assert.Equal(t, ids["first.jpg"], entryIDs(detail)["first.jpg"], "Add back keeps the Album Entry identity")
	assert.Equal(t, shared.ID, momentOf(detail, "first.jpg").ID)
	assert.Empty(t, detail.Excluded)
	_, err = module.IncludeEntry(t.Context(), album.ID, ids["first.jpg"], include)
	requireCode(t, err, "not_found")
}
