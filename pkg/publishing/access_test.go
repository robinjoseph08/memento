package publishing_test

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmptyAlbumAccessStillListsActivePeople(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.assets = nil
	source.albums["source"] = immich.Album{ID: "source", Name: "Empty"}
	module := publishing.New(db, source, noQueue)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, album.Access, 1)
	assert.Equal(t, "Alex", album.Access[0].DisplayName)
	assert.False(t, album.Access[0].Effective)
	saved, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, saved.Access[0].Decision)
	assert.Zero(t, saved.Access[0].AccessibleCount)
	assert.False(t, saved.Access[0].Effective)
}

func TestSplitAndMergePreserveEntryOverridesWithAlbumInheritance(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id, source := person.ID.String(), album.Moments[1]
	_, err = module.SaveMomentRules(t.Context(), album.ID, source.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	_, err = module.SaveEntryRules(t.Context(), album.ID, source.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	split := publishing.SplitMomentRequest{EntryIDs: []string{source.Entries[0].ID}}
	preview, err := module.PreviewSplit(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	assert.Empty(t, preview.Changes)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id, Allowed: true}}})
	require.NoError(t, err)
	split.ReviewToken = preview.ReviewToken
	_, err = module.SplitMoment(t.Context(), album.ID, source.ID, split)
	require.Error(t, err, "Album rules invalidate structural review")
	preview, err = module.PreviewSplit(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	assert.Empty(t, preview.Changes)
	split.ReviewToken = preview.ReviewToken
	divided, err := module.SplitMoment(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	assert.Equal(t, 2, divided.Access[0].AccessibleCount)
	newMomentID := ""
	for _, moment := range divided.Moments {
		if moment.ID != album.Moments[0].ID {
			assert.Equal(t, publishing.DecisionDeny, decisionFor(moment, id))
		}
		if moment.ID != source.ID && moment.ID != album.Moments[0].ID {
			newMomentID = moment.ID
		}
	}
	merge := publishing.MergeMomentsRequest{TargetMomentID: source.ID, CoverEntryID: source.Entries[0].ID}
	mergePreview, err := module.PreviewMerge(t.Context(), album.ID, newMomentID, merge)
	require.NoError(t, err)
	assert.Empty(t, mergePreview.Changes)
	merge.ReviewToken = mergePreview.ReviewToken
	merged, err := module.MergeMoments(t.Context(), album.ID, newMomentID, merge)
	require.NoError(t, err)
	assert.Equal(t, 2, merged.Access[0].AccessibleCount)
	for _, entry := range merged.Moments[1].Entries {
		if entry.ID == source.Entries[0].ID {
			assert.Equal(t, publishing.DecisionAllow, entry.Decisions[id])
		}
	}
}

func TestEntryDetectionIsSpecificToItsOwnMedia(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.faces = func(_ context.Context, assetID string) ([]immich.Face, error) {
		if assetID == "a" {
			return []immich.Face{{FaceID: "face", ID: "alex-face", Name: "Alex"}}, nil
		}
		return nil, nil
	}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	link := models.ImmichFaceLink{SourceID: "alex-face", PersonID: &person.ID, UpdatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&link).Exec(t.Context())
	require.NoError(t, err)
	refreshed, err := module.RefreshMomentFaces(t.Context(), album.ID, album.Moments[1].ID)
	require.NoError(t, err)
	assert.True(t, refreshed.Moments[1].Access.People[0].Detected)
	assert.Equal(t, 1, refreshed.Moments[1].Access.People[0].SupportingEntries, "only the media carrying the face supports the suggestion")
	assert.False(t, refreshed.Moments[0].Access.People[0].Detected)
}

func TestRemoveAllAccessRequiresCurrentReviewAndStaysWithinAlbum(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id, Allowed: true}}})
	require.NoError(t, err)
	_, err = module.SaveMomentRules(t.Context(), album.ID, album.Moments[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	_, err = module.SaveEntryRules(t.Context(), album.ID, album.Moments[1].Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	otherSource := fixture()
	otherSource.albums["other"] = immich.Album{ID: "other", Name: "Other", Count: 3}
	otherModule := publishing.New(db, otherSource, noQueue)
	other, err := otherModule.StartImport(t.Context(), "other")
	require.NoError(t, err)
	require.NoError(t, otherModule.ExecuteImport(t.Context(), other.ID))
	_, err = otherModule.SaveAlbumAccess(t.Context(), other.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id, Allowed: true}}})
	require.NoError(t, err)
	preview, err := module.PreviewRemoveAccess(t.Context(), album.ID, publishing.RemoveAccessPreviewRequest{PersonID: id})
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	assert.Equal(t, "Alex", preview.Changes[0].DisplayName)
	assert.Len(t, preview.Changes[0].LostEntryIDs, 2)
	assert.Empty(t, preview.Changes[0].GainedEntryIDs)
	_, err = module.SaveEntryRules(t.Context(), album.ID, album.Moments[1].Entries[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	_, err = module.RemoveAccess(t.Context(), album.ID, publishing.RemoveAccessRequest{PersonID: id, ReviewToken: preview.ReviewToken})
	require.Error(t, err)
	preview, err = module.PreviewRemoveAccess(t.Context(), album.ID, publishing.RemoveAccessPreviewRequest{PersonID: id})
	require.NoError(t, err)
	removed, err := module.RemoveAccess(t.Context(), album.ID, publishing.RemoveAccessRequest{PersonID: id, ReviewToken: preview.ReviewToken})
	require.NoError(t, err)
	assert.Empty(t, removed.Access[0].Decision)
	assert.False(t, removed.Access[0].Effective)
	other, err = otherModule.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, other.Access[0].Decision)
	assert.Equal(t, 3, other.Access[0].AccessibleCount)
	for _, moment := range removed.Moments {
		assert.Empty(t, moment.Access.People[0].Decision)
		for _, entry := range moment.Entries {
			assert.Empty(t, entry.Decisions)
		}
	}
}

func TestRulesSaveOnlyListedDecisionsAtomically(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	people := []models.Person{{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}, {ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}}
	_, err := db.NewInsert().Model(&people).Exec(t.Context())
	require.NoError(t, err)
	alex, sam, moment := people[0].ID.String(), people[1].ID.String(), album.Moments[1]
	_, err = module.SaveMomentRules(t.Context(), album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: sam, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	saved, err := module.SaveMomentRules(t.Context(), album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionDeny, decisionFor(saved.Moments[1], sam))
	assert.Equal(t, publishing.DecisionAllow, decisionFor(saved.Moments[1], alex))
	_, err = module.SaveMomentRules(t.Context(), album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionDeny}, {PersonID: models.NewUUIDv7().String(), Decision: publishing.DecisionAllow}}})
	require.Error(t, err)
	current, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, decisionFor(current.Moments[1], alex))
	_, err = module.SaveEntryRules(t.Context(), album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionDeny}, {PersonID: alex, Decision: publishing.DecisionAllow}}})
	require.Error(t, err)
	saved, err = module.SaveEntryRules(t.Context(), album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionDeny, saved.Moments[1].Entries[0].Decisions[alex])
	assert.Equal(t, 1, personFor(saved.Moments[1], alex).AccessibleCount)
	saved, err = module.SaveEntryRules(t.Context(), album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionInherit}}})
	require.NoError(t, err)
	assert.Empty(t, saved.Moments[1].Entries[0].Decisions)
	assert.Equal(t, 2, personFor(saved.Moments[1], alex).AccessibleCount)
}

func TestStructuralReviewIncludesAlbumAndEntryRules(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	source, target := album.Moments[1], album.Moments[0]
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id, Allowed: true}}})
	require.NoError(t, err)
	_, err = module.SaveMomentRules(t.Context(), album.ID, target.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	move := publishing.MoveEntriesRequest{EntryIDs: []string{source.Entries[1].ID}, DestinationMomentID: target.ID}
	preview, err := module.PreviewMove(t.Context(), album.ID, source.ID, move)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	assert.Equal(t, move.EntryIDs, preview.Changes[0].LostEntryIDs)
	_, err = module.SaveEntryRules(t.Context(), album.ID, source.Entries[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	move.ReviewToken = preview.ReviewToken
	_, err = module.MoveEntries(t.Context(), album.ID, source.ID, move)
	require.Error(t, err, "entry rule changes invalidate review")
	preview, err = module.PreviewMove(t.Context(), album.ID, source.ID, move)
	require.NoError(t, err)
	assert.Empty(t, preview.Changes, "an entry override follows the moved item")
	move.ReviewToken = preview.ReviewToken
	moved, err := module.MoveEntries(t.Context(), album.ID, source.ID, move)
	require.NoError(t, err)
	assert.Equal(t, 2, moved.Access[0].AccessibleCount)
}

func TestAlbumAccessPreviewMatchesSaveAndKeepsNarrowerRules(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	people := []models.Person{{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}, {ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}}
	_, err := db.NewInsert().Model(&people).Exec(t.Context())
	require.NoError(t, err)
	alex, sam := people[0].ID.String(), people[1].ID.String()
	_, err = module.SaveMomentRules(t.Context(), album.ID, album.Moments[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	request := publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex, Allowed: true}, {PersonID: sam, Allowed: true}}}
	preview, err := module.PreviewAlbumAccess(t.Context(), album.ID, request)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 2)
	assert.Len(t, preview.Changes[0].GainedEntryIDs, 1, "the Moment exclusion keeps applying")
	assert.Len(t, preview.Changes[1].GainedEntryIDs, 3)
	saved, err := module.SaveAlbumAccess(t.Context(), album.ID, request)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.Access[0].AccessibleCount)
	assert.Equal(t, 1, saved.Access[0].Exceptions)
	assert.Equal(t, 3, saved.Access[1].AccessibleCount)
	assert.True(t, personFor(saved.Moments[0], alex).Inherited)
	removed, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex}}})
	require.NoError(t, err)
	assert.Empty(t, removed.Access[0].Decision)
	assert.Equal(t, publishing.DecisionDeny, decisionFor(removed.Moments[1], alex), "unchecking keeps narrower rules")
	assert.Equal(t, publishing.DecisionAllow, removed.Access[1].Decision, "people left out of the request are untouched")
	_, err = module.PreviewAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex, Allowed: true}, {PersonID: alex}}})
	require.Error(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: models.NewUUIDv7().String(), Allowed: true}}})
	require.Error(t, err)
}

func TestEntryExceptionsOverrideMomentAndAlbumRules(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id, Allowed: true}}})
	require.NoError(t, err)
	moment := album.Moments[1]
	denied, err := module.SaveEntryRules(t.Context(), album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	assert.Equal(t, 2, denied.Access[0].AccessibleCount)
	assert.Equal(t, 1, denied.Moments[1].Access.People[0].AccessibleCount)
	assert.True(t, denied.Moments[1].Access.People[0].Effective)
	assert.Equal(t, publishing.DecisionDeny, denied.Moments[1].Entries[0].Decisions[id])
	_, err = module.SaveMomentRules(t.Context(), album.ID, moment.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)
	allowed, err := module.SaveEntryRules(t.Context(), album.ID, moment.Entries[1].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	assert.Equal(t, 2, allowed.Access[0].AccessibleCount)
	assert.False(t, allowed.Moments[1].Access.People[0].Effective)
	removed, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: id}}})
	require.NoError(t, err)
	assert.Empty(t, removed.Access[0].Decision)
	assert.Equal(t, 1, removed.Access[0].AccessibleCount)
	assert.True(t, removed.Access[0].Effective)
	assert.Equal(t, publishing.DecisionDeny, removed.Moments[1].Access.People[0].Decision)
}

func TestAlbumAccessDefaultsDeniedAndInheritsToEntries(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	require.NotNil(t, album.Moments[0].Entries[0].Decisions, "absent entry rules serialize as an empty object")
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, album.Access, 1)
	assert.False(t, album.Access[0].Effective)
	result, err := module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, result.Access[0].Decision)
	assert.Equal(t, 3, result.Access[0].AccessibleCount)
	assert.True(t, result.Moments[1].Access.People[0].Effective)
	assert.True(t, result.Moments[1].Access.People[0].Inherited)
	for _, moment := range result.Moments {
		assert.True(t, personFor(moment, person.ID.String()).Effective)
		assert.Equal(t, len(moment.Entries), personFor(moment, person.ID.String()).AccessibleCount)
		for _, entry := range moment.Entries {
			assert.Empty(t, entry.Decisions, "inheritance is computed, not stored per entry")
		}
	}
}
