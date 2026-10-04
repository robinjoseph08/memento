package publishing_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// offerFixture is an imported Album with a Curator, Circles, and People to
// offer it to. Moments[0] holds one photo and Moments[1] holds two.
type offerFixture struct {
	db      *bun.DB
	module  *publishing.Module
	album   publishing.AlbumDetail
	curator models.Person
}

func newOfferFixture(t *testing.T) offerFixture {
	t.Helper()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)
	return offerFixture{db: db, module: module, album: album, curator: curator}
}

func (f offerFixture) person(t *testing.T, name string) string {
	t.Helper()
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: name, CreatedAt: time.Now().UTC()}
	_, err := f.db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	return person.ID.String()
}

func (f offerFixture) circle(t *testing.T, name string, people ...string) string {
	t.Helper()
	circle := models.Circle{ID: models.NewUUIDv7(), Name: name, CreatedAt: time.Now().UTC()}
	_, err := f.db.NewInsert().Model(&circle).Exec(t.Context())
	require.NoError(t, err)
	for _, person := range people {
		f.join(t, circle.ID.String(), person)
	}
	return circle.ID.String()
}

func (f offerFixture) join(t *testing.T, circleID, personID string) {
	t.Helper()
	member := models.CircleMember{CircleID: models.UUID(uuid.MustParse(circleID)), PersonID: models.UUID(uuid.MustParse(personID))}
	_, err := f.db.NewInsert().Model(&member).Exec(t.Context())
	require.NoError(t, err)
}

func (f offerFixture) leave(t *testing.T, circleID, personID string) {
	t.Helper()
	_, err := f.db.NewDelete().Model((*models.CircleMember)(nil)).Where("circle_id = ? AND person_id = ?", circleID, personID).Exec(t.Context())
	require.NoError(t, err)
}

func (f offerFixture) offer(t *testing.T, offered bool, circleIDs ...string) publishing.AlbumDetail {
	t.Helper()
	request := publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{}}
	for _, id := range circleIDs {
		request.Circles = append(request.Circles, publishing.AlbumOfferChoice{CircleID: id, Offered: offered})
	}
	saved, err := f.module.SaveAlbumAccess(t.Context(), f.album.ID, request)
	require.NoError(t, err)
	return saved
}

func (f offerFixture) publish(t *testing.T) publishing.PublicationReview {
	t.Helper()
	review, err := f.module.ReviewPublication(t.Context(), f.album.ID)
	require.NoError(t, err)
	_, err = f.module.PublishAlbum(t.Context(), f.album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	return review
}

func (f offerFixture) rule(t *testing.T, personID, momentID, entryID string, decision publishing.Decision) {
	t.Helper()
	request := publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: personID, Decision: decision}}}
	var err error
	if entryID != "" {
		_, err = f.module.SaveEntryRules(t.Context(), f.album.ID, entryID, request)
	} else {
		_, err = f.module.SaveMomentRules(t.Context(), f.album.ID, momentID, request)
	}
	require.NoError(t, err)
}

// moreAlbums lists the photo count of each Album in the Person's More albums.
func (f offerFixture) moreAlbums(t *testing.T, personID string) map[string]int {
	t.Helper()
	albums, err := f.module.ViewMoreAlbums(t.Context(), personID)
	require.NoError(t, err)
	result := map[string]int{}
	for _, album := range albums {
		result[album.ID] = album.PhotoCount
	}
	return result
}

// offeredEntries lists the photos a Person's preview of the Album shows, or
// nil when the preview is not available to them.
func (f offerFixture) offeredEntries(t *testing.T, personID string) []string {
	t.Helper()
	_, err := f.module.ViewOfferedAlbum(t.Context(), personID, f.album.ID)
	if err != nil {
		require.ErrorIs(t, err, errcodes.NotFound("Album"))
		return nil
	}
	page, err := f.module.ViewOfferedEntries(t.Context(), personID, f.album.ID, "IMAGE", publishing.EntryPageRequest{})
	require.NoError(t, err)
	ids := []string{}
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func (f offerFixture) authorized(t *testing.T, personID, entryID string) bool {
	t.Helper()
	err := f.module.AuthorizeViewerEntry(t.Context(), personID, "", entryID)
	if err != nil {
		require.ErrorIs(t, err, errcodes.NotFound("Thumbnail"))
	}
	return err == nil
}

func (f offerFixture) entries(moment int) []string {
	ids := []string{}
	for _, entry := range f.album.Moments[moment].Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestAlbumOffersFillOnlyTheGapsPersonDecisionsLeave(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, cousin, denied, skipped, granted, outsider := f.person(t, "Grandma"), f.person(t, "Cousin"), f.person(t, "Denied"), f.person(t, "Skipped"), f.person(t, "Granted"), f.person(t, "Outsider")
	extended := f.circle(t, "Extended family", grandma, cousin, denied, skipped, granted)
	college := f.circle(t, "College friends", cousin)
	all := append(f.entries(0), f.entries(1)...)
	first, later := f.album.Moments[0], f.album.Moments[1]
	f.rule(t, denied, later.ID, "", publishing.DecisionDeny)
	f.rule(t, skipped, "", later.Entries[1].ID, publishing.DecisionDeny)
	f.rule(t, granted, first.ID, "", publishing.DecisionAllow)

	preview, err := f.module.PreviewAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: extended, Offered: true}}})
	require.NoError(t, err)
	offeredGains := map[string]int{}
	for _, change := range preview.Changes {
		require.Empty(t, change.GainedEntryIDs, "an Offer grants nothing directly")
		require.Empty(t, change.LostEntryIDs)
		offeredGains[change.DisplayName] = len(change.OfferedGainedEntryIDs)
	}
	require.Equal(t, map[string]int{"Grandma": 3, "Cousin": 3, "Denied": 1, "Skipped": 2, "Granted": 2}, offeredGains)

	saved := f.offer(t, true, extended)
	require.Len(t, saved.Circles, 2)
	require.Equal(t, "College friends", saved.Circles[0].Name)
	require.False(t, saved.Circles[0].Offered)
	require.Equal(t, 1, saved.Circles[0].MemberCount)
	require.Equal(t, publishing.AlbumCircle{CircleID: extended, Name: "Extended family", Offered: true, MemberCount: 5}, saved.Circles[1])

	// Offers apply only while the Album is published.
	require.Empty(t, f.moreAlbums(t, grandma))
	require.Nil(t, f.offeredEntries(t, grandma))
	require.False(t, f.authorized(t, grandma, all[0]))

	review := f.publish(t)
	audience := map[string][2]int{}
	for _, person := range review.Audience {
		audience[person.DisplayName] = [2]int{person.AccessibleCount, person.OfferedCount}
	}
	require.Equal(t, map[string][2]int{"Grandma": {0, 3}, "Cousin": {0, 3}, "Denied": {0, 1}, "Skipped": {0, 2}, "Granted": {1, 2}}, audience)

	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, grandma))
	require.ElementsMatch(t, all, f.offeredEntries(t, grandma))
	for _, id := range all {
		require.True(t, f.authorized(t, grandma, id))
	}
	// Offered media stays out of the Person's own Albums and Library, and a
	// direct link to their own Album finds nothing.
	own, err := f.module.ViewAlbums(t.Context(), grandma)
	require.NoError(t, err)
	require.Empty(t, own)
	library, err := f.module.ViewLibrary(t.Context(), grandma)
	require.NoError(t, err)
	require.Zero(t, library.PhotoCount)
	_, err = f.module.ViewAlbum(t.Context(), grandma, "", f.album.ID)
	require.ErrorIs(t, err, errcodes.NotFound("Album"))

	// A Moment or Album Entry deny for the Person beats the Offer.
	require.Equal(t, map[string]int{f.album.ID: 1}, f.moreAlbums(t, denied))
	require.Equal(t, f.entries(0), f.offeredEntries(t, denied))
	require.False(t, f.authorized(t, denied, later.Entries[0].ID))
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, skipped))
	require.False(t, f.authorized(t, skipped, later.Entries[1].ID))

	// Media granted directly stays in the Person's own Album; the rest is offered.
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, granted))
	require.ElementsMatch(t, all, f.offeredEntries(t, granted), "the preview shows the Album as joining would")
	own, err = f.module.ViewAlbums(t.Context(), granted)
	require.NoError(t, err)
	require.Len(t, own, 1)
	require.Equal(t, 1, own[0].PhotoCount)

	// People outside every offered Circle, and Curators, have nothing offered.
	require.Empty(t, f.moreAlbums(t, outsider))
	require.Nil(t, f.offeredEntries(t, outsider))
	require.False(t, f.authorized(t, outsider, all[0]))
	require.Empty(t, f.moreAlbums(t, f.curator.ID.String()))
	f.join(t, extended, f.curator.ID.String())
	require.Empty(t, f.moreAlbums(t, f.curator.ID.String()), "a Curator sees everything as their own")
	require.Nil(t, f.offeredEntries(t, f.curator.ID.String()))
	// A Curator's preview as a Person shows only that Person's own media.
	err = f.module.AuthorizeViewerEntry(t.Context(), f.curator.ID.String(), grandma, all[0])
	require.ErrorIs(t, err, errcodes.NotFound("Thumbnail"))
	// A deactivated member is offered nothing.
	_, err = f.db.NewUpdate().Model((*models.Person)(nil)).Set("deactivated_at = ?", time.Now().UTC()).Where("id = ?", cousin).Exec(t.Context())
	require.NoError(t, err)
	_, err = f.module.ViewMoreAlbums(t.Context(), cousin)
	require.Error(t, err)
	require.Error(t, f.module.AuthorizeViewerEntry(t.Context(), cousin, "", all[0]))
	_, err = f.db.NewUpdate().Model((*models.Person)(nil)).Set("deactivated_at = NULL").Where("id = ?", cousin).Exec(t.Context())
	require.NoError(t, err)

	// Membership is read live.
	f.join(t, extended, outsider)
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, outsider))
	f.leave(t, extended, outsider)
	require.Empty(t, f.moreAlbums(t, outsider))
	require.False(t, f.authorized(t, outsider, all[0]))

	// Excluded Media is never offered.
	exclude := publishing.ExcludeEntriesRequest{EntryIDs: []string{later.Entries[0].ID}}
	excludePreview, err := f.module.PreviewExclude(t.Context(), f.album.ID, later.ID, exclude)
	require.NoError(t, err)
	exclude.ReviewToken = excludePreview.ReviewToken
	_, err = f.module.ExcludeEntries(t.Context(), f.album.ID, later.ID, exclude)
	require.NoError(t, err)
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, grandma))
	require.False(t, f.authorized(t, grandma, later.Entries[0].ID))

	// An Offer to any of a Person's Circles counts.
	f.offer(t, true, college)
	withdrawn, err := f.module.PreviewAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: extended, Offered: false}}})
	require.NoError(t, err)
	losses := map[string]int{}
	for _, change := range withdrawn.Changes {
		losses[change.DisplayName] = len(change.OfferedLostEntryIDs)
	}
	require.Equal(t, map[string]int{"Grandma": 2, "Denied": 1, "Skipped": 1, "Granted": 1}, losses)
	f.offer(t, false, extended)
	require.Empty(t, f.moreAlbums(t, grandma))
	require.Nil(t, f.offeredEntries(t, grandma))
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, cousin))

	// Unpublishing withdraws every Offer from view without forgetting it.
	_, err = f.module.UnpublishAlbum(t.Context(), f.album.ID)
	require.NoError(t, err)
	require.Empty(t, f.moreAlbums(t, cousin))
	require.False(t, f.authorized(t, cousin, all[0]))
}

func TestAnOfferAloneMakesAnAlbumReadyToPublish(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	empty := f.circle(t, "Neighbors")
	ready := func() bool {
		albums, err := f.module.ListAlbums(t.Context(), "")
		require.NoError(t, err)
		require.Len(t, albums, 1)
		return albums[0].Ready
	}
	require.False(t, ready())
	f.offer(t, true, empty)
	require.True(t, ready(), "membership is live, so an empty Circle still counts")
	_, err := f.module.PreviewAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: models.NewUUIDv7().String(), Offered: true}}})
	requireInvalid(t, err)
	_, err = f.module.SaveAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: empty, Offered: true}, {CircleID: empty, Offered: false}}})
	requireInvalid(t, err)
}

func TestAPublicationReviewGoesStaleWhenOffersChange(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma := f.person(t, "Grandma")
	extended := f.circle(t, "Extended family", grandma)
	review, err := f.module.ReviewPublication(t.Context(), f.album.ID)
	require.NoError(t, err)
	f.offer(t, true, extended)
	_, err = f.module.PublishAlbum(t.Context(), f.album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.Error(t, err, "the Offer changed who publication reaches")
	review, err = f.module.ReviewPublication(t.Context(), f.album.ID)
	require.NoError(t, err)
	f.circle(t, "College friends", grandma)
	_, err = f.module.PublishAlbum(t.Context(), f.album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err, "a Circle the Album is not offered to changes nothing")
}

func TestMoreAlbumsCoverComesFromOfferedMomentsInCoverOrder(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, granted := f.person(t, "Grandma"), f.person(t, "Granted")
	extended := f.circle(t, "Extended family", grandma, granted)
	first, later := f.album.Moments[0], f.album.Moments[1]
	f.rule(t, granted, first.ID, "", publishing.DecisionAllow)
	f.offer(t, true, extended)
	f.publish(t)
	cover := func(personID string) string {
		t.Helper()
		albums, err := f.module.ViewMoreAlbums(t.Context(), personID)
		require.NoError(t, err)
		require.Len(t, albums, 1)
		return albums[0].CoverURL
	}
	require.Contains(t, cover(grandma), first.CoverEntryID, "capture order without a Cover Order")
	require.Contains(t, cover(granted), later.CoverEntryID, "only offered Moments compete")
	_, err := f.module.SaveCoverOrder(t.Context(), f.album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{later.ID, first.ID}})
	require.NoError(t, err)
	require.Contains(t, cover(grandma), later.CoverEntryID)
	_, err = f.module.SaveCoverOrder(t.Context(), f.album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{first.ID, later.ID}})
	require.NoError(t, err)
	require.Contains(t, cover(grandma), first.CoverEntryID)
	require.Contains(t, cover(granted), later.CoverEntryID, "a directly granted Moment never covers More albums")
	own, err := f.module.ViewAlbums(t.Context(), granted)
	require.NoError(t, err)
	require.Contains(t, own[0].CoverURL, first.CoverEntryID)
}

func TestRemoveAllAccessPreviewShowsWhatAnOfferStillCovers(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma := f.person(t, "Grandma")
	extended := f.circle(t, "Extended family", grandma)
	f.rule(t, grandma, f.album.Moments[1].ID, "", publishing.DecisionAllow)
	f.offer(t, true, extended)
	preview, err := f.module.PreviewRemoveAccess(t.Context(), f.album.ID, publishing.RemoveAccessPreviewRequest{PersonID: grandma})
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	require.ElementsMatch(t, f.entries(1), preview.Changes[0].LostEntryIDs)
	require.ElementsMatch(t, f.entries(1), preview.Changes[0].OfferedGainedEntryIDs)
}

func requireInvalid(t *testing.T, err error) {
	t.Helper()
	invalid, ok := errors.AsType[*errcodes.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, 422, invalid.HTTPCode)
}
