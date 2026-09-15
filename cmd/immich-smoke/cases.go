package main

import (
	"bytes"
	"context"
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
	return verifyCaseImport(ctx, db, library, source, raw)
}

// verifyCaseImport keeps the external-job wait separate from module and HTTP checks.
func verifyCaseImport(ctx context.Context, db *bun.DB, library *fixture.Library, source immich.Library, transport media.Source) error {
	live, err := source.GetAsset(ctx, library.LivePhoto.ID)
	if err != nil {
		return err
	}
	if live.LivePhotoVideoID == nil || *live.LivePhotoVideoID != library.LiveMotion.ID {
		return fmt.Errorf("the Live Photo did not retain its paired motion asset")
	}
	for _, expected := range library.Stack {
		actual, err := source.GetAsset(ctx, expected.ID)
		if err != nil {
			return err
		}
		if actual.Stack == nil || actual.Stack.ID != library.StackID || actual.Stack.PrimaryAssetID != library.Stack[0].ID || actual.Stack.AssetCount != 2 {
			return fmt.Errorf("fixture stack metadata differs")
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
		return err
	}
	if err := module.ExecuteImport(ctx, pending.ID); err != nil {
		return err
	}
	detail, err := module.GetAlbum(ctx, pending.ID)
	if err != nil {
		return err
	}
	if detail.Status != "complete" || detail.Published || detail.PhotoCount != 4 || detail.VideoCount != 1 || detail.Processed != 5 {
		return fmt.Errorf("extra cases did not import four photos and one video")
	}
	filenames := []string{}
	for _, moment := range detail.Moments {
		for _, entry := range moment.Entries {
			filenames = append(filenames, entry.Filename)
			if !entry.Available {
				return fmt.Errorf("extra case entry unavailable")
			}
			if err := checkMediaEndpoint(ctx, curatorMediaHandler(delivery), entry.ThumbnailURL); err != nil {
				return err
			}
			if entry.Kind == "VIDEO" {
				checksum, ok := tasks[entry.MediaID]
				if !ok || entry.ChapterStatus != "pending" {
					return fmt.Errorf("unchaptered video did not queue extraction")
				}
				if err := delivery.ExtractChapters(ctx, entry.MediaID, checksum, true); err != nil {
					return err
				}
			}
		}
	}
	expected := []string{library.PNG.Filename, library.LivePhoto.Filename, library.Stack[0].Filename, library.Stack[1].Filename, library.PlainVideo.Filename}
	slices.Sort(filenames)
	slices.Sort(expected)
	if !slices.Equal(filenames, expected) || len(tasks) != 1 {
		return fmt.Errorf("stack was not flattened or Live Photo motion became a separate entry")
	}
	people := identity.New(db, nil)
	curator, err := people.SignIn(ctx, identity.Claims{Provider: "fake", Subject: "smoke-curator", Email: "curator@example.test", EmailVerified: true, DisplayName: "Smoke Curator"})
	if err != nil {
		return err
	}
	viewer, err := people.CreatePerson(ctx, curator.Token, identity.CreatePersonRequest{DisplayName: "Extra cases viewer"})
	if err != nil {
		return err
	}
	if _, err := module.SaveAlbumAccess(ctx, detail.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: viewer.ID, Allowed: true}}}); err != nil {
		return err
	}
	review, err := module.ReviewPublication(ctx, detail.ID)
	if err != nil {
		return err
	}
	if len(review.Blockers) != 0 {
		return fmt.Errorf("extra cases blocked publication")
	}
	if _, err := module.PublishAlbum(ctx, detail.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken}); err != nil {
		return err
	}
	handler := publishingMediaHandler(module, delivery, viewer)
	photos, err := module.ViewEntries(ctx, viewer.ID, "", detail.ID, "IMAGE", publishing.EntryPageRequest{})
	if err != nil {
		return err
	}
	if len(photos.Entries) != 4 || photos.NextCursor != "" {
		return fmt.Errorf("viewer lost a stack member or Live Photo still")
	}
	pngChecked := false
	for _, entry := range photos.Entries {
		if entry.PlaybackURL != "" {
			return fmt.Errorf("the Live Photo motion was exposed as photo playback")
		}
		for _, path := range []string{entry.ThumbnailURL, entry.PreviewURL} {
			if err := checkMediaEndpoint(ctx, handler, path); err != nil {
				return err
			}
		}
		if entry.Title == strings.TrimSuffix(library.PNG.Filename, ".png") {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, entry.DownloadURL, nil))
			if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), library.PNGBytes) {
				return fmt.Errorf("PNG original download differs from uploaded non-JPEG file")
			}
			pngChecked = true
		}
	}
	if !pngChecked {
		return fmt.Errorf("non-JPEG case disappeared from the gallery")
	}
	videos, err := module.ViewEntries(ctx, viewer.ID, "", detail.ID, "VIDEO", publishing.EntryPageRequest{})
	if err != nil {
		return err
	}
	if len(videos.Entries) != 1 || videos.NextCursor != "" {
		return fmt.Errorf("paired Live Photo created a second viewer video")
	}
	video := videos.Entries[0]
	if video.ChapterStatus != "complete" || len(video.Chapters) != 0 || video.PlaybackURL == "" || video.DownloadURL == "" {
		return fmt.Errorf("unchaptered video did not complete extraction with zero chapters")
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, video.PlaybackURL, nil)
	request.Header.Set("Range", "bytes=0-9")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent || recorder.Body.Len() != 10 {
		return fmt.Errorf("unchaptered video did not serve a playback range")
	}
	fmt.Println("Extra cases: non-JPEG generated variants and original, paired Live Photo, flattened stack, and playable video with zero chapters verified")
	return nil
}
