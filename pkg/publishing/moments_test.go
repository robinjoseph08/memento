package publishing_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func importedAlbum(t *testing.T) (*bun.DB, *publishing.Module, publishing.AlbumDetail) {
	t.Helper()
	db := testdb.New(t)
	module := publishing.New(db, fixture(), noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	return db, module, album
}

func TestMomentTitleAndCoverEdits(t *testing.T) {
	t.Parallel()
	_, module, album := importedAlbum(t)
	moment := album.Moments[0]
	require.Len(t, moment.Entries, 1)

	renamed, err := module.UpdateMoment(t.Context(), album.ID, moment.ID, publishing.UpdateMomentRequest{Title: "  Arrival  "})
	require.NoError(t, err)
	assert.Equal(t, "Arrival", renamed.Moments[0].Title)
	assert.Equal(t, "Arrival", renamed.Moments[0].Label)

	cleared, err := module.UpdateMoment(t.Context(), album.ID, moment.ID, publishing.UpdateMomentRequest{})
	require.NoError(t, err)
	assert.Empty(t, cleared.Moments[0].Title)
	assert.Contains(t, cleared.Moments[0].Label, "July 4")

	second := cleared.Moments[1]
	require.GreaterOrEqual(t, len(second.Entries), 2)
	changed, err := module.SetMomentCover(t.Context(), album.ID, second.ID, publishing.SetMomentCoverRequest{EntryID: second.Entries[1].ID})
	require.NoError(t, err)
	assert.Equal(t, second.Entries[1].ID, changed.Moments[1].CoverEntryID)

	_, err = module.SetMomentCover(t.Context(), album.ID, second.ID, publishing.SetMomentCoverRequest{EntryID: moment.Entries[0].ID})
	require.Error(t, err)
}

func TestImmediateMomentDecisionsHaveLocalizedUndo(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	sam := models.Person{ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{alex, sam}).Exec(t.Context())
	require.NoError(t, err)
	momentID := album.Moments[0].ID

	first, err := module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: alex.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	require.Equal(t, publishing.DecisionAllow, decisionFor(first.Album.Moments[0], alex.ID.String()))

	second, err := module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: sam.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	require.Equal(t, publishing.DecisionAllow, decisionFor(second.Album.Moments[0], sam.ID.String()))

	undone, err := module.UndoMomentAccess(t.Context(), album.ID, momentID, first.Undo)
	require.NoError(t, err)
	assert.Empty(t, decisionFor(undone.Moments[0], alex.ID.String()))
	assert.Equal(t, publishing.DecisionAllow, decisionFor(undone.Moments[0], sam.ID.String()))

	_, err = module.UndoMomentAccess(t.Context(), album.ID, momentID, first.Undo)
	require.Error(t, err)
}

func TestAddAllSuggestedKeepsExplicitExclusionsAndUndoesOnlyItsOwnGrants(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.faces = func(_ context.Context, assetID string) ([]immich.Face, error) {
		return []immich.Face{
			{FaceID: "alex-" + assetID, ID: "immich-alex", Name: "Immich Alex"},
			{FaceID: "sam-" + assetID, ID: "immich-sam", Name: "Immich Sam"},
		}, nil
	}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	momentID := album.Moments[1].ID
	_, err = module.RefreshMomentFaces(t.Context(), album.ID, momentID)
	require.NoError(t, err)

	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	sam := models.Person{ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}
	taylor := models.Person{ID: models.NewUUIDv7(), DisplayName: "Taylor", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&[]models.Person{alex, sam, taylor}).Exec(t.Context())
	require.NoError(t, err)
	alexID, samID := alex.ID, sam.ID
	links := []models.ImmichFaceLink{
		{SourceID: "immich-alex", PersonID: &alexID, UpdatedAt: time.Now().UTC()},
		{SourceID: "immich-sam", PersonID: &samID, UpdatedAt: time.Now().UTC()},
	}
	_, err = db.NewInsert().Model(&links).Exec(t.Context())
	require.NoError(t, err)
	excluded, err := module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: sam.ID.String(), Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	require.True(t, personFor(excluded.Album.Moments[1], alex.ID.String()).Suggested)
	require.False(t, personFor(excluded.Album.Moments[1], sam.ID.String()).Suggested, "an explicit exclusion is not a suggestion")

	added, err := module.AddMomentSuggestions(t.Context(), album.ID, momentID)
	require.NoError(t, err)
	assert.Equal(t, publishing.DecisionAllow, decisionFor(added.Album.Moments[1], alex.ID.String()))
	assert.Equal(t, publishing.DecisionDeny, decisionFor(added.Album.Moments[1], sam.ID.String()), "Add all suggested keeps existing exclusions")
	assert.Empty(t, decisionFor(added.Album.Moments[1], taylor.ID.String()), "undetected people gain nothing")
	require.Len(t, added.Undo.Changes, 1)
	assert.Equal(t, alex.ID.String(), added.Undo.Changes[0].PersonID)

	undone, err := module.UndoMomentAccess(t.Context(), album.ID, momentID, added.Undo)
	require.NoError(t, err)
	assert.Empty(t, decisionFor(undone.Moments[1], alex.ID.String()))
	assert.True(t, personFor(undone.Moments[1], alex.ID.String()).Suggested, "the suggestion returns after Undo")
	assert.Equal(t, publishing.DecisionDeny, decisionFor(undone.Moments[1], sam.ID.String()), "Undo leaves unrelated decisions alone")
}

func TestUndoRejectsANewerWriteWithTheSameDecision(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	momentID := album.Moments[0].ID

	first, err := module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	latest, err := module.SetMomentAccess(t.Context(), album.ID, momentID, publishing.SetMomentAccessRequest{PersonID: person.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	require.NotEqual(t, first.Undo.Changes[0].CurrentUpdatedAt, latest.Undo.Changes[0].CurrentUpdatedAt)

	_, err = module.UndoMomentAccess(t.Context(), album.ID, momentID, first.Undo)
	require.Error(t, err)
	current, getErr := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, getErr)
	assert.Equal(t, publishing.DecisionAllow, decisionFor(current.Moments[0], person.ID.String()))
}

func TestMovingACoverPromotesTheEarliestRemainingEntry(t *testing.T) {
	t.Parallel()
	_, module, album := importedAlbum(t)
	destination, source := album.Moments[0], album.Moments[1]
	require.Len(t, source.Entries, 2)
	request := publishing.MoveEntriesRequest{
		EntryIDs:            []string{source.CoverEntryID},
		DestinationMomentID: destination.ID,
	}
	remaining := ""
	for _, entry := range source.Entries {
		if entry.ID != source.CoverEntryID {
			remaining = entry.ID
		}
	}
	preview, err := module.PreviewMove(t.Context(), album.ID, source.ID, request)
	require.NoError(t, err)
	request.ReviewToken = preview.ReviewToken
	moved, err := module.MoveEntries(t.Context(), album.ID, source.ID, request)
	require.NoError(t, err)
	assert.Equal(t, remaining, moved.Moments[1].CoverEntryID, "the surviving Moment keeps a cover without being asked")
}

func TestSplitGivesBothMomentsTheirEarliestEntryAsCover(t *testing.T) {
	t.Parallel()
	_, module, album := importedAlbum(t)
	first, destination := album.Moments[0], album.Moments[1]
	move := publishing.MoveEntriesRequest{EntryIDs: []string{first.Entries[0].ID}, DestinationMomentID: destination.ID}
	movePreview, err := module.PreviewMove(t.Context(), album.ID, first.ID, move)
	require.NoError(t, err)
	move.ReviewToken = movePreview.ReviewToken
	combined, err := module.MoveEntries(t.Context(), album.ID, first.ID, move)
	require.NoError(t, err)
	require.Len(t, combined.Moments, 1)
	source := combined.Moments[0]
	require.Len(t, source.Entries, 3)

	// Entries arrive in capture order. Split off the cover plus the last
	// entry, listed out of order, so the earliest selected entry must win and
	// the surviving Moment falls back to its earliest remaining entry.
	coverIndex := slices.IndexFunc(source.Entries, func(entry publishing.Entry) bool { return entry.ID == source.CoverEntryID })
	require.NotEqual(t, -1, coverIndex)
	require.NotEqual(t, 2, coverIndex, "the fixture's cover must not already be the last entry")
	selected := map[int]bool{2: true, coverIndex: true}
	split := publishing.SplitMomentRequest{EntryIDs: []string{source.Entries[2].ID, source.CoverEntryID}}
	splitPreview, err := module.PreviewSplit(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	split.ReviewToken = splitPreview.ReviewToken
	result, err := module.SplitMoment(t.Context(), album.ID, source.ID, split)
	require.NoError(t, err)
	require.Len(t, result.Moments, 2)
	earliest := func(want bool) string {
		for index, entry := range source.Entries {
			if selected[index] == want {
				return entry.ID
			}
		}
		return ""
	}
	for _, moment := range result.Moments {
		if moment.ID == source.ID {
			assert.Equal(t, earliest(false), moment.CoverEntryID, "the surviving Moment gets its earliest remaining entry")
		} else {
			assert.Equal(t, earliest(true), moment.CoverEntryID, "the new Moment gets its earliest entry")
		}
	}
}

func TestStructuralChangesPreviewAndRevalidateEffectiveAudience(t *testing.T) {
	t.Parallel()
	db, module, album := importedAlbum(t)
	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	sam := models.Person{ID: models.NewUUIDv7(), DisplayName: "Sam", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&[]models.Person{alex, sam}).Exec(t.Context())
	require.NoError(t, err)
	first, second := album.Moments[0], album.Moments[1]
	_, err = module.SetMomentAccess(t.Context(), album.ID, first.ID, publishing.SetMomentAccessRequest{PersonID: alex.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	_, err = module.SetMomentAccess(t.Context(), album.ID, second.ID, publishing.SetMomentAccessRequest{PersonID: sam.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)

	move := publishing.MoveEntriesRequest{EntryIDs: []string{second.Entries[0].ID, second.Entries[1].ID}, DestinationMomentID: first.ID}
	movePreview, err := module.PreviewMove(t.Context(), album.ID, second.ID, move)
	require.NoError(t, err)
	assert.True(t, movePreview.RemovesMoment)
	assert.Equal(t, []publishing.AudienceChange{
		{PersonID: alex.ID.String(), DisplayName: "Alex", GainedEntryIDs: []string{second.Entries[0].ID, second.Entries[1].ID}, LostEntryIDs: []string{}},
		{PersonID: sam.ID.String(), DisplayName: "Sam", GainedEntryIDs: []string{}, LostEntryIDs: []string{second.Entries[0].ID, second.Entries[1].ID}},
	}, movePreview.Changes)
	move.ReviewToken = movePreview.ReviewToken
	moved, err := module.MoveEntries(t.Context(), album.ID, second.ID, move)
	require.NoError(t, err)
	require.Len(t, moved.Moments, 1)
	require.Len(t, moved.Moments[0].Entries, 3)

	current := moved.Moments[0]
	split := publishing.SplitMomentRequest{EntryIDs: []string{current.CoverEntryID}}
	splitPreview, err := module.PreviewSplit(t.Context(), album.ID, current.ID, split)
	require.NoError(t, err)
	assert.Empty(t, splitPreview.Changes)
	split.ReviewToken = splitPreview.ReviewToken
	splitAlbum, err := module.SplitMoment(t.Context(), album.ID, current.ID, split)
	require.NoError(t, err)
	require.Len(t, splitAlbum.Moments, 2)
	assert.Contains(t, splitAlbum.Moments[0].Label, "(1)")
	assert.Contains(t, splitAlbum.Moments[1].Label, "(2)")
	assert.Equal(t, publishing.DecisionAllow, decisionFor(splitAlbum.Moments[0], alex.ID.String()))
	assert.Equal(t, publishing.DecisionAllow, decisionFor(splitAlbum.Moments[1], alex.ID.String()))

	source, target := splitAlbum.Moments[1], splitAlbum.Moments[0]
	_, err = module.SetMomentAccess(t.Context(), album.ID, source.ID, publishing.SetMomentAccessRequest{PersonID: sam.ID.String(), Decision: publishing.DecisionAllow})
	require.NoError(t, err)
	merge := publishing.MergeMomentsRequest{TargetMomentID: target.ID, CoverEntryID: target.CoverEntryID}
	unresolved, err := module.PreviewMerge(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	assert.False(t, unresolved.Ready)
	require.Len(t, unresolved.Conflicts, 1)
	assert.Equal(t, sam.ID.String(), unresolved.Conflicts[0].PersonID)

	merge.Resolutions = []publishing.AccessResolution{{PersonID: sam.ID.String(), Decision: publishing.DecisionAllow}}
	mergePreview, err := module.PreviewMerge(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	require.True(t, mergePreview.Ready)
	merge.ReviewToken = mergePreview.ReviewToken
	_, err = module.SetMomentAccess(t.Context(), album.ID, target.ID, publishing.SetMomentAccessRequest{PersonID: alex.ID.String(), Decision: publishing.DecisionDeny})
	require.NoError(t, err)
	_, err = module.MergeMoments(t.Context(), album.ID, source.ID, merge)
	require.Error(t, err)
	unchanged, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, unchanged.Moments, 2)

	merge.Resolutions = append(merge.Resolutions, publishing.AccessResolution{PersonID: alex.ID.String(), Decision: publishing.DecisionAllow})
	mergePreview, err = module.PreviewMerge(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	require.True(t, mergePreview.Ready)
	merge.ReviewToken = mergePreview.ReviewToken
	merged, err := module.MergeMoments(t.Context(), album.ID, source.ID, merge)
	require.NoError(t, err)
	require.Len(t, merged.Moments, 1)
	assert.Equal(t, publishing.DecisionAllow, decisionFor(merged.Moments[0], sam.ID.String()))
}

func TestFaceRefreshDerivesRecommendationsAndPreservesCacheOnFailure(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	source.faces = func(_ context.Context, assetID string) ([]immich.Face, error) {
		return []immich.Face{{FaceID: "face-" + assetID, ID: "immich-alex", Name: "Immich Alex"}}, nil
	}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	moment := album.Moments[1]

	refreshed, err := module.RefreshMomentFaces(t.Context(), album.ID, moment.ID)
	require.NoError(t, err)
	require.NotNil(t, refreshed.Moments[1].Access.RefreshedAt)
	require.Len(t, refreshed.Moments[1].Access.Faces, 1)
	assert.Equal(t, "immich-alex", refreshed.Moments[1].Access.Faces[0].SourceID)
	assert.Equal(t, 2, refreshed.Moments[1].Access.Faces[0].Occurrences)
	var mediaIDs []models.UUID
	require.NoError(t, db.NewSelect().TableExpr("album_entries AS entry").Column("media_item_id").Where("entry.moment_id = ?", moment.ID).Order("media_item_id").Scan(t.Context(), &mediaIDs))
	require.Len(t, mediaIDs, 2)
	_, err = db.NewDelete().Model((*models.MediaFaceRefresh)(nil)).Where("media_item_id = ?", mediaIDs[0]).Exec(t.Context())
	require.NoError(t, err)
	partial, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Nil(t, partial.Moments[1].Access.RefreshedAt)
	_, err = module.RefreshMomentFaces(t.Context(), album.ID, moment.ID)
	require.NoError(t, err)

	alex := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&alex).Exec(t.Context())
	require.NoError(t, err)
	alexID := alex.ID
	link := models.ImmichFaceLink{SourceID: "immich-alex", PersonID: &alexID, UpdatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&link).Exec(t.Context())
	require.NoError(t, err)
	linked, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	person := linked.Moments[1].Access.People[0]
	assert.True(t, person.Detected)
	assert.True(t, person.Suggested)
	assert.Equal(t, 2, person.SupportingEntries)

	cachedAt := *linked.Moments[1].Access.RefreshedAt
	source.faces = func(context.Context, string) ([]immich.Face, error) {
		return nil, errors.New("controlled face refresh failure")
	}
	_, err = module.RefreshMomentFaces(t.Context(), album.ID, moment.ID)
	require.Error(t, err)
	cached, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Equal(t, cachedAt, *cached.Moments[1].Access.RefreshedAt)
	require.Len(t, cached.Moments[1].Access.Faces, 1)
	assert.Equal(t, "immich-alex", cached.Moments[1].Access.Faces[0].SourceID)
}

func TestOlderFaceRefreshCannotOverwriteNewerCache(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	source := fixture()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	source.faces = func(ctx context.Context, _ string) ([]immich.Face, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []immich.Face{{ID: "old-face", Name: "Old"}}, nil
		}
		return []immich.Face{{ID: "new-face", Name: "New"}}, nil
	}
	module := publishing.New(db, source, noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	moment := album.Moments[0]
	require.Len(t, moment.Entries, 1)

	firstDone := make(chan error, 1)
	go func() {
		_, err := module.RefreshMomentFaces(t.Context(), album.ID, moment.ID)
		firstDone <- err
	}()
	<-firstStarted
	newer, err := module.RefreshMomentFaces(t.Context(), album.ID, moment.ID)
	require.NoError(t, err)
	require.Len(t, newer.Moments[0].Access.Faces, 1)
	assert.Equal(t, "new-face", newer.Moments[0].Access.Faces[0].SourceID)
	close(releaseFirst)
	require.NoError(t, <-firstDone)

	current, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	require.Len(t, current.Moments[0].Access.Faces, 1)
	assert.Equal(t, "new-face", current.Moments[0].Access.Faces[0].SourceID)
}

func personFor(moment publishing.Moment, personID string) publishing.AccessPerson {
	for _, person := range moment.Access.People {
		if person.PersonID == personID {
			return person
		}
	}
	return publishing.AccessPerson{}
}

func decisionFor(moment publishing.Moment, personID string) publishing.Decision {
	for _, person := range moment.Access.People {
		if person.PersonID == personID {
			return person.Decision
		}
	}
	return ""
}
