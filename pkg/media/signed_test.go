package media_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestSignedURLServesAuthorizedMediaWithoutACookieUntilItExpires(t *testing.T) {
	t.Parallel()
	clip := immich.Asset{ID: "clip", Checksum: "Yg==", Filename: "clip.mp4", Kind: "VIDEO", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	still := immich.Asset{ID: "still", Checksum: "YQ==", Filename: "still.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	upstream := &source{}
	var seen []immich.PlaybackRequest
	upstream.playback = fakeStream("0123456789abcdef", &seen)
	db := testdb.New(t)
	imports := publishing.New(db, upstream, func(context.Context, bun.Tx, string) error { return nil })
	importEntry := func(sourceAlbum string, asset immich.Asset) publishing.Entry {
		upstream.asset = asset
		album, err := imports.StartImport(t.Context(), sourceAlbum)
		require.NoError(t, err)
		require.NoError(t, imports.ExecuteImport(t.Context(), album.ID))
		album, err = imports.GetAlbum(t.Context(), album.ID)
		require.NoError(t, err)
		return album.Moments[0].Entries[0]
	}
	video := importEntry("videos", clip)
	photo := importEntry("photos", still)
	hidden := importEntry("hidden", still)
	version := strings.Split(video.ThumbnailURL, "?v=")[1]

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	actor := "alex"
	allowed := true
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("identity.person_id", actor); return next(c) }
	}
	library := media.New(db, upstream)
	library.Now = func() time.Time { return now }
	media.RegisterSignedRoutes(e, library, func(_ context.Context, actorID, previewID, entryID string) error {
		require.Empty(t, previewID, "signed media never runs in a preview context")
		if !allowed || actorID != "alex" || entryID == hidden.ID {
			return errcodes.NotFound("Thumbnail")
		}
		return nil
	}, "https://memento.example/", person)
	do := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		e.ServeHTTP(r, req)
		return r
	}
	mint := func(entryID, variant string) (int, string) {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/media/signed", strings.NewReader(`{"entry_id":"`+entryID+`","variant":"`+variant+`"}`))
		req.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(r, req)
		var body media.SignedURL
		if r.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(r.Body.Bytes(), &body))
		}
		return r.Code, body.URL
	}

	// Refusals come first so they prove nothing was fetched from Immich.
	upstream.asset = still
	code, _ := mint(hidden.ID, "preview")
	require.Equal(t, http.StatusNotFound, code, "an entry the Person cannot view is refused")
	code, _ = mint(photo.ID, "playback")
	require.Equal(t, http.StatusNotFound, code, "photos have no playback")
	code, _ = mint(video.ID, "preview")
	require.Equal(t, http.StatusNotFound, code, "a video casts as playback")
	code, _ = mint(photo.ID, "original")
	require.Equal(t, http.StatusUnprocessableEntity, code)
	code, _ = mint("not-an-entry", "preview")
	require.Equal(t, http.StatusNotFound, code)

	code, playbackURL := mint(video.ID, "playback")
	require.Equal(t, http.StatusOK, code)
	require.True(t, strings.HasPrefix(playbackURL, "https://memento.example/api/media/signed/playback/"), playbackURL)
	code, previewURL := mint(photo.ID, "preview")
	require.Equal(t, http.StatusOK, code)
	require.True(t, strings.HasPrefix(previewURL, "https://memento.example/api/media/signed/preview/"), previewURL)
	playbackPath := strings.TrimPrefix(playbackURL, "https://memento.example")
	previewPath := strings.TrimPrefix(previewURL, "https://memento.example")
	require.Empty(t, upstream.requested, "minting reads nothing from Immich")

	// The TV has no cookie: nothing sets an identity on these requests.
	actor = ""
	upstream.asset = clip
	tag := "\"" + version + "\""
	full := do(http.MethodGet, playbackPath, nil)
	require.Equal(t, http.StatusOK, full.Code)
	require.Equal(t, "0123456789abcdef", full.Body.String())
	require.Equal(t, "video/mp4", full.Header().Get("Content-Type"))
	require.Equal(t, "16", full.Header().Get("Content-Length"))
	require.Equal(t, "bytes", full.Header().Get("Accept-Ranges"))
	require.Equal(t, tag, full.Header().Get("ETag"))
	require.Equal(t, "private, max-age=31536000, immutable", full.Header().Get("Cache-Control"), "shared proxies never store it")
	require.Equal(t, "nosniff", full.Header().Get("X-Content-Type-Options"))
	partial := do(http.MethodGet, playbackPath, map[string]string{"Range": "bytes=4-7"})
	require.Equal(t, http.StatusPartialContent, partial.Code)
	require.Equal(t, "4567", partial.Body.String())
	require.Equal(t, "bytes 4-7/16", partial.Header().Get("Content-Range"))
	require.Equal(t, "4", partial.Header().Get("Content-Length"))
	head := do(http.MethodHead, playbackPath, map[string]string{"Range": "bytes=12-"})
	require.Equal(t, http.StatusPartialContent, head.Code)
	require.Empty(t, head.Body.String())
	require.Equal(t, "video/mp4", head.Header().Get("Content-Type"))
	require.Equal(t, "bytes 12-15/16", head.Header().Get("Content-Range"))
	unsatisfiable := do(http.MethodGet, playbackPath, map[string]string{"Range": "bytes=99-"})
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, unsatisfiable.Code)
	require.Equal(t, "bytes */16", unsatisfiable.Header().Get("Content-Range"))

	// The key lives with the installation, so a restarted process honors URLs
	// minted before it.
	restarted := echo.New()
	restarted.HTTPErrorHandler = errcodes.NewHandler().Handle
	fresh := media.New(db, upstream)
	fresh.Now = library.Now
	media.RegisterSignedRoutes(restarted, fresh, func(context.Context, string, string, string) error { return nil }, "https://memento.example", person)
	survived := httptest.NewRecorder()
	restarted.ServeHTTP(survived, httptest.NewRequest(http.MethodGet, playbackPath, nil))
	require.Equal(t, http.StatusOK, survived.Code)
	require.Equal(t, "0123456789abcdef", survived.Body.String())

	upstream.asset = still
	image := do(http.MethodGet, previewPath, nil)
	require.Equal(t, http.StatusOK, image.Code)
	require.Equal(t, "large image", image.Body.String())
	require.Equal(t, "image/jpeg", image.Header().Get("Content-Type"))
	require.Equal(t, "preview:still", upstream.requested)
	imageHead := do(http.MethodHead, previewPath, nil)
	require.Equal(t, http.StatusOK, imageHead.Code)
	require.Empty(t, imageHead.Body.String())
	require.Equal(t, "image/jpeg", imageHead.Header().Get("Content-Type"))

	// From here on every request is refused before Immich hears about it.
	upstream.asset = clip
	upstream.requested = ""
	before := len(seen)
	refused := func(path, reason string) {
		t.Helper()
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := do(method, path, map[string]string{"If-None-Match": tag})
			require.Equal(t, http.StatusNotFound, response.Code, reason)
			require.NotContains(t, response.Body.String(), "0123456789abcdef", reason)
		}
		require.Empty(t, upstream.requested, reason)
		require.Len(t, seen, before, reason)
	}
	token, _, _ := strings.Cut(strings.TrimPrefix(playbackPath, "/api/media/signed/playback/"), "?")
	refused("/api/media/signed/preview/"+token+"?v="+version, "a playback token never serves another variant")
	refused("/api/media/signed/original/"+token+"?v="+version, "only playback and preview are signed")
	refused(strings.Replace(playbackPath, video.ID, photo.ID, 1), "a token edited to name another entry")
	refused("/api/media/signed/playback/"+token[:len(token)-1]+"?v="+version, "a truncated signature")
	allowed = false
	refused(playbackPath, "access removed while the token is unexpired")
	allowed = true
	require.Equal(t, http.StatusOK, do(http.MethodGet, playbackPath, nil).Code)
	before = len(seen)
	upstream.requested = ""
	now = now.Add(6*time.Hour - time.Second)
	require.Equal(t, http.StatusOK, do(http.MethodGet, playbackPath, nil).Code, "tokens last six hours")
	before = len(seen)
	upstream.requested = ""
	now = now.Add(time.Second)
	refused(playbackPath, "an expired token")
}
