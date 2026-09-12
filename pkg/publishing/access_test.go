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
	assert.True(t, album.Access[0].Inherited)
	saved, err := module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, saved.Album.Access[0].Decision)
	assert.Zero(t, saved.Album.Access[0].AccessibleCount)
	assert.False(t, saved.Album.Access[0].Effective)
}

func TestSplitAndMergePreserveEntryOverridesWithAlbumInheritance(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id, source := person.ID.String(), album.Moments[1]
	_, err = module.SetMomentAccess(t.Context(), album.ID, source.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	_, err = module.SetEntryAccess(t.Context(), album.ID, source.Entries[0].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	split := publishing.SplitMomentRequest{EntryIDs: []string{source.Entries[0].ID}}
	preview, err := module.PreviewSplit(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	assert.Empty(t, preview.Changes)
	_, err = module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
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
			assert.Equal(t, publishing.DecisionAllow, entry.Access[0].Decision)
		}
	}
}

func TestMergeResolutionInvalidatesEarlierDeletionUndo(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id, target, source := person.ID.String(), album.Moments[0], album.Moments[1]
	_, err = module.SetMomentAccess(t.Context(), album.ID, target.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	deletion, err := module.SetMomentAccess(t.Context(), album.ID, target.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionInherit})
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, source.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	merge := publishing.MergeMomentsRequest{TargetMomentID: target.ID, CoverEntryID: target.CoverEntryID, Resolutions: []publishing.AccessResolution{{PersonID: id, Decision: publishing.DecisionInherit}}}
	preview, err := module.PreviewMerge(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	merge.ReviewToken = preview.ReviewToken
	_, err = module.MergeMoments(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	_, err = module.UndoMomentAccess(t.Context(), album.ID, target.ID, deletion.Undo)
	require.Error(t, err, "merge replaced the reviewed rule even though both results inherit")
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
	for _, entry := range refreshed.Moments[1].Entries {
		assert.Equal(t, entry.Filename == "first.jpg", entry.Access[0].Detected, entry.Filename)
		assert.Equal(t, entry.Filename == "first.jpg", entry.Access[0].Suggested, entry.Filename)
	}
}

func TestRemoveAllAccessRequiresCurrentReviewAndStaysWithinAlbum(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	_, err = module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, album.Moments[1].ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	_, err = module.SetEntryAccess(t.Context(), album.ID, album.Moments[1].Entries[0].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	otherSource := fixture()
	otherSource.albums["other"] = immich.Album{ID: "other", Name: "Other", Count: 3}
	otherModule := publishing.New(db, otherSource, noQueue)
	other, err := otherModule.StartImport(t.Context(), "other")
	require.NoError(t, err)
	require.NoError(t, otherModule.ExecuteImport(t.Context(), other.ID))
	_, err = otherModule.SetAlbumAccess(t.Context(), other.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	preview, err := module.PreviewRemoveAccess(t.Context(), album.ID, publishing.RemoveAccessPreviewRequest{PersonID: id})
	require.NoError(t, err)
	assert.Equal(t, 1, preview.AlbumDecisions)
	assert.Equal(t, 1, preview.MomentDecisions)
	assert.Equal(t, 1, preview.EntryDecisions)
	assert.Equal(t, 2, preview.AccessibleCount)
	_, err = module.SetEntryAccess(t.Context(), album.ID, album.Moments[1].Entries[1].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
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
			assert.Empty(t, entry.Access[0].Decision)
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
	_, err = module.SetMomentAccess(t.Context(), album.ID, moment.ID, publishing.SetMomentAccessRequest{PersonID: sam, Decision: publishing.DecisionDeny})
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
	assert.False(t, saved.Moments[1].Entries[0].Access[0].Effective)
	saved, err = module.SaveEntryRules(t.Context(), album.ID, moment.Entries[0].ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex, Decision: publishing.DecisionInherit}}})
	require.NoError(t, err)
	assert.True(t, saved.Moments[1].Entries[0].Access[0].Effective)
}

func TestStructuralReviewIncludesAlbumAndEntryRules(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	source, target := album.Moments[1], album.Moments[0]
	_, err = module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, target.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	move := publishing.MoveEntriesRequest{EntryIDs: []string{source.Entries[1].ID}, DestinationMomentID: target.ID}
	preview, err := module.PreviewMove(t.Context(), album.ID, source.ID, move)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	assert.Equal(t, move.EntryIDs, preview.Changes[0].LostEntryIDs)
	_, err = module.SetEntryAccess(t.Context(), album.ID, source.Entries[1].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
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

//nolint:tparallel // Scope cases deliberately reuse one Album and Person sequentially.
func TestAccessUndoRestoresDeletedRulesAndRejectsReplacedDeletions(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id, momentID, entryID := person.ID.String(), album.Moments[1].ID, album.Moments[1].Entries[0].ID
	scopes := []struct {
		name     string
		set      func(publishing.Decision) (publishing.AccessResult, error)
		undo     func(publishing.UndoMomentAccessRequest) (publishing.AlbumDetail, error)
		decision func(publishing.AlbumDetail) publishing.Decision
	}{
		{"album", func(d publishing.Decision) (publishing.AccessResult, error) {
			return module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: d})
		}, func(r publishing.UndoMomentAccessRequest) (publishing.AlbumDetail, error) {
			return module.UndoAlbumAccess(t.Context(), album.ID, r)
		}, func(a publishing.AlbumDetail) publishing.Decision { return a.Access[0].Decision }},
		{"moment", func(d publishing.Decision) (publishing.AccessResult, error) {
			return module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: id, Decision: d})
		}, func(r publishing.UndoMomentAccessRequest) (publishing.AlbumDetail, error) {
			return module.UndoMomentAccess(t.Context(), album.ID, momentID, r)
		}, func(a publishing.AlbumDetail) publishing.Decision { return a.Moments[1].Access.People[0].Decision }},
		{"entry", func(d publishing.Decision) (publishing.AccessResult, error) {
			return module.SetEntryAccess(t.Context(), album.ID, entryID, publishing.SetEntryAccessRequest{PersonID: id, Decision: d})
		}, func(r publishing.UndoMomentAccessRequest) (publishing.AlbumDetail, error) {
			return module.UndoEntryAccess(t.Context(), album.ID, entryID, r)
		}, func(a publishing.AlbumDetail) publishing.Decision { return a.Moments[1].Entries[0].Access[0].Decision }},
	}
	for _, scope := range scopes {
		t.Run(scope.name, func(t *testing.T) {
			_, err := scope.set(publishing.DecisionAllow)
			require.NoError(t, err)
			removed, err := scope.set(publishing.DecisionInherit)
			require.NoError(t, err)
			assert.Empty(t, scope.decision(removed.Album))
			restored, err := scope.undo(removed.Undo)
			require.NoError(t, err)
			assert.Equal(t, publishing.DecisionAllow, scope.decision(restored))
			_, err = scope.undo(removed.Undo)
			require.Error(t, err)
			old, err := scope.set(publishing.DecisionInherit)
			require.NoError(t, err)
			_, err = scope.set(publishing.DecisionAllow)
			require.NoError(t, err)
			latest, err := scope.set(publishing.DecisionInherit)
			require.NoError(t, err)
			_, err = scope.undo(old.Undo)
			require.Error(t, err)
			_, err = scope.undo(latest.Undo)
			require.NoError(t, err)
		})
	}
}

func TestEntryExceptionsOverrideMomentAndAlbumRules(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	id := person.ID.String()
	_, err = module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	moment := album.Moments[1]
	denied, err := module.SetEntryAccess(t.Context(), album.ID, moment.Entries[0].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	assert.Equal(t, 2, denied.Album.Access[0].AccessibleCount)
	assert.Equal(t, 1, denied.Album.Moments[1].Access.People[0].AccessibleCount)
	assert.True(t, denied.Album.Moments[1].Access.People[0].Effective)
	assert.False(t, denied.Album.Moments[1].Entries[0].Access[0].Effective)
	_, err = module.SetMomentAccess(t.Context(), album.ID, moment.ID, publishing.SetMomentAccessRequest{PersonID: id, Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	allowed, err := module.SetEntryAccess(t.Context(), album.ID, moment.Entries[1].ID, publishing.SetEntryAccessRequest{PersonID: id, Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	assert.Equal(t, 2, allowed.Album.Access[0].AccessibleCount)
	assert.False(t, allowed.Album.Moments[1].Access.People[0].Effective)
	removed, err := module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: id, Decision: publishing.DecisionInherit})
	require.NoError(t, err)
	assert.Empty(t, removed.Album.Access[0].Decision)
	assert.Equal(t, 1, removed.Album.Access[0].AccessibleCount)
	assert.True(t, removed.Album.Access[0].Effective)
	assert.Equal(t, publishing.DecisionDeny, removed.Album.Moments[1].Access.People[0].Decision)
}

func TestAlbumAccessDefaultsDeniedAndInheritsToEntries(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	require.NotNil(t, album.Moments[0].Entries[0].Access, "empty audiences serialize as arrays")
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, album.Access, 1)
	assert.False(t, album.Access[0].Effective)
	result, err := module.SetAlbumAccess(t.Context(), album.ID, publishing.SetAlbumAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, result.Album.Access[0].Decision)
	assert.Equal(t, 3, result.Album.Access[0].AccessibleCount)
	assert.True(t, result.Album.Moments[1].Access.People[0].Effective)
	assert.True(t, result.Album.Moments[1].Access.People[0].Inherited)
	for _, moment := range result.Album.Moments {
		for _, entry := range moment.Entries {
			require.Len(t, entry.Access, 1)
			assert.True(t, entry.Access[0].Effective)
			assert.True(t, entry.Access[0].Inherited)
		}
	}
}
