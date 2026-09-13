package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/uptrace/bun"
)

// verifyPublishing uses Memento's public modules and production media HTTP routes.
// Only sign-in middleware is replaced, with a fixed actor per in-process handler.
func verifyPublishing(ctx context.Context, db *bun.DB, module *publishing.Module, delivery *media.Module, imported []publishing.AlbumDetail, uploaded []fixture.Asset) error {
	people := identity.New(db, nil)
	curator, err := people.SignIn(ctx, identity.Claims{Provider: "fake", Subject: "smoke-curator", Email: "curator@example.test", EmailVerified: true, DisplayName: "Smoke Curator"})
	if err != nil {
		return err
	}
	alex, err := people.CreatePerson(ctx, curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	if err != nil {
		return err
	}
	sam, err := people.CreatePerson(ctx, curator.Token, identity.CreatePersonRequest{DisplayName: "Sam"})
	if err != nil {
		return err
	}
	a, b := imported[0], imported[1]
	if len(a.Moments) != 3 || len(b.Moments) != 1 {
		return fmt.Errorf("publishing fixture requires three Moments and an overlapping Album")
	}
	first, middle, last := a.Moments[0], a.Moments[1], a.Moments[2]
	if _, err := module.SaveAlbumAccess(ctx, a.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex.ID, Allowed: true}}}); err != nil {
		return err
	}
	if _, err := module.SaveMomentRules(ctx, a.ID, first.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID, Decision: publishing.DecisionDeny}}}); err != nil {
		return err
	}
	if _, err := module.SaveEntryRules(ctx, a.ID, middle.CoverEntryID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID, Decision: publishing.DecisionDeny}}}); err != nil {
		return err
	}
	if _, err := module.SaveMomentRules(ctx, a.ID, first.ID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: sam.ID, Decision: publishing.DecisionAllow}}}); err != nil {
		return err
	}
	if _, err := module.SaveAlbumAccess(ctx, b.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: alex.ID, Allowed: true}}}); err != nil {
		return err
	}
	curatorHTTP := publishingMediaHandler(module, delivery, curator.Person)
	alexHTTP := publishingMediaHandler(module, delivery, alex)
	samHTTP := publishingMediaHandler(module, delivery, sam)
	alexPreview, err := verifyViewer(ctx, module, curatorHTTP, curator.Person.ID, alex.ID, a.ID, 2, last.CoverEntryID)
	if err != nil {
		return err
	}
	samPreview, err := verifyViewer(ctx, module, curatorHTTP, curator.Person.ID, sam.ID, a.ID, 1, first.CoverEntryID)
	if err != nil {
		return err
	}
	if alexPreview.CoverURL == samPreview.CoverURL {
		return fmt.Errorf("selected Persons reused the same preview cover")
	}
	if _, err := module.ViewAlbum(ctx, alex.ID, "", a.ID); !isNotFound(err) {
		if err != nil {
			return fmt.Errorf("unpublished Album returned an unexpected error: %w", err)
		}
		return fmt.Errorf("ordinary viewer could read an unpublished Album")
	}
	if err := deniedMedia(ctx, alexHTTP, alexPreview.CoverURL, http.StatusForbidden); err != nil {
		return fmt.Errorf("ordinary viewer forged Curator preview: %w", err)
	}
	if err := deniedMedia(ctx, curatorHTTP, strings.Replace(alexPreview.CoverURL, "/thumbnail?", "/original?", 1), http.StatusNotFound); err != nil {
		return fmt.Errorf("preview enabled original download: %w", err)
	}
	// With every configured cover denied, the remaining middle photo must not
	// become an arbitrary Album cover.
	if _, err := module.SaveEntryRules(ctx, a.ID, last.CoverEntryID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID, Decision: publishing.DecisionDeny}}}); err != nil {
		return err
	}
	if _, err := verifyViewer(ctx, module, curatorHTTP, curator.Person.ID, alex.ID, a.ID, 1, ""); err != nil {
		return err
	}
	if _, err := module.SaveEntryRules(ctx, a.ID, last.CoverEntryID, publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: alex.ID, Decision: publishing.DecisionInherit}}}); err != nil {
		return err
	}
	for _, album := range []publishing.AlbumDetail{a, b} {
		review, err := module.ReviewPublication(ctx, album.ID)
		if err != nil {
			return err
		}
		if len(review.Blockers) != 0 || review.ReviewToken == "" {
			return fmt.Errorf("valid scoped Album was blocked from publication")
		}
		if album.ID == a.ID {
			want := []publishing.PublicationAudience{{PersonID: alex.ID, DisplayName: "Alex", AccessibleCount: 2}, {PersonID: sam.ID, DisplayName: "Sam", AccessibleCount: 1}}
			if !reflect.DeepEqual(review.Audience, want) {
				return fmt.Errorf("publication audience did not match scoped previews: %+v", review.Audience)
			}
		}
		published, err := module.PublishAlbum(ctx, album.ID, publishing.PublishRequest{ReviewToken: review.ReviewToken})
		if err != nil {
			return err
		}
		if !published.Published {
			return fmt.Errorf("publication did not make the Album published")
		}
	}
	alexView, err := verifyViewer(ctx, module, alexHTTP, alex.ID, "", a.ID, 2, last.CoverEntryID)
	if err != nil {
		return err
	}
	if err := verifyDownloads(ctx, module, alexHTTP, samHTTP, alex.ID, a.ID, uploaded); err != nil {
		return fmt.Errorf("original photo downloads: %w", err)
	}
	if _, err := verifyViewer(ctx, module, samHTTP, sam.ID, "", a.ID, 1, first.CoverEntryID); err != nil {
		return err
	}
	if _, err := verifyViewer(ctx, module, alexHTTP, alex.ID, "", b.ID, 2, b.Moments[0].CoverEntryID); err != nil {
		return err
	}
	for _, actor := range []struct {
		id      string
		handler http.Handler
		denied  string
	}{{alex.ID, alexHTTP, middle.CoverEntryID}, {sam.ID, samHTTP, last.CoverEntryID}} {
		for _, entry := range append(append([]publishing.Entry{}, middle.Entries...), last.Entries...) {
			if entry.ID == actor.denied {
				if err := deniedMedia(ctx, actor.handler, viewerMediaURL(actor.id, entry), http.StatusNotFound); err != nil {
					return fmt.Errorf("denied Album Entry delivered shared media: %w", err)
				}
			}
		}
	}
	if err := deniedMedia(ctx, samHTTP, alexView.CoverURL, http.StatusNotFound); err != nil {
		return fmt.Errorf("viewer media URL crossed Person identity: %w", err)
	}
	if _, err := module.UnpublishAlbum(ctx, a.ID); err != nil {
		return err
	}
	if _, err := module.ViewAlbum(ctx, alex.ID, "", a.ID); !isNotFound(err) {
		if err != nil {
			return fmt.Errorf("unpublished Album returned an unexpected error: %w", err)
		}
		return fmt.Errorf("unpublished Album remained visible")
	}
	if err := deniedMedia(ctx, alexHTTP, alexView.CoverURL, http.StatusNotFound); err != nil {
		return fmt.Errorf("unpublication retained media access: %w", err)
	}
	if _, err := verifyViewer(ctx, module, curatorHTTP, curator.Person.ID, alex.ID, a.ID, 2, last.CoverEntryID); err != nil {
		return fmt.Errorf("unpublication broke Curator preview: %w", err)
	}
	beforeDelete, err := module.GetAlbum(ctx, b.ID)
	if err != nil {
		return err
	}
	if err := module.DeleteAlbum(ctx, a.ID, publishing.DeleteAlbumRequest{Title: a.Title + " wrong"}); err == nil {
		return fmt.Errorf("album deletion accepted the wrong confirmation title")
	}
	if err := module.DeleteAlbum(ctx, a.ID, publishing.DeleteAlbumRequest{Title: a.Title}); err != nil {
		return err
	}
	if err := deniedMedia(ctx, curatorHTTP, alexPreview.CoverURL, http.StatusNotFound); err != nil {
		return fmt.Errorf("deleted Album still delivered preview media: %w", err)
	}
	afterDelete, err := module.GetAlbum(ctx, b.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeDelete, afterDelete) {
		return fmt.Errorf("deleting an overlapping Album changed surviving metadata or access")
	}
	if _, err := verifyViewer(ctx, module, alexHTTP, alex.ID, "", b.ID, 2, b.Moments[0].CoverEntryID); err != nil {
		return fmt.Errorf("deleting a shared Album broke surviving media: %w", err)
	}
	albums, err := module.ViewAlbums(ctx, alex.ID)
	if err != nil {
		return err
	}
	if len(albums) != 1 || albums[0].ID != b.ID {
		return fmt.Errorf("viewer listing did not retain only the surviving published Album")
	}
	fmt.Println("Publishing: two Person previews, configured covers and placeholder, real authorized thumbnail and original bytes, denial, unpublish, and shared deletion verified")
	return nil
}

// verifyDownloads streams every authorized original through the production
// route and compares it byte for byte with the uploaded fixture file. The
// same URLs must stay neutral for another Person and in preview.
func verifyDownloads(ctx context.Context, module *publishing.Module, viewer, other http.Handler, actorID, albumID string, uploaded []fixture.Asset) error {
	page, err := module.ViewEntries(ctx, actorID, "", albumID, "IMAGE", "")
	if err != nil {
		return err
	}
	if len(page.Entries) == 0 {
		return fmt.Errorf("no authorized photos to download")
	}
	for _, entry := range page.Entries {
		if entry.DownloadURL == "" {
			return fmt.Errorf("authorized photo %q has no download URL", entry.Title)
		}
		index := -1
		for i, asset := range uploaded {
			if strings.TrimSuffix(asset.Filename, ".jpg") == entry.Title {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("photo %q is not an uploaded fixture", entry.Title)
		}
		want, err := fixture.JPEG(uploaded[index].Photo, index)
		if err != nil {
			return err
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, entry.DownloadURL, nil)
		request.Header.Set("Range", "bytes=0-1")
		viewer.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), want) {
			return fmt.Errorf("download of %q returned HTTP %d with %d bytes, want %d uploaded bytes", entry.Title, recorder.Code, recorder.Body.Len(), len(want))
		}
		disposition, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != uploaded[index].Filename {
			return fmt.Errorf("download of %q is not an attachment named after the source file", entry.Title)
		}
		if recorder.Header().Get("Cache-Control") != "private, no-store" || recorder.Header().Get("Content-Range") != "" {
			return fmt.Errorf("download of %q used shared caching or honored a range", entry.Title)
		}
		if recorder.Header().Get("Content-Length") != fmt.Sprint(len(want)) {
			return fmt.Errorf("download of %q did not announce its length", entry.Title)
		}
		if err := deniedMedia(ctx, other, entry.DownloadURL, http.StatusNotFound); err != nil {
			return fmt.Errorf("download URL crossed Person identity: %w", err)
		}
	}
	return nil
}

func publishingMediaHandler(module *publishing.Module, delivery *media.Module, actor identity.Person) *echo.Echo {
	handler := echo.New()
	handler.HTTPErrorHandler = errcodes.NewHandler().Handle
	requirePerson := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("identity.person_id", actor.ID)
			return next(c)
		}
	}
	requireCurator := func(next echo.HandlerFunc) echo.HandlerFunc {
		return requirePerson(func(c *echo.Context) error {
			if !actor.IsCurator {
				return identity.ErrAccessDenied
			}
			return next(c)
		})
	}
	media.RegisterViewerRoutes(handler, delivery, module.AuthorizeViewerEntry, requirePerson, requireCurator)
	return handler
}

func verifyViewer(ctx context.Context, module *publishing.Module, handler http.Handler, actorID, previewID, albumID string, photos int, coverID string) (publishing.ViewerAlbum, error) {
	album, err := module.ViewAlbum(ctx, actorID, previewID, albumID)
	if err != nil {
		return album, err
	}
	if album.PhotoCount != photos || album.VideoCount != 0 {
		return album, fmt.Errorf("scoped Album counts differ: %d photos, %d videos", album.PhotoCount, album.VideoCount)
	}
	if coverID == "" {
		if album.CoverURL != "" {
			return album, fmt.Errorf("denied configured covers selected an arbitrary photo")
		}
	} else {
		if !strings.Contains(album.CoverURL, "/entries/"+coverID+"/thumbnail?") {
			return album, fmt.Errorf("album cover did not advance to its earliest authorized configured cover")
		}
		if err := checkMediaEndpoint(ctx, handler, album.CoverURL); err != nil {
			return album, err
		}
		if !strings.Contains(album.CoverPreviewURL, "/entries/"+coverID+"/preview?") {
			return album, fmt.Errorf("album header cover did not use the larger preview variant")
		}
		if err := checkMediaEndpoint(ctx, handler, album.CoverPreviewURL); err != nil {
			return album, err
		}
	}
	page, err := module.ViewEntries(ctx, actorID, previewID, albumID, "IMAGE", "")
	if err != nil {
		return album, err
	}
	if len(page.Entries) != photos || page.NextCursor != "" {
		return album, fmt.Errorf("photo gallery differs from scoped Album count")
	}
	for _, entry := range page.Entries {
		if err := checkMediaEndpoint(ctx, handler, entry.ThumbnailURL); err != nil {
			return album, err
		}
		if err := checkMediaEndpoint(ctx, handler, entry.PreviewURL); err != nil {
			return album, err
		}
	}
	videos, err := module.ViewEntries(ctx, actorID, previewID, albumID, "VIDEO", "")
	if err != nil {
		return album, err
	}
	if len(videos.Entries) != 0 || videos.NextCursor != "" {
		return album, fmt.Errorf("photo-only fixture returned video entries")
	}
	return album, nil
}

func viewerMediaURL(personID string, entry publishing.Entry) string {
	path, _ := url.Parse(entry.ThumbnailURL)
	return "/api/media/viewer/" + personID + "/entries/" + entry.ID + "/thumbnail?" + path.RawQuery
}

func deniedMedia(ctx context.Context, handler http.Handler, path string, status int) error {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(ctx, method, path, nil)
		request.Header.Set("If-None-Match", "*")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != status || strings.HasPrefix(recorder.Header().Get("Content-Type"), "image/") {
			return fmt.Errorf("denied %s returned HTTP %d, want %d", method, recorder.Code, status)
		}
	}
	return nil
}

func isNotFound(err error) bool {
	var code *errcodes.Error
	return errors.As(err, &code) && code.HTTPCode == http.StatusNotFound
}
