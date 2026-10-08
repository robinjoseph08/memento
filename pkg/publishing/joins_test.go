package publishing_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
)

// ownAlbum is the Person's own view of the fixture Album, or false when it
// is not among their Albums.
func (f offerFixture) ownAlbum(t *testing.T, personID string) (publishing.ViewerAlbum, bool) {
	t.Helper()
	album, err := f.module.ViewAlbum(t.Context(), personID, "", f.album.ID)
	if err != nil {
		require.ErrorIs(t, err, errcodes.NotFound("Album"))
		return album, false
	}
	albums, err := f.module.ViewAlbums(t.Context(), personID)
	require.NoError(t, err)
	require.Len(t, albums, 1)
	require.Equal(t, f.album.ID, albums[0].ID)
	return album, true
}

func (f offerFixture) libraryPhotos(t *testing.T, personID string) int {
	t.Helper()
	library, err := f.module.ViewLibrary(t.Context(), personID)
	require.NoError(t, err)
	return library.PhotoCount
}

// joined lists how many items each Person tagged "Joined" on the access
// page can see in the Album.
func (f offerFixture) joined(t *testing.T) map[string]int {
	t.Helper()
	album, err := f.module.GetAlbum(t.Context(), f.album.ID)
	require.NoError(t, err)
	result := map[string]int{}
	for _, person := range album.Access {
		if person.Joined {
			result[person.DisplayName] = person.AccessibleCount
		}
	}
	return result
}

func TestJoiningAnOfferedAlbumMakesItTheViewersOwn(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, outsider := f.person(t, "Grandma"), f.person(t, "Outsider")
	extended := f.circle(t, "Extended family", grandma)
	f.offer(t, true, extended)
	f.publish(t)

	require.ErrorIs(t, f.module.JoinAlbum(t.Context(), outsider, f.album.ID), errcodes.NotFound("Album"), "nothing is offered to them")
	require.ErrorIs(t, f.module.JoinAlbum(t.Context(), grandma, "not-a-uuid"), errcodes.NotFound("Album"))
	require.Empty(t, f.joined(t))

	require.ErrorIs(t, f.module.JoinAlbum(t.Context(), f.curator.ID.String(), f.album.ID), errcodes.NotFound("Album"), "a Curator has nothing to join")
	require.NoError(t, f.module.JoinAlbum(t.Context(), grandma, f.album.ID))
	require.NoError(t, f.module.JoinAlbum(t.Context(), grandma, f.album.ID), "joining twice changes nothing")
	review, err := f.module.ReviewPublication(t.Context(), f.album.ID)
	require.NoError(t, err)
	require.Len(t, review.Audience, 1)
	require.Equal(t, [2]int{3, 0}, [2]int{review.Audience[0].AccessibleCount, review.Audience[0].OfferedCount}, "joined media is Grandma's own")
	own, ok := f.ownAlbum(t, grandma)
	require.True(t, ok)
	require.Equal(t, 3, own.PhotoCount)
	require.True(t, own.Joined)
	require.False(t, own.HasOwnMedia, "nothing was shared directly")
	require.Zero(t, own.MorePhotoCount+own.MoreVideoCount)
	require.Equal(t, 3, f.libraryPhotos(t, grandma))
	require.Empty(t, f.moreAlbums(t, grandma))
	require.Nil(t, f.offeredEntries(t, grandma), "a joined Album has no preview")
	require.Equal(t, map[string]int{"Grandma": 3}, f.joined(t))
	// A deny for the Person still beats the Offer they joined.
	f.rule(t, grandma, "", f.album.Moments[1].Entries[0].ID, publishing.DecisionDeny)
	own, _ = f.ownAlbum(t, grandma)
	require.Equal(t, 2, own.PhotoCount)
	f.rule(t, grandma, "", f.album.Moments[1].Entries[0].ID, publishing.DecisionInherit)
	// Joined media is the Person's own when a Curator previews them too.
	preview, err := f.module.ViewAlbum(t.Context(), f.curator.ID.String(), grandma, f.album.ID)
	require.NoError(t, err)
	require.Equal(t, 3, preview.PhotoCount)
	visible, err := f.module.VisibleEntries(t.Context(), f.db, grandma)
	require.NoError(t, err)
	require.Empty(t, visible, "joining announces nothing")

	left, err := f.module.LeaveAlbum(t.Context(), grandma, f.album.ID)
	require.NoError(t, err)
	require.False(t, left.Kept, "nothing of their own is left")
	_, err = f.module.LeaveAlbum(t.Context(), grandma, f.album.ID)
	require.NoError(t, err, "leaving twice changes nothing")
	_, ok = f.ownAlbum(t, grandma)
	require.False(t, ok)
	require.Zero(t, f.libraryPhotos(t, grandma))
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, grandma))
	require.Empty(t, f.joined(t))
}

func TestJoiningAnAlbumWithDirectAccessMergesTheOfferedMedia(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	granted := f.person(t, "Granted")
	extended := f.circle(t, "Extended family", granted)
	first := f.album.Moments[0]
	f.rule(t, granted, first.ID, "", publishing.DecisionAllow)
	f.offer(t, true, extended)
	f.publish(t)

	more, err := f.module.ViewMoreAlbums(t.Context(), granted)
	require.NoError(t, err)
	require.Len(t, more, 1)
	require.Equal(t, 2, more[0].PhotoCount, "only the media beyond their own")
	require.True(t, more[0].HasOwnMedia)
	own, ok := f.ownAlbum(t, granted)
	require.True(t, ok)
	require.Equal(t, 1, own.PhotoCount)
	require.Equal(t, [2]int{2, 0}, [2]int{own.MorePhotoCount, own.MoreVideoCount})
	require.False(t, own.Joined)

	require.NoError(t, f.module.JoinAlbum(t.Context(), granted, f.album.ID))
	require.Equal(t, map[string]int{"Granted": 3}, f.joined(t), "direct and joined media both count")
	own, ok = f.ownAlbum(t, granted)
	require.True(t, ok)
	require.Equal(t, 3, own.PhotoCount)
	require.Zero(t, own.MorePhotoCount+own.MoreVideoCount)
	require.True(t, own.Joined)
	require.True(t, own.HasOwnMedia, "part of it was shared directly")
	require.Equal(t, 3, f.libraryPhotos(t, granted))
	require.Empty(t, f.moreAlbums(t, granted))

	left, err := f.module.LeaveAlbum(t.Context(), granted, f.album.ID)
	require.NoError(t, err)
	require.True(t, left.Kept, "the directly granted Moment keeps the Album theirs")
	own, ok = f.ownAlbum(t, granted)
	require.True(t, ok, "the directly granted Moment stays")
	require.Equal(t, 1, own.PhotoCount)
	require.Equal(t, [2]int{2, 0}, [2]int{own.MorePhotoCount, own.MoreVideoCount})
	require.Equal(t, 1, f.libraryPhotos(t, granted))
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, granted))

	// An Album with nothing offered beyond their own has no line to follow.
	f.offer(t, false, extended)
	own, ok = f.ownAlbum(t, granted)
	require.True(t, ok)
	require.Zero(t, own.MorePhotoCount+own.MoreVideoCount)
}

func TestAJoinOutlivesTheOffersBehindIt(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma := f.person(t, "Grandma")
	extended := f.circle(t, "Extended family", grandma)
	later := f.album.Moments[1]
	f.offer(t, true, extended)
	f.publish(t)

	// Media kept out of the Album when Grandma joins comes back in a new
	// Moment, which the Album Offer covers without another Join.
	excluded := later.Entries[0]
	exclude := publishing.ExcludeEntriesRequest{EntryIDs: []string{excluded.ID}}
	excludePreview, err := f.module.PreviewExclude(t.Context(), f.album.ID, later.ID, exclude)
	require.NoError(t, err)
	exclude.ReviewToken = excludePreview.ReviewToken
	_, err = f.module.ExcludeEntries(t.Context(), f.album.ID, later.ID, exclude)
	require.NoError(t, err)
	require.NoError(t, f.module.JoinAlbum(t.Context(), grandma, f.album.ID))
	own, ok := f.ownAlbum(t, grandma)
	require.True(t, ok)
	require.Equal(t, 2, own.PhotoCount)
	include := publishing.IncludeEntryRequest{MomentID: "new:" + excluded.CapturedAt[:10]}
	includePreview, err := f.module.PreviewInclude(t.Context(), f.album.ID, excluded.ID, include)
	require.NoError(t, err)
	include.ReviewToken = includePreview.ReviewToken
	_, err = f.module.IncludeEntry(t.Context(), f.album.ID, excluded.ID, include)
	require.NoError(t, err)
	own, ok = f.ownAlbum(t, grandma)
	require.True(t, ok)
	require.Equal(t, 3, own.PhotoCount)

	// Withdrawing the Offer takes the Album away and the "Joined" tag with
	// it, while the Join waits for the Offer to return. The Curator's review
	// reads it as media leaving Grandma's own Album.
	withdrawn, err := f.module.PreviewAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: extended, Offered: false}}})
	require.NoError(t, err)
	require.Len(t, withdrawn.Changes, 1)
	require.Len(t, withdrawn.Changes[0].LostEntryIDs, 3)
	require.Empty(t, withdrawn.Changes[0].OfferedLostEntryIDs)
	f.offer(t, false, extended)
	_, ok = f.ownAlbum(t, grandma)
	require.False(t, ok)
	require.Empty(t, f.moreAlbums(t, grandma))
	require.Empty(t, f.joined(t))
	f.offer(t, true, extended)
	own, ok = f.ownAlbum(t, grandma)
	require.True(t, ok)
	require.Equal(t, 3, own.PhotoCount)
	require.Empty(t, f.moreAlbums(t, grandma))
	require.Equal(t, map[string]int{"Grandma": 3}, f.joined(t))

	// Leaving the Circle works the same way.
	f.removeMember(t, extended, grandma)
	_, ok = f.ownAlbum(t, grandma)
	require.False(t, ok)
	f.addMember(t, extended, grandma)
	_, ok = f.ownAlbum(t, grandma)
	require.True(t, ok)
}

func TestViewingGroupsFollowOffersAndJoins(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, granted, joiner := f.person(t, "Grandma"), f.person(t, "Granted"), f.person(t, "Joiner")
	f.person(t, "Outsider")
	extended := f.circle(t, "Extended family", grandma, granted, joiner)
	first, later := f.album.Moments[0], f.album.Moments[1]
	f.rule(t, granted, first.ID, "", publishing.DecisionAllow)
	f.offer(t, true, extended)
	f.publish(t)
	require.NoError(t, f.module.JoinAlbum(t.Context(), joiner, f.album.ID))

	groups := func() map[string][]string {
		t.Helper()
		result, err := f.module.ViewingGroups(t.Context(), f.album.ID)
		require.NoError(t, err)
		require.Empty(t, result.Placeholder)
		byName := map[string][]string{}
		for _, group := range result.Groups {
			for _, person := range group.People {
				byName[person.DisplayName] = group.MomentIDs
			}
		}
		return byName
	}
	// Someone who hasn't joined sees the offered covers in More albums, but
	// their own Album's covers come first when they have one.
	require.Equal(t, map[string][]string{
		"Grandma": {first.ID, later.ID},
		"Granted": {first.ID},
		"Joiner":  {first.ID, later.ID},
	}, groups())
	require.NoError(t, f.module.JoinAlbum(t.Context(), granted, f.album.ID))
	require.Equal(t, []string{first.ID, later.ID}, groups()["Granted"])
}
