package publishing_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
)

func TestCoverOrderLeadsViewerAndCuratorCoversBeforeCaptureOrder(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, alex}).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	earliest, later := album.Moments[0], album.Moments[1]
	viewerCover := func() string {
		view, err := module.ViewAlbum(t.Context(), curator.ID.String(), alex.ID.String(), album.ID)
		require.NoError(t, err)
		return view.CoverURL
	}
	curatorCover := func() string {
		listed, err := module.ListAlbums(t.Context(), "")
		require.NoError(t, err)
		require.Len(t, listed, 1)
		return listed[0].CoverURL
	}
	require.Contains(t, viewerCover(), earliest.CoverEntryID, "an empty Cover Order keeps capture order")
	require.Contains(t, curatorCover(), earliest.CoverEntryID)

	ordered, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{later.ID}})
	require.NoError(t, err)
	require.Equal(t, 0, ordered.Moments[0].CoverPosition)
	require.Equal(t, 1, ordered.Moments[1].CoverPosition)
	require.Contains(t, viewerCover(), later.CoverEntryID)
	require.Contains(t, curatorCover(), later.CoverEntryID, "the Curator list follows the same order without access rules")

	_, err = module.SaveEntryRules(t.Context(), album.ID, later.CoverEntryID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	require.Contains(t, viewerCover(), earliest.CoverEntryID, "a denied preferred cover falls through to capture order")
	require.Contains(t, curatorCover(), later.CoverEntryID)

	reordered, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{earliest.ID, later.ID}})
	require.NoError(t, err)
	require.Equal(t, 1, reordered.Moments[0].CoverPosition)
	require.Equal(t, 2, reordered.Moments[1].CoverPosition)
	cleared, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{})
	require.NoError(t, err)
	for _, moment := range cleared.Moments {
		require.Equal(t, 0, moment.CoverPosition)
	}

	for name, ids := range map[string][]string{
		"unknown":   {models.NewUUIDv7().String()},
		"duplicate": {later.ID, later.ID},
		"malformed": {"not-a-moment"},
	} {
		_, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: ids})
		var validation *errcodes.FieldError
		require.ErrorAs(t, err, &validation, name)
		require.Contains(t, validation.Fields, "moment_ids", name)
	}
	unchanged, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Equal(t, cleared.Moments, unchanged.Moments, "a rejected order leaves the saved one alone")
}

func TestViewingGroupsFollowVisibleMomentCovers(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	earliest, later := album.Moments[0], album.Moments[1]
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	bo := models.Person{ID: models.NewUUIDv7(), DisplayName: "Bo", CreatedAt: time.Now().UTC()}
	kim := models.Person{ID: models.NewUUIDv7(), DisplayName: "Kim", CreatedAt: time.Now().UTC()}
	pat := models.Person{ID: models.NewUUIDv7(), DisplayName: "Pat", CreatedAt: time.Now().UTC()}
	sam := models.Person{ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{curator, alex, bo, kim, pat, sam}).Exec(t.Context())
	require.NoError(t, err)

	empty, err := module.ViewingGroups(t.Context(), album.ID)
	require.NoError(t, err)
	require.Empty(t, empty.Groups)
	require.Empty(t, empty.Placeholder)

	// Alex, Bo and Kim get the whole Album; Sam only the later Moment.
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{
		{PersonID: alex.ID.String(), Allowed: true}, {PersonID: bo.ID.String(), Allowed: true}, {PersonID: kim.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	_, err = module.SaveMomentRules(t.Context(), album.ID, later.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: sam.ID.String(), Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	// Kim loses both covers but keeps the other photo of the later Moment.
	_, err = module.SaveMomentRules(t.Context(), album.ID, earliest.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: kim.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	_, err = module.SaveEntryRules(t.Context(), album.ID, later.CoverEntryID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: kim.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)

	groups, err := module.ViewingGroups(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, groups.Groups, 2)
	require.Equal(t, []string{earliest.ID, later.ID}, groups.Groups[0].MomentIDs)
	require.Equal(t, []publishing.ViewingPerson{{PersonID: alex.ID.String(), DisplayName: "Alex"}, {PersonID: bo.ID.String(), DisplayName: "Bo"}}, groups.Groups[0].People)
	require.Equal(t, []string{later.ID}, groups.Groups[1].MomentIDs)
	require.Equal(t, []publishing.ViewingPerson{{PersonID: sam.ID.String(), DisplayName: "Sam"}}, groups.Groups[1].People)
	require.Equal(t, []publishing.ViewingPerson{{PersonID: kim.ID.String(), DisplayName: "Kim"}}, groups.Placeholder)
	for _, group := range groups.Groups {
		for _, person := range group.People {
			require.NotEqual(t, pat.ID.String(), person.PersonID, "a Person without access is not mentioned")
			require.NotEqual(t, curator.ID.String(), person.PersonID, "Curators are not viewers")
		}
	}

	// A cover that went offline in Immich leaves the running for everyone.
	_, err = db.NewUpdate().TableExpr("media_items AS item").Set("offline = true").
		Where("item.id = (SELECT media_item_id FROM album_entries WHERE id = ?)", earliest.CoverEntryID).Exec(t.Context())
	require.NoError(t, err)
	groups, err = module.ViewingGroups(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, groups.Groups, 1)
	require.Equal(t, []string{later.ID}, groups.Groups[0].MomentIDs)
	require.Len(t, groups.Groups[0].People, 3)

	_, err = module.ViewingGroups(t.Context(), models.NewUUIDv7().String())
	require.Error(t, err)
}

func TestCoverPositionSurvivesSplitAndMerge(t *testing.T) {
	t.Parallel()
	_, module, album := importedAlbum(t)
	earliest, later := album.Moments[0], album.Moments[1]
	ordered, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{later.ID, earliest.ID}})
	require.NoError(t, err)
	require.Equal(t, 2, ordered.Moments[0].CoverPosition)
	require.Equal(t, 1, ordered.Moments[1].CoverPosition)

	// Splitting keeps the place on the original Moment; the new one is unplaced.
	split := publishing.SplitMomentRequest{EntryIDs: []string{later.Entries[1].ID}}
	preview, err := module.PreviewSplit(t.Context(), album.ID, later.ID, split)
	require.NoError(t, err)
	split.ReviewToken = preview.ReviewToken
	afterSplit, err := module.SplitMoment(t.Context(), album.ID, later.ID, split)
	require.NoError(t, err)
	require.Len(t, afterSplit.Moments, 3)
	positions := map[string]int{}
	for _, moment := range afterSplit.Moments {
		positions[moment.ID] = moment.CoverPosition
	}
	require.Equal(t, 1, positions[later.ID])
	require.Equal(t, 2, positions[earliest.ID])
	var created publishing.Moment
	for _, moment := range afterSplit.Moments {
		if moment.ID != later.ID && moment.ID != earliest.ID {
			created = moment
		}
	}
	require.Equal(t, 0, created.CoverPosition)

	// Merging a preferred Moment into a later-placed one keeps the better place.
	merge := publishing.MergeMomentsRequest{TargetMomentID: earliest.ID, CoverEntryID: earliest.CoverEntryID}
	mergePreview, err := module.PreviewMerge(t.Context(), album.ID, later.ID, merge)
	require.NoError(t, err)
	require.True(t, mergePreview.Ready)
	merge.ReviewToken = mergePreview.ReviewToken
	merged, err := module.MergeMoments(t.Context(), album.ID, later.ID, merge)
	require.NoError(t, err)
	require.Len(t, merged.Moments, 2)
	positions = map[string]int{}
	for _, moment := range merged.Moments {
		positions[moment.ID] = moment.CoverPosition
	}
	require.Equal(t, 1, positions[earliest.ID])
	require.Equal(t, 0, positions[created.ID])

	// Merging an unplaced Moment into a placed one changes nothing.
	merge = publishing.MergeMomentsRequest{TargetMomentID: earliest.ID, CoverEntryID: earliest.CoverEntryID}
	mergePreview, err = module.PreviewMerge(t.Context(), album.ID, created.ID, merge)
	require.NoError(t, err)
	merge.ReviewToken = mergePreview.ReviewToken
	merged, err = module.MergeMoments(t.Context(), album.ID, created.ID, merge)
	require.NoError(t, err)
	require.Len(t, merged.Moments, 1)
	require.Equal(t, 1, merged.Moments[0].CoverPosition)
}

func TestExcludingAPlacedMomentFreesItsPlace(t *testing.T) {
	t.Parallel()
	_, module, album := importedAlbum(t)
	earliest, later := album.Moments[0], album.Moments[1]
	_, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{earliest.ID, later.ID}})
	require.NoError(t, err)
	// The earliest Moment has one item, so keeping it out removes the Moment.
	exclude := publishing.ExcludeEntriesRequest{EntryIDs: []string{earliest.Entries[0].ID}}
	preview, err := module.PreviewExclude(t.Context(), album.ID, earliest.ID, exclude)
	require.NoError(t, err)
	exclude.ReviewToken = preview.ReviewToken
	excluded, err := module.ExcludeEntries(t.Context(), album.ID, earliest.ID, exclude)
	require.NoError(t, err)
	require.Len(t, excluded.Moments, 1)
	require.Equal(t, later.ID, excluded.Moments[0].ID)
	require.Equal(t, 2, excluded.Moments[0].CoverPosition, "the surviving Moment keeps its own place")
	listed, err := module.ListAlbums(t.Context(), "")
	require.NoError(t, err)
	require.Contains(t, listed[0].CoverURL, later.CoverEntryID)
	// Adding the item back creates a fresh, unplaced Moment and the freed
	// place is available to a new order.
	include := publishing.IncludeEntryRequest{MomentID: "new:" + earliest.Date}
	includePreview, err := module.PreviewInclude(t.Context(), album.ID, earliest.Entries[0].ID, include)
	require.NoError(t, err)
	include.ReviewToken = includePreview.ReviewToken
	restored, err := module.IncludeEntry(t.Context(), album.ID, earliest.Entries[0].ID, include)
	require.NoError(t, err)
	require.Len(t, restored.Moments, 2)
	require.Equal(t, 0, restored.Moments[0].CoverPosition)
	require.Equal(t, 2, restored.Moments[1].CoverPosition)
	reordered, err := module.SaveCoverOrder(t.Context(), album.ID, publishing.SaveCoverOrderRequest{MomentIDs: []string{restored.Moments[0].ID}})
	require.NoError(t, err)
	require.Equal(t, 1, reordered.Moments[0].CoverPosition)
	require.Equal(t, 0, reordered.Moments[1].CoverPosition)
}
