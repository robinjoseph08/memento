package publishing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type chapterRequest struct {
	item     models.UUID
	checksum string
}

// recordingChapters stands in for Media's chapter service.
type recordingChapters struct {
	requests []chapterRequest
	retried  []string
	fail     error
}

func (c *recordingChapters) RequestChapters(_ context.Context, tx bun.Tx, id models.UUID, checksum string) error {
	if tx.Tx == nil {
		return errors.New("request outside the import transaction")
	}
	c.requests = append(c.requests, chapterRequest{id, checksum})
	return c.fail
}

func (c *recordingChapters) RetryChapters(_ context.Context, id string) error {
	c.retried = append(c.retried, id)
	return c.fail
}

func videoLibrary() *library {
	source := fixture()
	source.assets = append(source.assets, immich.Asset{ID: "clip", Checksum: "Y2xpcA==", Filename: "family reunion.MP4", Kind: "VIDEO", LocalDateTime: "2026-07-05T10:00:00+14:00", FileCreatedAt: "2026-07-04T20:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z", Duration: new(9000)})
	source.albums["source"] = immich.Album{ID: "source", Name: "Summer", Description: "From Immich", Count: 4}
	source.albums["other"] = immich.Album{ID: "other", Name: "Reunion", Count: 4}
	return source
}

func mustParse(t *testing.T, value string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(value)
	require.NoError(t, err)
	return id
}

func findVideo(t *testing.T, detail publishing.AlbumDetail) publishing.Entry {
	t.Helper()
	for _, moment := range detail.Moments {
		for _, entry := range moment.Entries {
			if entry.Kind == "VIDEO" {
				return entry
			}
		}
	}
	t.Fatal("album has no video")
	return publishing.Entry{}
}

func TestImportRequestsChaptersForEveryCommittedVideo(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	chapters := &recordingChapters{}
	module := publishing.New(db, videoLibrary(), noQueue)
	module.Chapters = chapters
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	detail, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	video := findVideo(t, detail)
	require.Equal(t, []chapterRequest{{models.UUID(mustParse(t, video.MediaID)), "Y2xpcA=="}}, chapters.requests, "photos are never probed")
	require.Equal(t, "pending", video.ChapterStatus)
	require.Empty(t, video.Title, "no override until a Curator sets one")
	require.Equal(t, "/api/media/entries/"+video.ID+"/playback?v="+strings.Split(video.ThumbnailURL, "?v=")[1], video.PlaybackURL)
	require.Empty(t, detail.Moments[0].Entries[0].PlaybackURL, "photos have no playback")
	// The same Media Item in another Album asks again; Media decides whether
	// the checksum still has a result.
	other, err := module.StartImport(t.Context(), "other")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), other.ID))
	require.Len(t, chapters.requests, 2)
	require.Equal(t, chapters.requests[0], chapters.requests[1])

	// A refused request fails the import instead of committing a video without work.
	failing := &recordingChapters{fail: errors.New("queue unavailable")}
	source := videoLibrary()
	source.albums["third"] = immich.Album{ID: "third", Name: "Third", Count: 4}
	strict := publishing.New(db, source, noQueue)
	strict.Chapters = failing
	third, err := strict.StartImport(t.Context(), "third")
	require.NoError(t, err)
	require.Error(t, strict.ExecuteImport(t.Context(), third.ID))
	status, err := strict.GetAlbum(t.Context(), third.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", status.Status)
}

func TestVideoTitleIsGlobalWithFilenameFallback(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	module := publishing.New(db, videoLibrary(), noQueue)
	first, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), first.ID))
	second, err := module.StartImport(t.Context(), "other")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), second.ID))
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)
	detail, err := module.GetAlbum(t.Context(), first.ID)
	require.NoError(t, err)
	video := findVideo(t, detail)
	photo := detail.Moments[0].Entries[0]
	if photo.Kind == "VIDEO" {
		photo = detail.Moments[0].Entries[1]
	}
	viewerTitle := func(albumID string) string {
		page, err := module.ViewEntries(t.Context(), curator.ID.String(), "", albumID, "VIDEO", "")
		require.NoError(t, err)
		require.Len(t, page.Entries, 1)
		return page.Entries[0].Title
	}
	require.Equal(t, "family reunion", viewerTitle(first.ID), "the fallback drops only the extension")

	updated, err := module.UpdateVideo(t.Context(), first.ID, video.ID, publishing.UpdateVideoRequest{Title: "  Reunion highlights  "})
	require.NoError(t, err)
	require.Equal(t, "Reunion highlights", findVideo(t, updated).Title)
	require.Equal(t, "Reunion highlights", viewerTitle(first.ID))
	require.Equal(t, "Reunion highlights", viewerTitle(second.ID), "the title belongs to the Media Item, so both Albums show it")
	otherDetail, err := module.GetAlbum(t.Context(), second.ID)
	require.NoError(t, err)
	require.Equal(t, "Reunion highlights", findVideo(t, otherDetail).Title)

	cleared, err := module.UpdateVideo(t.Context(), first.ID, video.ID, publishing.UpdateVideoRequest{Title: "   "})
	require.NoError(t, err)
	require.Empty(t, findVideo(t, cleared).Title)
	require.Equal(t, "family reunion", viewerTitle(second.ID), "clearing restores the filename everywhere")

	_, err = module.UpdateVideo(t.Context(), first.ID, video.ID, publishing.UpdateVideoRequest{Title: repeatRune('x', 201)})
	var fieldErr *errcodes.FieldError
	require.ErrorAs(t, err, &fieldErr)
	require.Contains(t, fieldErr.Fields, "title")
	_, err = module.UpdateVideo(t.Context(), first.ID, photo.ID, publishing.UpdateVideoRequest{Title: "Nope"})
	require.ErrorIs(t, err, errcodes.NotFound("Video"), "photos have no title override")
	_, err = module.UpdateVideo(t.Context(), second.ID, video.ID, publishing.UpdateVideoRequest{Title: "Nope"})
	require.ErrorIs(t, err, errcodes.NotFound("Video"), "an Album Entry is edited through its own Album")
	_, err = module.UpdateVideo(t.Context(), first.ID, "junk", publishing.UpdateVideoRequest{Title: "Nope"})
	require.ErrorIs(t, err, errcodes.NotFound("Video"))
}

func TestRetryChaptersResolvesTheVideoBehindAnAlbumEntry(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	module := publishing.New(db, videoLibrary(), noQueue)
	album, err := module.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, module.ExecuteImport(t.Context(), album.ID))
	detail, err := module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	video := findVideo(t, detail)
	_, err = module.RetryChapters(t.Context(), album.ID, video.ID)
	require.Error(t, err, "an installation without extraction says so")
	chapters := &recordingChapters{}
	module.Chapters = chapters
	_, err = module.RetryChapters(t.Context(), album.ID, video.ID)
	require.NoError(t, err)
	require.Equal(t, []string{video.MediaID}, chapters.retried)
	photo := detail.Moments[0].Entries[0]
	if photo.Kind == "VIDEO" {
		photo = detail.Moments[0].Entries[1]
	}
	_, err = module.RetryChapters(t.Context(), album.ID, photo.ID)
	require.ErrorIs(t, err, errcodes.NotFound("Video"))
	require.Len(t, chapters.retried, 1)

	// Stored results reach both projections with their public status.
	row := models.MediaChapterResult{MediaItemID: models.UUID(mustParse(t, video.MediaID)), Checksum: "Y2xpcA==", Status: "complete",
		Chapters: []models.Chapter{{Title: "Arrival", Start: 0, End: 2.5}, {Title: "", Start: 2.5, End: 9}}, UpdatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&row).Exec(t.Context())
	require.NoError(t, err)
	detail, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	video = findVideo(t, detail)
	require.Equal(t, "complete", video.ChapterStatus)
	want := []publishing.Chapter{{Title: "Arrival", Start: 0, End: 2.5}, {Title: "", Start: 2.5, End: 9}}
	require.Equal(t, want, video.Chapters)
	curator := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true, CreatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(&curator).Exec(t.Context())
	require.NoError(t, err)
	page, err := module.ViewEntries(t.Context(), curator.ID.String(), "", album.ID, "VIDEO", "")
	require.NoError(t, err)
	require.Equal(t, "complete", page.Entries[0].ChapterStatus)
	require.Equal(t, want, page.Entries[0].Chapters)
	_, err = db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'failed'").Set("message = 'Chapter extraction failed.'").Where("media_item_id = ?", row.MediaItemID).Exec(t.Context())
	require.NoError(t, err)
	detail, err = module.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	video = findVideo(t, detail)
	require.Equal(t, "failed", video.ChapterStatus)
	require.Equal(t, "Chapter extraction failed.", video.ChapterMessage)
	failures, err := module.ChapterFailures(t.Context())
	require.NoError(t, err)
	var momentID string
	for _, moment := range detail.Moments {
		for _, entry := range moment.Entries {
			if entry.ID == video.ID {
				momentID = moment.ID
			}
		}
	}
	require.Equal(t, []publishing.ChapterFailure{{AlbumID: album.ID, AlbumTitle: detail.Title, MomentID: momentID, EntryID: video.ID, Title: video.Filename[:len(video.Filename)-4], Message: "Chapter extraction failed."}}, failures, "a failed extraction is addressed through its Album Entry")
	review, err := module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	require.Empty(t, review.Blockers, "chapter failure never blocks publication")
	require.Contains(t, review.Warnings, "1 video without chapter data after a failed check. Playback works; retry from the video details.")
	_, err = db.NewUpdate().Model((*models.MediaChapterResult)(nil)).Set("status = 'complete'").Set("chapters = '[]'::jsonb").Where("media_item_id = ?", row.MediaItemID).Exec(t.Context())
	require.NoError(t, err)
	failures, err = module.ChapterFailures(t.Context())
	require.NoError(t, err)
	require.Empty(t, failures)
	review, err = module.ReviewPublication(t.Context(), album.ID)
	require.NoError(t, err)
	for _, warning := range review.Warnings {
		require.NotContains(t, warning, "chapter", "no chapters is a normal state with no warning")
	}
}

func repeatRune(r rune, n int) string {
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = r
	}
	return string(runes)
}
