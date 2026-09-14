package publishing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// syncFixture keeps membership explicit so removing an asset from the source
// album is one list edit, and adds a second album for shared-item scenarios.
func syncFixture() *library {
	source := videoLibrary()
	source.membership = map[string][]string{"source": {"a", "b", "z", "clip"}, "other": {"clip"}}
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Description: "From Immich", Count: 4, UpdatedAt: "2026-07-06T00:00:00Z"}
	source.albums["other"] = immich.Album{ID: "other", Name: "Reunion", Description: "Shared", Count: 1, UpdatedAt: "2026-07-06T00:00:00Z"}
	return source
}

func (l *library) setMembers(ids ...string) {
	l.membership["source"] = ids
	a := l.albums["source"]
	a.Count = len(ids)
	l.albums["source"] = a
}

func (l *library) asset(id string) *immich.Asset {
	for i := range l.assets {
		if l.assets[i].ID == id {
			return &l.assets[i]
		}
	}
	panic("unknown fixture asset " + id)
}

func newSyncAsset(id, localDateTime string) immich.Asset {
	return immich.Asset{ID: id, Checksum: "bmV3", Filename: id + ".jpg", Kind: "IMAGE", LocalDateTime: localDateTime, FileCreatedAt: "2026-07-07T10:00:00Z", UpdatedAt: "2026-07-07T10:00:00Z"}
}

func syncedAlbums(t *testing.T, source *library, chapters publishing.ChapterService) (*bun.DB, *publishing.Module, publishing.AlbumDetail, publishing.AlbumDetail) {
	t.Helper()
	db := testdb.New(t)
	module := publishing.New(db, source, noQueue)
	module.Chapters = chapters
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	album, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	other, err := module.StartImport(t.Context(), "other")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), other.ID))
	other, err = module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	return db, module, album, other
}

func entryIDs(album publishing.AlbumDetail) map[string]string {
	result := map[string]string{}
	for _, moment := range album.Moments {
		for _, entry := range moment.Entries {
			result[entry.Filename] = entry.ID
		}
	}
	return result
}

func hasEntry(album publishing.AlbumDetail, filename string) bool {
	_, ok := entryIDs(album)[filename]
	return ok
}

// curation strips face refresh times, the one thing a check may change, so
// two details can be compared for everything a Curator owns.
func curation(album publishing.AlbumDetail) publishing.AlbumDetail {
	moments := make([]publishing.Moment, len(album.Moments))
	for i, moment := range album.Moments {
		moment.Access.RefreshedAt = nil
		moments[i] = moment
	}
	album.Moments = moments
	return album
}

func momentOf(album publishing.AlbumDetail, filename string) publishing.Moment {
	for _, moment := range album.Moments {
		for _, entry := range moment.Entries {
			if entry.Filename == filename {
				return moment
			}
		}
	}
	return publishing.Moment{}
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	typed, ok := errors.AsType[*errcodes.Error](err)
	require.True(t, ok, "expected %s, got %v", code, err)
	assert.Equal(t, code, typed.Code)
}

func TestSyncNoOpAdditionAndPersistentExclusion(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	chapters := &recordingChapters{}
	_, module, album, _ := syncedAlbums(t, source, chapters)
	imported := len(chapters.requests)

	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.UpToDate)
	assert.False(t, review.Ready)
	assert.Empty(t, review.Additions)
	assert.True(t, review.FacesRefreshed)
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	requireCode(t, err, "sync_unchanged")

	// A new asset on a day without a Moment suggests a new Moment.
	source.assets = append(source.assets, newSyncAsset("fresh", "2026-07-06T09:00:00+14:00"))
	source.setMembers("a", "b", "z", "clip", "fresh")
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Additions, 1)
	assert.True(t, review.Ready)
	assert.False(t, review.UpToDate)
	assert.Equal(t, "new:2026-07-06", review.Additions[0].SuggestedMomentID)
	assert.Equal(t, "new:2026-07-06", review.Additions[0].MomentID)
	assert.False(t, review.Additions[0].Returning)
	assert.Equal(t, "/api/media/sources/assets/fresh/thumbnail", review.Additions[0].ThumbnailURL)
	assert.Equal(t, publishing.SyncMomentOption{ID: "new:2026-07-06", Label: "July 6, 2026", New: true}, review.Moments[len(review.Moments)-1])

	// Excluding it keeps the Album unchanged, and the exclusion survives rechecks.
	exclude := publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "fresh", Exclude: true}}}
	review, err = module.CheckSync(t.Context(), album.ID, exclude)
	require.NoError(t, err)
	assert.True(t, review.Ready)
	exclude.ReviewToken = review.ReviewToken
	applied, err := module.ApplySync(t.Context(), album.ID, exclude)
	require.NoError(t, err)
	assert.Len(t, applied.Moments, len(album.Moments))
	assert.False(t, hasEntry(applied, "fresh.jpg"), "an excluded asset is never shown")
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.UpToDate)
	assert.Empty(t, review.Additions, "excluded media lives in the Excluded section, not the review")
	require.Len(t, applied.Excluded, 1)
	assert.Equal(t, "fresh.jpg", applied.Excluded[0].Filename)
	assert.Equal(t, "/api/media/sources/assets/fresh/thumbnail", applied.Excluded[0].ThumbnailURL)
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	requireCode(t, err, "sync_unchanged")

	// Add back places it into a new Moment for its capture day.
	include := publishing.IncludeEntryRequest{MomentID: "new:2026-07-06"}
	preview, err := module.PreviewInclude(t.Context(), album.ID, applied.Excluded[0].ID, include)
	require.NoError(t, err)
	include.ReviewToken = preview.ReviewToken
	applied, err = module.IncludeEntry(t.Context(), album.ID, applied.Excluded[0].ID, include)
	require.NoError(t, err)
	require.Len(t, applied.Moments, len(album.Moments)+1)
	assert.Empty(t, applied.Excluded)
	placed := momentOf(applied, "fresh.jpg")
	assert.Equal(t, "2026-07-06", placed.Date)
	assert.Equal(t, entryIDs(applied)["fresh.jpg"], placed.CoverEntryID)
	assert.Len(t, chapters.requests, imported, "photos never queue extraction")

	// Idempotent recheck.
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.UpToDate)
	assert.Empty(t, review.Additions)
	again, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Equal(t, curation(applied), curation(again))

	// Placements are validated against the diff.
	_, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "a", MomentID: placed.ID}}})
	requireCode(t, err, "validation_error")
}

func TestSyncRemovalRestartRestoreAndAnnouncements(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	source.faces = func(_ context.Context, id string) ([]immich.Face, error) {
		if id == "a" {
			return []immich.Face{{FaceID: "face-1", ID: "person-1", Name: "Alex", UpdatedAt: "v1"}}, nil
		}
		return nil, nil
	}
	db, module, album, _ := syncedAlbums(t, source, nil)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC(), OnboardingCompletedAt: new(time.Now().UTC())}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	publication, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	album, err = module.PublishAlbum(t.Context(), album.ID, publishing.PublishRequest{ReviewToken: publication.ReviewToken})
	require.NoError(t, err)
	updates := notifications.New(db, nil, nil, module, nil)
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := updates.RecordBaseline(ctx, tx, person.ID.String())
		return err
	}))
	ids := entryIDs(album)
	lonely := momentOf(album, "last.jpg")
	require.Len(t, lonely.Entries, 1, "last.jpg is alone on July 4")
	shared := momentOf(album, "first.jpg")
	require.Equal(t, ids["first.jpg"], shared.CoverEntryID)

	// Removing the cover of a surviving Moment needs a replacement; removing a
	// Moment's only entry removes that Moment, and the review says so.
	source.setMembers("b", "clip")
	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Removals, 2)
	assert.False(t, review.Ready)
	assert.Equal(t, []string{"Choose a new cover for " + shared.Label + "."}, review.Blockers)
	require.Len(t, review.CoverChoices, 1)
	assert.Equal(t, shared.ID, review.CoverChoices[0].MomentID)
	require.Len(t, review.CoverChoices[0].Options, 2, "the photo and the video remain in that Moment")
	assert.Equal(t, ids["second.jpg"], review.CoverChoices[0].Options[0].EntryID)
	assert.Equal(t, ids["family reunion.MP4"], review.CoverChoices[0].Options[1].EntryID)
	require.Len(t, review.RemovedMoments, 1)
	assert.Equal(t, lonely.ID, review.RemovedMoments[0].ID)
	require.Len(t, review.Audience, 1)
	assert.ElementsMatch(t, []string{ids["first.jpg"], ids["last.jpg"]}, review.Audience[0].LostEntryIDs)
	for _, removal := range review.Removals {
		assert.Equal(t, "left_album", removal.Reason, "the assets still exist in Immich")
	}
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	requireCode(t, err, "validation_error")
	request := publishing.SyncRequest{Covers: []publishing.SyncCover{{MomentID: shared.ID, EntryID: ids["second.jpg"]}}}
	review, err = module.CheckSync(t.Context(), album.ID, request)
	require.NoError(t, err)
	assert.True(t, review.Ready)
	request.ReviewToken = review.ReviewToken
	applied, err := module.ApplySync(t.Context(), album.ID, request)
	require.NoError(t, err)
	require.Len(t, applied.Moments, 1, "the emptied July 4 Moment is gone")
	assert.Equal(t, ids["second.jpg"], momentOf(applied, "second.jpg").CoverEntryID)
	assert.False(t, hasEntry(applied, "first.jpg"))
	assert.False(t, hasEntry(applied, "last.jpg"))
	assert.True(t, applied.Published, "publication survives synchronization")
	var retained models.AlbumEntry
	require.NoError(t, db.NewSelect().Model(&retained).Where("id = ?", ids["first.jpg"]).Scan(t.Context()))
	assert.NotNil(t, retained.RemovedAt)
	assert.Nil(t, retained.MomentID)
	announced, err := updates.AnnouncedEntryIDs(t.Context(), person.ID.String())
	require.NoError(t, err)
	assert.Contains(t, announced, ids["first.jpg"], "announcement history survives removal")

	// After a restart the asset returns to the source album together with a
	// genuinely new one. Restoration keeps the entry; only the new one is
	// notification content.
	restarted := publishing.New(db, source, noQueue)
	source.assets = append(source.assets, newSyncAsset("fresh", "2026-07-05T09:00:00+14:00"))
	source.setMembers("a", "b", "clip", "fresh")
	review, err = restarted.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Additions, 2)
	byID := map[string]publishing.SyncAddition{}
	for _, addition := range review.Additions {
		byID[addition.SourceID] = addition
	}
	assert.True(t, byID["a"].Returning)
	assert.False(t, byID["fresh"].Returning)
	assert.Equal(t, shared.ID, byID["a"].SuggestedMomentID, "the July 5 Moment already holds that day")
	assert.Equal(t, shared.ID, byID["fresh"].SuggestedMomentID)
	assert.True(t, review.Ready)
	require.Len(t, review.Audience, 1)
	assert.ElementsMatch(t, []string{ids["first.jpg"], "source:fresh"}, review.Audience[0].GainedEntryIDs)
	// The cache for the returning item is stale by the time it comes back.
	source.faces = func(_ context.Context, id string) ([]immich.Face, error) {
		if id == "a" {
			return []immich.Face{{FaceID: "face-2", ID: "person-2", Name: "Sam", UpdatedAt: "v2"}}, nil
		}
		return nil, nil
	}
	restored, err := restarted.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.Equal(t, ids["first.jpg"], entryIDs(restored)["first.jpg"], "restoration preserves Album Entry identity")
	assert.Equal(t, shared.ID, momentOf(restored, "first.jpg").ID)
	var cached []models.MediaFaceAssociation
	for _, entry := range momentOf(restored, "first.jpg").Entries {
		if entry.Filename == "first.jpg" {
			require.NoError(t, db.NewSelect().Model(&cached).Where("media_item_id = ?", entry.MediaID).Scan(t.Context()))
		}
	}
	names := []string{}
	for _, face := range cached {
		names = append(names, face.SourceName)
	}
	assert.Equal(t, []string{"Sam"}, names, "joining media gets its faces refreshed even when its Media Item already existed")
	preview, err := updates.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	require.Len(t, preview.People[0].Albums, 1)
	assert.Equal(t, 1, preview.People[0].Albums[0].PhotoCount, "only the new photo is unannounced")
	assert.Equal(t, notifications.AlbumUpdated, preview.People[0].Albums[0].Status)
}

func TestSyncTrashedDeletedAndOutageNeverDeleteCuration(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	db, module, album, _ := syncedAlbums(t, source, nil)
	ids := entryIDs(album)

	// A trashed asset leaves the listing but still exists: it is a removal the
	// Curator approves, never a silent one.
	source.asset("z").Trashed = true
	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.Empty(t, review.Changes)
	require.Len(t, review.Removals, 1)
	assert.Equal(t, ids["last.jpg"], review.Removals[0].EntryID)
	assert.Equal(t, "trashed", review.Removals[0].Reason)
	assert.True(t, review.Ready)
	still, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.True(t, hasEntry(still, "last.jpg"), "a check never removes membership")

	// An outage fails the check without touching anything.
	source.offline = true
	_, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	requireCode(t, err, "immich_unavailable")
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	requireCode(t, err, "immich_unavailable")
	source.offline = false
	source.unsupported = true
	_, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	requireCode(t, err, "immich_unsupported_version")
	require.ErrorContains(t, err, "synchronization")
	source.unsupported = false
	unchanged, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Equal(t, curation(still), curation(unchanged))

	// A missing source album is reported, not treated as removing everything.
	delete(source.albums, "source")
	_, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	requireCode(t, err, "source_missing")
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Description: "From Immich", Count: 4, UpdatedAt: "2026-07-06T00:00:00Z"}

	// Approving the trashed removal retains the entry; restoring the asset
	// from the trash brings it back as returning media.
	applied, err := module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.False(t, hasEntry(applied, "last.jpg"))
	retained, err := db.NewSelect().Model((*models.AlbumEntry)(nil)).Where("id = ? AND removed_at IS NOT NULL AND excluded_at IS NULL", ids["last.jpg"]).Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, retained)
	source.asset("z").Trashed = false
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Additions, 1)
	assert.True(t, review.Additions[0].Returning)
	applied, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.Equal(t, ids["last.jpg"], entryIDs(applied)["last.jpg"])

	// Deleting the asset from Immich altogether is a removal too, and a file
	// Immich cannot find stays as unavailable media.
	source.asset("a").Offline = true
	source.asset("a").UpdatedAt = "2026-07-09T00:00:00Z"
	source.setMembers("a", "b", "clip")
	for i := range source.assets {
		if source.assets[i].ID == "z" {
			source.assets = append(source.assets[:i], source.assets[i+1:]...)
			break
		}
	}
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Removals, 1)
	assert.Equal(t, "deleted", review.Removals[0].Reason)
	require.Len(t, review.Changes, 1)
	assert.Contains(t, review.Changes[0].Fields, "availability")
	assert.False(t, review.Changes[0].NewAvailable)
	applied, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.False(t, hasEntry(applied, "last.jpg"))
	for _, entry := range momentOf(applied, "first.jpg").Entries {
		if entry.Filename == "first.jpg" {
			assert.False(t, entry.Available)
		}
	}
}

func TestSyncSharedItemCheckCancelApplyAndExtraction(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	chapters := &recordingChapters{}
	db, module, album, other := syncedAlbums(t, source, chapters)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	other, err = module.SaveAlbumAccess(t.Context(), other.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	otherVideo := findVideo(t, other)
	imported := len(chapters.requests)

	clip := source.asset("clip")
	clip.Checksum, clip.UpdatedAt = "bmV3Y2xpcA==", "2026-07-08T00:00:00Z"
	b := source.asset("b")
	b.LocalDateTime, b.UpdatedAt = "2026-07-05T12:00:00+14:00", "2026-07-08T00:00:00Z"
	a := source.albums["source"]
	a.Description = "Edited in Immich"
	source.albums["source"] = a

	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.NotNil(t, review.Description)
	assert.Equal(t, "Edited in Immich", review.Description.After)
	require.Len(t, review.Changes, 2)
	changes := map[string]publishing.SyncChange{}
	for _, change := range review.Changes {
		changes[change.Filename] = change
	}
	assert.Equal(t, []string{"checksum", "details"}, changes["family reunion.MP4"].Fields)
	assert.Equal(t, []publishing.SyncAlbumRef{{ID: other.ID, Title: "Reunion"}}, changes["family reunion.MP4"].OtherAlbums)
	assert.Equal(t, []string{"capture_time", "details"}, changes["second.jpg"].Fields)
	assert.Equal(t, "2026-07-05T12:00:00", changes["second.jpg"].NewCapturedAt)
	assert.Empty(t, changes["second.jpg"].OtherAlbums)
	assert.Empty(t, review.Audience, "metadata changes alter no one's access")
	assert.True(t, review.Ready)

	// Cancelling is doing nothing: neither Album changed beyond cached faces.
	cancelled, err := module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	assert.Equal(t, curation(other), curation(cancelled))
	assert.Len(t, chapters.requests, imported)

	applied, err := module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.Equal(t, "Edited in Immich", applied.Description)
	video := findVideo(t, applied)
	assert.NotEqual(t, otherVideo.ThumbnailURL, video.ThumbnailURL, "a changed checksum gets a new content version")
	require.Len(t, chapters.requests, imported+1)
	assert.Equal(t, "bmV3Y2xpcA==", chapters.requests[imported].checksum)
	assert.Equal(t, otherVideo.MediaID, chapters.requests[imported].item.String())
	// Global facts are consistent in the other Album while its curation is untouched.
	after, err := module.GetAlbum(t.Context(), other.ID)
	require.NoError(t, err)
	afterVideo := findVideo(t, after)
	assert.Equal(t, otherVideo.ID, afterVideo.ID)
	assert.Equal(t, video.ThumbnailURL[len("/api/media/entries/")+36:], afterVideo.ThumbnailURL[len("/api/media/entries/")+36:], "both Albums show the same content version")
	assert.Equal(t, other.Moments[0].ID, after.Moments[0].ID)
	assert.Equal(t, other.Access, after.Access)
	assert.Equal(t, "Shared", after.Description)
	unchangedPhoto := momentOf(applied, "first.jpg")
	for _, entry := range unchangedPhoto.Entries {
		if entry.Filename == "first.jpg" {
			assert.Equal(t, entryIDs(album)["first.jpg"], entry.ID)
		}
	}
	// A recheck of the other Album finds nothing left to do.
	review, err = module.CheckSync(t.Context(), other.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.UpToDate)

	// A newly synchronized video is extracted automatically.
	source.assets = append(source.assets, immich.Asset{ID: "reel", Checksum: "cmVlbA==", Filename: "reel.mov", Kind: "VIDEO", LocalDateTime: "2026-07-05T11:00:00+14:00", FileCreatedAt: "2026-07-04T21:00:00Z", UpdatedAt: "2026-07-08T00:00:00Z", Duration: new(4000)})
	source.setMembers("a", "b", "z", "clip", "reel")
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Additions, 1)
	assert.Equal(t, "VIDEO", review.Additions[0].Kind)
	applied, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	require.Len(t, chapters.requests, imported+2)
	assert.Equal(t, "cmVlbA==", chapters.requests[imported+1].checksum)
	reel := momentOf(applied, "reel.mov")
	require.NotEmpty(t, reel.ID)
	for _, entry := range reel.Entries {
		if entry.Filename == "reel.mov" {
			assert.Equal(t, chapters.requests[imported+1].item.String(), entry.MediaID)
		}
	}
}

func TestSyncFaceRefreshFailureKeepsCachedAssociations(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	source.faces = func(_ context.Context, id string) ([]immich.Face, error) {
		if id == "a" {
			return []immich.Face{{FaceID: "face-1", ID: "person-1", Name: "Alex", UpdatedAt: "v1"}}, nil
		}
		return nil, nil
	}
	db, module, album, _ := syncedAlbums(t, source, nil)
	first, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, first.FacesRefreshed)
	assert.Empty(t, first.FacesMessage)
	count, err := db.NewSelect().Model((*models.MediaFaceAssociation)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	source.faces = func(context.Context, string) ([]immich.Face, error) { return nil, errors.New("face service down") }
	second, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.False(t, second.FacesRefreshed)
	assert.Contains(t, second.FacesMessage, "cached faces remain")
	assert.True(t, second.UpToDate)
	count, err = db.NewSelect().Model((*models.MediaFaceAssociation)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, count, "a failed refresh keeps the cache")
	detail, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.NotNil(t, momentOf(detail, "first.jpg").Access.RefreshedAt)
}

func TestSyncStaleReviewsAndAtomicFailure(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	chapters := &recordingChapters{}
	_, module, album, _ := syncedAlbums(t, source, chapters)
	shared := momentOf(album, "first.jpg")
	ids := entryIDs(album)

	source.assets = append(source.assets, newSyncAsset("fresh", "2026-07-05T09:00:00+14:00"))
	source.setMembers("a", "b", "z", "clip", "fresh")
	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	assert.True(t, review.Ready)
	token := review.ReviewToken

	// Source membership changed after review: the unreviewed set is refused.
	source.assets = append(source.assets, newSyncAsset("later", "2026-07-05T10:00:00+14:00"))
	source.setMembers("a", "b", "z", "clip", "fresh", "later")
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: token})
	requireCode(t, err, "sync_changed")
	source.setMembers("a", "b", "z", "clip", "fresh")

	// The destination changed after review: the cover of the target Moment.
	_, err = module.SetMomentCover(t.Context(), album.ID, shared.ID, publishing.SetMomentCoverRequest{EntryID: ids["second.jpg"]})
	require.NoError(t, err)
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: token})
	requireCode(t, err, "sync_changed")
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{})
	requireCode(t, err, "sync_changed")
	unchanged, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.False(t, hasEntry(unchanged, "fresh.jpg"))

	// A placement into a Moment that no longer exists is invalid, not silently
	// redirected.
	_, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "fresh", MomentID: "new:not-a-date"}}})
	requireCode(t, err, "validation_error")

	// A failure inside the apply transaction leaves every prior fact in place.
	source.assets = append(source.assets, immich.Asset{ID: "reel", Checksum: "cmVlbA==", Filename: "reel.mov", Kind: "VIDEO", LocalDateTime: "2026-07-05T11:00:00+14:00", FileCreatedAt: "2026-07-04T21:00:00Z", UpdatedAt: "2026-07-08T00:00:00Z", Duration: new(4000)})
	source.setMembers("b", "z", "clip", "fresh", "reel")
	edited := source.albums["source"]
	edited.Description = "Changed"
	source.albums["source"] = edited
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Removals, 1)
	require.Len(t, review.Additions, 2)
	assert.True(t, review.Ready, "second.jpg is the cover now, so removing first.jpg needs no choice")
	chapters.fail = errors.New("queue unavailable")
	_, err = module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.ErrorContains(t, err, "queue unavailable")
	after, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	assert.Equal(t, curation(unchanged), curation(after), "a failed apply changes nothing")
	chapters.fail = nil
	applied, err := module.ApplySync(t.Context(), album.ID, publishing.SyncRequest{ReviewToken: review.ReviewToken})
	require.NoError(t, err)
	assert.Equal(t, "Changed", applied.Description)
	assert.False(t, hasEntry(applied, "first.jpg"))
	assert.True(t, hasEntry(applied, "reel.mov"))
}

func TestSyncAudienceEffectsFollowPlacement(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	db, module, album, _ := syncedAlbums(t, source, nil)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	_, err = module.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.ID.String(), Allowed: true}}})
	require.NoError(t, err)
	lonely := momentOf(album, "last.jpg")
	_, err = module.SaveMomentRules(t.Context(), album.ID, lonely.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: person.ID.String(), Decision: publishing.DecisionDeny}}})
	require.NoError(t, err)

	source.assets = append(source.assets, newSyncAsset("fresh", "2026-07-04T09:00:00-10:00"))
	source.setMembers("a", "b", "z", "clip", "fresh")
	review, err := module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{})
	require.NoError(t, err)
	require.Len(t, review.Additions, 1)
	assert.Equal(t, lonely.ID, review.Additions[0].SuggestedMomentID)
	assert.Empty(t, review.Audience, "the suggested Moment denies Alex")

	shared := momentOf(album, "first.jpg")
	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "fresh", MomentID: shared.ID}}})
	require.NoError(t, err)
	require.Len(t, review.Audience, 1)
	assert.Equal(t, "Alex", review.Audience[0].DisplayName)
	assert.Equal(t, []string{"source:fresh"}, review.Audience[0].GainedEntryIDs)

	review, err = module.CheckSync(t.Context(), album.ID, publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "fresh", MomentID: "new:2026-07-04"}}})
	require.NoError(t, err)
	require.Len(t, review.Audience, 1, "a new Moment inherits the Album allow")
	assert.Equal(t, []string{"source:fresh"}, review.Audience[0].GainedEntryIDs)
}

func TestSyncExcludedVideoIsNotExtracted(t *testing.T) {
	t.Parallel()
	source := syncFixture()
	chapters := &recordingChapters{}
	_, module, album, _ := syncedAlbums(t, source, chapters)
	imported := len(chapters.requests)
	source.assets = append(source.assets, immich.Asset{ID: "reel", Checksum: "cmVlbA==", Filename: "reel.mov", Kind: "VIDEO", LocalDateTime: "2026-07-05T11:00:00+14:00", FileCreatedAt: "2026-07-04T21:00:00Z", UpdatedAt: "2026-07-08T00:00:00Z", Duration: new(4000)})
	source.setMembers("a", "b", "z", "clip", "reel")
	exclude := publishing.SyncRequest{Placements: []publishing.SyncPlacement{{SourceID: "reel", Exclude: true}}}
	review, err := module.CheckSync(t.Context(), album.ID, exclude)
	require.NoError(t, err)
	assert.False(t, review.FacesRefreshed, "a check carrying decisions leaves cached faces alone")
	exclude.ReviewToken = review.ReviewToken
	applied, err := module.ApplySync(t.Context(), album.ID, exclude)
	require.NoError(t, err)
	assert.False(t, hasEntry(applied, "reel.mov"))
	assert.Len(t, chapters.requests, imported, "media kept out of every Album is never probed")

	// Adding it back later extracts it like any newly synchronized video.
	require.Len(t, applied.Excluded, 1)
	include := publishing.IncludeEntryRequest{MomentID: momentOf(album, "first.jpg").ID}
	preview, err := module.PreviewInclude(t.Context(), album.ID, applied.Excluded[0].ID, include)
	require.NoError(t, err)
	include.ReviewToken = preview.ReviewToken
	applied, err = module.IncludeEntry(t.Context(), album.ID, applied.Excluded[0].ID, include)
	require.NoError(t, err)
	assert.True(t, hasEntry(applied, "reel.mov"))
	require.Len(t, chapters.requests, imported+1)
	assert.Equal(t, "cmVlbA==", chapters.requests[imported].checksum)
}
