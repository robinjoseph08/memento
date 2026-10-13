package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/uptrace/bun"
)

// verifyCases runs after the original source snapshot because SetupCases adds an album.
// source may override only the import gate for an explicitly requested compatibility probe.
func verifyCases(ctx context.Context, db *bun.DB, library *fixture.Library, source immich.Library) error {
	if err := library.SetupCases(ctx); err != nil {
		return err
	}
	raw := library.Source()
	raw.Probe = ffprobe.Command{}
	ready, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	photos := append([]fixture.Asset{library.PNG, library.LivePhoto}, library.Stack...)
	if err := waitForMedia(ready, raw, photos); err != nil {
		return err
	}
	if err := waitForVideo(ready, raw, library.PlainVideo); err != nil {
		return err
	}
	published, err := verifyCaseImport(ctx, db, library, source, raw)
	if err != nil {
		return err
	}
	return verifyTouchThenEdit(ctx, library, raw, published)
}

// publishedCase is the extra-cases Album after publication, for checks that
// change the source afterward.
type publishedCase struct {
	module       *publishing.Module
	albumID      string
	viewerID     string
	handler      http.Handler
	thumbnailURL string
}

// verifyCaseImport keeps the external-job wait separate from module and HTTP
// checks. It returns the published Album with the PNG's viewer thumbnail.
func verifyCaseImport(ctx context.Context, db *bun.DB, library *fixture.Library, source immich.Library, transport media.Source) (publishedCase, error) {
	live, err := source.GetAsset(ctx, library.LivePhoto.ID)
	if err != nil {
		return publishedCase{}, err
	}
	if live.LivePhotoVideoID == nil || *live.LivePhotoVideoID != library.LiveMotion.ID {
		return publishedCase{}, fmt.Errorf("the Live Photo did not retain its paired motion asset")
	}
	for _, expected := range library.Stack {
		actual, err := source.GetAsset(ctx, expected.ID)
		if err != nil {
			return publishedCase{}, err
		}
		if actual.Stack == nil || actual.Stack.ID != library.StackID || actual.Stack.PrimaryAssetID != library.Stack[0].ID || actual.Stack.AssetCount != 2 {
			return publishedCase{}, fmt.Errorf("fixture stack metadata differs")
		}
	}
	module := publishing.New(db, source, func(context.Context, bun.Tx, string) error { return nil })
	delivery := media.New(db, transport)
	tasks := map[string]string{}
	delivery.EnqueueChapters = func(_ context.Context, tx bun.Tx, id, checksum string) error {
		if tx.Tx == nil {
			return fmt.Errorf("chapter task was not transactional")
		}
		if _, exists := tasks[id]; exists {
			return fmt.Errorf("duplicate case chapter task")
		}
		tasks[id] = checksum
		return nil
	}
	module.Chapters = delivery
	pending, err := module.StartImport(ctx, library.CasesAlbum.ID)
	if err != nil {
		return publishedCase{}, err
	}
	if err := module.ExecuteImport(ctx, pending.ID); err != nil {
		return publishedCase{}, err
	}
	detail, err := module.GetAlbum(ctx, pending.ID)
	if err != nil {
		return publishedCase{}, err
	}
	if detail.Status != "complete" || detail.Published || detail.PhotoCount != 4 || detail.VideoCount != 1 || detail.Processed != 5 {
		return publishedCase{}, fmt.Errorf("extra cases did not import four photos and one video")
	}
	filenames := []string{}
	for _, moment := range detail.Moments {
		for _, entry := range moment.Entries {
			filenames = append(filenames, entry.Filename)
			if !entry.Available {
				return publishedCase{}, fmt.Errorf("extra case entry unavailable")
			}
			if err := checkMediaEndpoint(ctx, curatorMediaHandler(delivery), entry.ThumbnailURL); err != nil {
				return publishedCase{}, err
			}
			if entry.Kind == "VIDEO" {
				checksum, ok := tasks[entry.MediaID]
				if !ok || entry.ChapterStatus != "pending" {
					return publishedCase{}, fmt.Errorf("unchaptered video did not queue extraction")
				}
				if err := delivery.ExtractChapters(ctx, entry.MediaID, checksum, true); err != nil {
					return publishedCase{}, err
				}
			}
		}
	}
	expected := []string{library.PNG.Filename, library.LivePhoto.Filename, library.Stack[0].Filename, library.Stack[1].Filename, library.PlainVideo.Filename}
	slices.Sort(filenames)
	slices.Sort(expected)
	if !slices.Equal(filenames, expected) || len(tasks) != 1 {
		return publishedCase{}, fmt.Errorf("stack was not flattened or Live Photo motion became a separate entry")
	}
	people := identity.New(db, nil)
	curator, err := people.SignIn(ctx, identity.Claims{Email: "curator@example.test", EmailVerified: true, DisplayName: "Smoke Curator"})
	if err != nil {
		return publishedCase{}, err
	}
	viewer, err := people.CreatePerson(ctx, curator.Token, identity.CreatePersonRequest{DisplayName: "Extra cases viewer"})
	if err != nil {
		return publishedCase{}, err
	}
	if _, err := module.SaveAlbumAccess(ctx, detail.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: viewer.ID, Allowed: true}}}); err != nil {
		return publishedCase{}, err
	}
	review, err := module.ReviewPublication(ctx, detail.ID)
	if err != nil {
		return publishedCase{}, err
	}
	if len(review.Blockers) != 0 {
		return publishedCase{}, fmt.Errorf("extra cases blocked publication")
	}
	if _, err := module.PublishAlbum(ctx, detail.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken}); err != nil {
		return publishedCase{}, err
	}
	handler := publishingMediaHandler(module, delivery, viewer)
	photos, err := module.ViewEntries(ctx, viewer.ID, "", detail.ID, "IMAGE", publishing.EntryPageRequest{})
	if err != nil {
		return publishedCase{}, err
	}
	if len(photos.Entries) != 4 || photos.NextCursor != "" {
		return publishedCase{}, fmt.Errorf("viewer lost a stack member or Live Photo still")
	}
	pngThumbnail := ""
	for _, entry := range photos.Entries {
		if entry.PlaybackURL != "" {
			return publishedCase{}, fmt.Errorf("the Live Photo motion was exposed as photo playback")
		}
		for _, path := range []string{entry.ThumbnailURL, entry.PreviewURL} {
			if err := checkMediaEndpoint(ctx, handler, path); err != nil {
				return publishedCase{}, err
			}
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, entry.DownloadURL, nil))
		if recorder.Code != http.StatusOK {
			return publishedCase{}, fmt.Errorf("photo original returned HTTP %d", recorder.Code)
		}
		if bytes.Equal(recorder.Body.Bytes(), library.PNGBytes) {
			pngThumbnail = entry.ThumbnailURL
		}
	}
	if pngThumbnail == "" {
		return publishedCase{}, fmt.Errorf("non-JPEG case disappeared from the gallery")
	}
	videos, err := module.ViewEntries(ctx, viewer.ID, "", detail.ID, "VIDEO", publishing.EntryPageRequest{})
	if err != nil {
		return publishedCase{}, err
	}
	if len(videos.Entries) != 1 || videos.NextCursor != "" {
		return publishedCase{}, fmt.Errorf("paired Live Photo created a second viewer video")
	}
	video := videos.Entries[0]
	if video.ChapterStatus != "complete" || len(video.Chapters) != 0 || video.PlaybackURL == "" || video.DownloadURL == "" {
		return publishedCase{}, fmt.Errorf("unchaptered video did not complete extraction with zero chapters")
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, video.PlaybackURL, nil)
	request.Header.Set("Range", "bytes=0-9")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent || recorder.Body.Len() != 10 {
		return publishedCase{}, fmt.Errorf("unchaptered video did not serve a playback range")
	}
	fmt.Println("Extra cases: non-JPEG generated variants and original, paired Live Photo, flattened stack, and playable video with zero chapters verified")
	return publishedCase{module: module, albumID: detail.ID, viewerID: viewer.ID, handler: handler, thumbnailURL: pngThumbnail}, nil
}

// verifyTouchThenEdit proves a published photo keeps serving after it is
// touched in Immich without changing what it shows, as favoriting or a
// thumbnail rebuild does. Once it is cropped in Immich's editor, which keeps
// the original checksum, its old URL stops serving and synchronization
// reviews the crop and issues a URL that serves.
func verifyTouchThenEdit(ctx context.Context, library *fixture.Library, source media.Source, published publishedCase) error {
	before, err := source.GetAsset(ctx, library.PNG.ID)
	if err != nil {
		return err
	}
	if before.Edited {
		return fmt.Errorf("an uploaded photo reported Immich edits")
	}
	if err := library.Favorite(ctx, library.PNG.ID); err != nil {
		return err
	}
	if err := waitForAsset(ctx, source, library.PNG.ID, func(a immich.Asset) bool { return a.UpdatedAt != before.UpdatedAt }); err != nil {
		return fmt.Errorf("favoriting did not touch the asset: %w", err)
	}
	if err := checkMediaEndpoint(ctx, published.handler, published.thumbnailURL); err != nil {
		return fmt.Errorf("after favoriting: %w", err)
	}
	review, err := published.module.CheckSync(ctx, published.albumID, publishing.SyncRequest{})
	if err != nil {
		return err
	}
	if !review.UpToDate {
		return fmt.Errorf("favoriting was reported as a change to review")
	}
	if err := library.Crop(ctx, library.PNG.ID, 32, 24); err != nil {
		return err
	}
	if err := waitForAsset(ctx, source, library.PNG.ID, func(a immich.Asset) bool {
		return a.Edited && a.Width != nil && *a.Width == 32 && a.Checksum == before.Checksum
	}); err != nil {
		return fmt.Errorf("crop was not reported as an edit with new dimensions: %w", err)
	}
	recorder := httptest.NewRecorder()
	published.handler.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, published.thumbnailURL, nil))
	if recorder.Code != http.StatusNotFound {
		return fmt.Errorf("a cropped photo served HTTP %d under its old URL", recorder.Code)
	}
	review, err = published.module.CheckSync(ctx, published.albumID, publishing.SyncRequest{})
	if err != nil {
		return err
	}
	if len(review.Changes) != 1 || !slices.Contains(review.Changes[0].Fields, "details") {
		return fmt.Errorf("the crop was not reviewed as one changed photo")
	}
	if _, err := published.module.ApplySync(ctx, published.albumID, publishing.SyncRequest{ReviewToken: review.ReviewToken}); err != nil {
		return err
	}
	photos, err := published.module.ViewEntries(ctx, published.viewerID, "", published.albumID, "IMAGE", publishing.EntryPageRequest{})
	if err != nil {
		return err
	}
	entryPath, _, _ := strings.Cut(published.thumbnailURL, "?")
	cropped := ""
	for _, entry := range photos.Entries {
		if path, _, _ := strings.Cut(entry.ThumbnailURL, "?"); path == entryPath {
			cropped = entry.ThumbnailURL
		}
	}
	if cropped == "" || cropped == published.thumbnailURL {
		return fmt.Errorf("applying the crop did not issue a new photo URL")
	}
	if err := checkMediaEndpoint(ctx, published.handler, cropped); err != nil {
		return fmt.Errorf("after applying the crop: %w", err)
	}
	fmt.Println("A favorited photo kept serving and needed no sync review; a crop in Immich's editor stopped its old URL until synchronization issued a new one")
	return nil
}

func waitForAsset(ctx context.Context, source media.Source, id string, ready func(immich.Asset) bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var last error
	for {
		asset, err := source.GetAsset(ctx, id)
		if err == nil && ready(asset) {
			return nil
		}
		if err != nil {
			last = err
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), last)
		case <-time.After(time.Second):
		}
	}
}
