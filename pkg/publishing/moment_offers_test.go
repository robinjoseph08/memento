package publishing_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
)

// momentOffer saves one Circle's decision for a Moment.
func (f offerFixture) momentOffer(t *testing.T, momentID, circleID string, decision publishing.OfferDecision) publishing.AlbumDetail {
	t.Helper()
	saved, err := f.module.SaveMomentRules(t.Context(), f.album.ID, momentID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{},
		Circles: []publishing.CircleResolution{{CircleID: circleID, Decision: decision}}})
	require.NoError(t, err)
	return saved
}

func momentCircle(moment publishing.Moment, name string) publishing.MomentCircle {
	for _, circle := range moment.Access.Circles {
		if circle.Name == name {
			return circle
		}
	}
	return publishing.MomentCircle{}
}

func momentPerson(moment publishing.Moment, personID string) publishing.AccessPerson {
	for _, person := range moment.Access.People {
		if person.PersonID == personID {
			return person
		}
	}
	return publishing.AccessPerson{}
}

func TestAMomentOfferSharesOneMomentWithoutTheAlbum(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, denied, outsider := f.person(t, "Grandma"), f.person(t, "Denied"), f.person(t, "Outsider")
	extended := f.circle(t, "Extended family", grandma, denied)
	f.circle(t, "College friends")
	first, later := f.album.Moments[0], f.album.Moments[1]
	ready := func() bool {
		albums, err := f.module.ListAlbums(t.Context(), "")
		require.NoError(t, err)
		return albums[0].Ready
	}

	// A withhold alone offers nothing, so it does not make the Album ready.
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionWithhold)
	require.False(t, ready())

	saved := f.momentOffer(t, first.ID, extended, publishing.OfferDecisionOffer)
	require.True(t, ready(), "a Moment Offer alone makes the Album ready")
	require.Equal(t, publishing.MomentCircle{CircleID: extended, Name: "Extended family", MemberCount: 2, Decision: publishing.OfferDecisionOffer, Offered: true}, momentCircle(saved.Moments[0], "Extended family"))
	require.Equal(t, publishing.MomentCircle{CircleID: extended, Name: "Extended family", MemberCount: 2, Decision: publishing.OfferDecisionWithhold}, momentCircle(saved.Moments[1], "Extended family"))
	require.Empty(t, momentCircle(saved.Moments[0], "College friends").Decision)
	require.Equal(t, []string{"Extended family"}, momentPerson(saved.Moments[0], grandma).OfferingCircles)
	require.Empty(t, momentPerson(saved.Moments[1], grandma).OfferingCircles)

	// A Person's own deny beats the Moment Offer.
	f.rule(t, denied, first.ID, "", publishing.DecisionDeny)
	f.publish(t)
	require.Equal(t, map[string]int{f.album.ID: 1}, f.moreAlbums(t, grandma))
	require.Equal(t, f.entries(0), f.offeredEntries(t, grandma))
	require.True(t, f.authorized(t, grandma, first.Entries[0].ID))
	require.False(t, f.authorized(t, grandma, later.Entries[0].ID))
	require.Empty(t, f.moreAlbums(t, denied))
	require.False(t, f.authorized(t, denied, first.Entries[0].ID))
	require.Empty(t, f.moreAlbums(t, outsider))

	// Inherit removes the Moment decision; the Album is not offered, so
	// nothing is left.
	f.momentOffer(t, first.ID, extended, publishing.OfferDecisionInherit)
	require.Empty(t, f.moreAlbums(t, grandma))

	offers, err := f.module.ListCircleOffers(t.Context())
	require.NoError(t, err)
	require.Empty(t, offers)
	f.momentOffer(t, first.ID, extended, publishing.OfferDecisionOffer)
	offers, err = f.module.ListCircleOffers(t.Context())
	require.NoError(t, err)
	require.Equal(t, []publishing.CircleOffers{{CircleID: extended, Albums: []publishing.OfferedAlbum{{ID: f.album.ID, Title: f.album.Title}}}}, offers, "a Moment Offer alone lists the Album, so deleting the Circle names it")
	_, err = f.db.NewDelete().Model((*models.Circle)(nil)).Where("id = ?", extended).Exec(t.Context())
	require.NoError(t, err)
	require.Empty(t, f.moreAlbums(t, grandma), "deleting the Circle withdraws its Moment Offers")
}

func TestAWithheldMomentStaysHiddenOnlyFromThatCircle(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, cousin, granted := f.person(t, "Grandma"), f.person(t, "Cousin"), f.person(t, "Granted")
	extended := f.circle(t, "Extended family", grandma, cousin, granted)
	college := f.circle(t, "College friends")
	later := f.album.Moments[1]
	f.rule(t, granted, later.ID, "", publishing.DecisionAllow)
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionWithhold)

	// The "Visibility after saving" preview of an Album Offer leaves out the
	// withheld Moment.
	preview, err := f.module.PreviewAlbumAccess(t.Context(), f.album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: extended, Offered: true}}})
	require.NoError(t, err)
	offered := map[string][]string{}
	for _, change := range preview.Changes {
		offered[change.DisplayName] = change.OfferedGainedEntryIDs
	}
	require.Equal(t, map[string][]string{"Grandma": f.entries(0), "Cousin": f.entries(0), "Granted": f.entries(0)}, offered)

	saved := f.offer(t, true, extended)
	require.Equal(t, publishing.MomentCircle{CircleID: extended, Name: "Extended family", MemberCount: 3, Decision: publishing.OfferDecisionWithhold, AlbumOffered: true}, momentCircle(saved.Moments[1], "Extended family"))
	require.Equal(t, publishing.MomentCircle{CircleID: extended, Name: "Extended family", MemberCount: 3, AlbumOffered: true, Offered: true}, momentCircle(saved.Moments[0], "Extended family"))
	f.publish(t)
	require.Equal(t, map[string]int{f.album.ID: 1}, f.moreAlbums(t, grandma))
	require.False(t, f.authorized(t, grandma, later.Entries[0].ID))
	// A Person-level allow still beats the withhold.
	own, err := f.module.ViewAlbums(t.Context(), granted)
	require.NoError(t, err)
	require.Len(t, own, 1)
	require.Equal(t, 2, own[0].PhotoCount)

	// A withhold for one Circle never blocks another Circle's Offer.
	f.addMember(t, college, cousin)
	f.momentOffer(t, later.ID, college, publishing.OfferDecisionOffer)
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, cousin))
	require.ElementsMatch(t, append(f.entries(0), f.entries(1)...), f.offeredEntries(t, cousin))
	require.Equal(t, map[string]int{f.album.ID: 1}, f.moreAlbums(t, grandma))

	// Removing the withhold lets the Album Offer reach the Moment again.
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionInherit)
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, grandma))
}

func TestMomentOffersRejectUnknownCirclesAndItems(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	extended := f.circle(t, "Extended family")
	moment := f.album.Moments[0]
	save := func(circles ...publishing.CircleResolution) error {
		_, err := f.module.SaveMomentRules(t.Context(), f.album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{}, Circles: circles})
		return err
	}
	requireInvalid(t, save(publishing.CircleResolution{CircleID: models.NewUUIDv7().String(), Decision: publishing.OfferDecisionOffer}))
	requireInvalid(t, save(publishing.CircleResolution{CircleID: extended, Decision: publishing.OfferDecisionOffer}, publishing.CircleResolution{CircleID: extended, Decision: publishing.OfferDecisionWithhold}))
	requireInvalid(t, save(publishing.CircleResolution{CircleID: extended, Decision: "allow"}))
	// Circles get no decisions for single items.
	_, err := f.module.SaveEntryRules(t.Context(), f.album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{},
		Circles: []publishing.CircleResolution{{CircleID: extended, Decision: publishing.OfferDecisionOffer}}})
	requireInvalid(t, err)
}

func TestSplittingAMomentCopiesItsOffers(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma := f.person(t, "Grandma")
	extended, college := f.circle(t, "Extended family", grandma), f.circle(t, "College friends")
	later := f.album.Moments[1]
	f.offer(t, true, college)
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionOffer)
	f.momentOffer(t, later.ID, college, publishing.OfferDecisionWithhold)
	f.publish(t)

	split := publishing.SplitMomentRequest{EntryIDs: []string{later.Entries[1].ID}}
	preview, err := f.module.PreviewSplit(t.Context(), f.album.ID, later.ID, split)
	require.NoError(t, err)
	require.Empty(t, preview.Changes, "splitting changes no one's media")
	split.ReviewToken = preview.ReviewToken
	result, err := f.module.SplitMoment(t.Context(), f.album.ID, later.ID, split)
	require.NoError(t, err)
	require.Len(t, result.Moments, 3)
	for _, moment := range result.Moments[1:] {
		require.Equal(t, publishing.OfferDecisionOffer, momentCircle(moment, "Extended family").Decision)
		require.Equal(t, publishing.OfferDecisionWithhold, momentCircle(moment, "College friends").Decision)
	}
	require.Equal(t, map[string]int{f.album.ID: 2}, f.moreAlbums(t, grandma))
}

func TestMergingMomentsWithDifferentOffersAsksTheCurator(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	grandma, friend := f.person(t, "Grandma"), f.person(t, "Friend")
	extended, college := f.circle(t, "Extended family", grandma), f.circle(t, "College friends", friend)
	neighbors := f.circle(t, "Neighbors")
	first, later := f.album.Moments[0], f.album.Moments[1]
	f.offer(t, true, college)
	f.momentOffer(t, first.ID, extended, publishing.OfferDecisionOffer)
	f.momentOffer(t, later.ID, college, publishing.OfferDecisionWithhold)
	f.publish(t)

	merge := publishing.MergeMomentsRequest{TargetMomentID: later.ID, CoverEntryID: later.CoverEntryID}
	unresolved, err := f.module.PreviewMerge(t.Context(), f.album.ID, first.ID, merge)
	require.NoError(t, err)
	require.False(t, unresolved.Ready)
	require.Empty(t, unresolved.Conflicts)
	require.Equal(t, []publishing.CircleConflict{
		{CircleID: college, Name: "College friends", Source: publishing.OfferDecisionInherit, Target: publishing.OfferDecisionWithhold},
		{CircleID: extended, Name: "Extended family", Source: publishing.OfferDecisionOffer, Target: publishing.OfferDecisionInherit},
	}, unresolved.CircleConflicts)
	_, err = f.module.MergeMoments(t.Context(), f.album.ID, first.ID, merge)
	require.Error(t, err, "a merge never changes Offers without a resolution")

	// Only listed Circles may be resolved.
	merge.CircleResolutions = []publishing.CircleResolution{{CircleID: neighbors, Decision: publishing.OfferDecisionOffer}}
	_, err = f.module.PreviewMerge(t.Context(), f.album.ID, first.ID, merge)
	requireInvalid(t, err)

	merge.CircleResolutions = []publishing.CircleResolution{{CircleID: extended, Decision: publishing.OfferDecisionOffer}, {CircleID: college, Decision: publishing.OfferDecisionInherit}}
	preview, err := f.module.PreviewMerge(t.Context(), f.album.ID, first.ID, merge)
	require.NoError(t, err)
	require.True(t, preview.Ready)
	offered := map[string]int{}
	for _, change := range preview.Changes {
		offered[change.DisplayName] = len(change.OfferedGainedEntryIDs) - len(change.OfferedLostEntryIDs)
	}
	require.Equal(t, map[string]int{"Grandma": 2, "Friend": 2}, offered)
	merge.ReviewToken = preview.ReviewToken
	merged, err := f.module.MergeMoments(t.Context(), f.album.ID, first.ID, merge)
	require.NoError(t, err)
	require.Len(t, merged.Moments, 1)
	require.Equal(t, publishing.OfferDecisionOffer, momentCircle(merged.Moments[0], "Extended family").Decision)
	require.Empty(t, momentCircle(merged.Moments[0], "College friends").Decision)
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, grandma))
	require.Equal(t, map[string]int{f.album.ID: 3}, f.moreAlbums(t, friend))
}

func TestMomentsAddedLaterInheritTheAlbumOffer(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	db, module, album, _ := syncedAlbums(t, source, &recordingChapters{})
	grandma := models.Person{ID: models.NewUUIDv7(), DisplayName: "Grandma", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&grandma).Exec(t.Context())
	require.NoError(t, err)
	circle := models.Circle{ID: models.NewUUIDv7(), Name: "Extended family", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&circle).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewInsert().Model(&models.CircleMember{CircleID: circle.ID, PersonID: grandma.ID}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{Circles: []publishing.AlbumOfferChoice{{CircleID: circle.ID.String(), Offered: true}}})
	require.NoError(t, err)
	for _, moment := range album.Moments {
		_, err = module.SaveMomentRules(t.Context(), album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{},
			Circles: []publishing.CircleResolution{{CircleID: circle.ID.String(), Decision: publishing.OfferDecisionWithhold}}})
		require.NoError(t, err)
	}
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	_, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	more, err := module.ViewMoreAlbums(t.Context(), grandma.ID.String())
	require.NoError(t, err)
	require.Empty(t, more)

	source.assets = append(source.assets, newSyncAsset("fresh", "2026-07-06T09:00:00+14:00"))
	source.setMembers("a", "b", "z", "clip", "fresh")
	check, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, check.Audience, 1, "the check's review shows the new Moment offered")
	require.Len(t, check.Audience[0].OfferedGainedEntryIDs, 1)
	applied, err := module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: check.ReviewToken})
	require.NoError(t, err)
	fresh := entryIDs(applied)["fresh.jpg"]
	more, err = module.ViewMoreAlbums(t.Context(), grandma.ID.String())
	require.NoError(t, err)
	require.Len(t, more, 1)
	require.Equal(t, 1, more[0].PhotoCount)
	require.NoError(t, module.AuthorizeViewerEntry(t.Context(), grandma.ID.String(), "", fresh))
}

func TestAMergeReviewGoesStaleWhenAResolvedCircleIsDeleted(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	extended := f.circle(t, "Extended family", f.person(t, "Grandma"))
	first, later := f.album.Moments[0], f.album.Moments[1]
	f.momentOffer(t, first.ID, extended, publishing.OfferDecisionOffer)
	merge := publishing.MergeMomentsRequest{TargetMomentID: later.ID, CoverEntryID: later.CoverEntryID,
		CircleResolutions: []publishing.CircleResolution{{CircleID: extended, Decision: publishing.OfferDecisionOffer}}}
	preview, err := f.module.PreviewMerge(t.Context(), f.album.ID, first.ID, merge)
	require.NoError(t, err)
	require.True(t, preview.Ready)
	merge.ReviewToken = preview.ReviewToken
	_, err = f.db.NewDelete().Model((*models.Circle)(nil)).Where("id = ?", extended).Exec(t.Context())
	require.NoError(t, err)
	_, err = f.module.MergeMoments(t.Context(), f.album.ID, first.ID, merge)
	requireCode(t, err, "audience_changed")
}
