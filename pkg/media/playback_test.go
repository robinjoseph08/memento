package media_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// fakeStream answers like Immich's playback endpoint over a small fixed body.
func fakeStream(content string, seen *[]immich.PlaybackRequest) func(context.Context, immich.PlaybackRequest) (immich.Playback, error) {
	return func(_ context.Context, request immich.PlaybackRequest) (immich.Playback, error) {
		*seen = append(*seen, request)
		body := content
		result := immich.Playback{StatusCode: http.StatusOK, ContentType: "video/mp4", ContentLength: int64(len(content)), AcceptRanges: true}
		switch request.Range {
		case "":
		case "bytes=4-7":
			body = content[4:8]
			result.StatusCode, result.ContentLength, result.ContentRange = http.StatusPartialContent, 4, fmt.Sprintf("bytes 4-7/%d", len(content))
		case "bytes=12-":
			body = content[12:]
			result.StatusCode, result.ContentLength, result.ContentRange = http.StatusPartialContent, int64(len(content)-12), fmt.Sprintf("bytes 12-%d/%d", len(content)-1, len(content))
		default:
			// Immich explains an unsatisfiable range with a short text body.
			body = "Range Not Satisfiable"
			result.StatusCode, result.ContentLength, result.ContentRange = http.StatusRequestedRangeNotSatisfiable, int64(len(body)), fmt.Sprintf("bytes */%d", len(content))
		}
		if request.Head {
			body = ""
		}
		result.Body = io.NopCloser(strings.NewReader(body))
		return result, nil
	}
}

func TestViewerPlaybackProxiesOneRangeWithMementoValidators(t *testing.T) {
	t.Parallel()
	upstream := &source{asset: immich.Asset{ID: "clip", Checksum: "Yg==", Filename: "clip.mp4", Kind: "VIDEO", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}}
	var seen []immich.PlaybackRequest
	upstream.playback = fakeStream("0123456789abcdef", &seen)
	db := testdb.New(t)
	imports := publishing.New(db, upstream, func(context.Context, bun.Tx, string) error { return nil })
	album, err := imports.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, imports.ExecuteImport(t.Context(), album.ID))
	album, err = imports.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	video := album.Moments[0].Entries[0]
	version := strings.Split(video.ThumbnailURL, "?v=")[1]
	upstream.asset = immich.Asset{ID: "still", Checksum: "YQ==", Filename: "still.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	photoAlbum, err := imports.StartImport(t.Context(), "photos")
	require.NoError(t, err)
	require.NoError(t, imports.ExecuteImport(t.Context(), photoAlbum.ID))
	photoAlbum, err = imports.GetAlbum(t.Context(), photoAlbum.ID)
	require.NoError(t, err)
	photo := photoAlbum.Moments[0].Entries[0]
	upstream.asset = immich.Asset{ID: "clip", Checksum: "Yg==", Filename: "clip.mp4", Kind: "VIDEO", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}

	actor := "alex"
	allowed := true
	var selected string
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("identity.person_id", actor); return next(c) }
	}
	curator := func(next echo.HandlerFunc) echo.HandlerFunc {
		return person(func(c *echo.Context) error {
			if actor != "curator" {
				return echo.ErrForbidden
			}
			return next(c)
		})
	}
	media.RegisterViewerRoutes(e, media.New(db, upstream), func(_ context.Context, actorID, previewID, entryID string) error {
		selected = previewID
		if !allowed || (entryID != video.ID && entryID != photo.ID) {
			return errcodes.NotFound("Video")
		}
		return nil
	}, person, curator)
	do := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		e.ServeHTTP(r, req)
		return r
	}
	url := "/api/media/viewer/alex/entries/" + video.ID + "/playback?v=" + version
	tag := "\"" + version + "\""

	full := do(http.MethodGet, url, nil)
	require.Equal(t, http.StatusOK, full.Code)
	require.Equal(t, "0123456789abcdef", full.Body.String())
	require.Equal(t, "video/mp4", full.Header().Get("Content-Type"))
	require.Equal(t, "16", full.Header().Get("Content-Length"))
	require.Equal(t, "bytes", full.Header().Get("Accept-Ranges"))
	require.Equal(t, tag, full.Header().Get("ETag"))
	require.Equal(t, "private, max-age=31536000, immutable", full.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", full.Header().Get("X-Content-Type-Options"))
	require.Empty(t, full.Header().Get("Content-Range"))
	require.Equal(t, "playback:clip", upstream.requested)

	partial := do(http.MethodGet, url, map[string]string{"Range": "bytes=4-7"})
	require.Equal(t, http.StatusPartialContent, partial.Code)
	require.Equal(t, "4567", partial.Body.String())
	require.Equal(t, "bytes 4-7/16", partial.Header().Get("Content-Range"))
	require.Equal(t, "4", partial.Header().Get("Content-Length"))
	require.Equal(t, tag, partial.Header().Get("ETag"))

	seeking := do(http.MethodGet, url, map[string]string{"Range": "bytes=12-", "If-Range": tag})
	require.Equal(t, http.StatusPartialContent, seeking.Code, "a matching If-Range keeps the seek")
	require.Equal(t, "cdef", seeking.Body.String())

	stale := do(http.MethodGet, url, map[string]string{"Range": "bytes=4-7", "If-Range": "\"other\""})
	require.Equal(t, http.StatusOK, stale.Code, "a foreign validator asks for the whole video")
	require.Equal(t, "0123456789abcdef", stale.Body.String())
	require.Empty(t, seen[len(seen)-1].Range, "Immich never sees a range it must not honor")

	for _, header := range []string{"bytes=abc", "bytes=0-1,4-7", "items=0-1", "bytes=", "bytes=1-0-"} {
		whole := do(http.MethodGet, url, map[string]string{"Range": header})
		require.Equal(t, http.StatusOK, whole.Code, header)
		require.Empty(t, seen[len(seen)-1].Range, header)
	}

	unsatisfiable := do(http.MethodGet, url, map[string]string{"Range": "bytes=99-"})
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, unsatisfiable.Code)
	require.Equal(t, "bytes */16", unsatisfiable.Header().Get("Content-Range"))
	require.Empty(t, unsatisfiable.Body.String())
	require.Empty(t, unsatisfiable.Header().Get("Content-Length"), "no body means no declared length")

	upstream.requested = ""
	cached := do(http.MethodGet, url, map[string]string{"If-None-Match": "W/" + tag, "Range": "bytes=4-7"})
	require.Equal(t, http.StatusNotModified, cached.Code)
	require.Equal(t, tag, cached.Header().Get("ETag"))
	require.Empty(t, upstream.requested, "a validated cache never reaches Immich")

	before := len(seen)
	head := do(http.MethodHead, url, map[string]string{"Range": "bytes=4-7"})
	require.Equal(t, http.StatusPartialContent, head.Code)
	require.Empty(t, head.Body.String())
	require.Equal(t, "video/mp4", head.Header().Get("Content-Type"))
	require.Equal(t, "bytes 4-7/16", head.Header().Get("Content-Range"))
	require.Len(t, seen, before+1)
	require.True(t, seen[before].Head)

	upstream.requested = ""
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/alex/entries/"+photo.ID+"/playback?v="+strings.Split(photo.ThumbnailURL, "?v=")[1], nil).Code, "photos have no playback")
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/sam/entries/"+video.ID+"/playback?v="+version, nil).Code, "another Person's URL is never served")
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/alex/entries/"+video.ID+"/playback?v=wrong", nil).Code)
	allowed = false
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, url, map[string]string{"If-None-Match": tag}).Code, "authorization runs before the conditional answer")
	allowed = true
	require.Empty(t, upstream.requested)
	require.Equal(t, http.StatusForbidden, do(http.MethodGet, "/api/media/preview/sam/entries/"+video.ID+"/playback?v="+version, nil).Code)
	actor = "curator"
	preview := do(http.MethodGet, "/api/media/preview/sam/entries/"+video.ID+"/playback?v="+version, map[string]string{"Range": "bytes=4-7"})
	require.Equal(t, http.StatusPartialContent, preview.Code, "preview plays the same stream")
	require.Equal(t, "sam", selected)
	require.Equal(t, "private, max-age=31536000, immutable", preview.Header().Get("Cache-Control"))
	actor = "alex"

	upstream.playback = func(context.Context, immich.PlaybackRequest) (immich.Playback, error) {
		return immich.Playback{}, &errcodes.Error{HTTPCode: 403, Code: "immich_permission_denied", Message: "Enable asset.view on the Immich API key."}
	}
	failed := do(http.MethodGet, url, nil)
	require.Equal(t, http.StatusBadGateway, failed.Code)
	require.Contains(t, failed.Body.String(), "Media is unavailable. Try again later.")
	require.NotContains(t, failed.Body.String(), "Immich")
	require.NotContains(t, failed.Body.String(), "asset.view")
	upstream.playback = fakeStream("0123456789abcdef", &seen)
	upstream.asset.UpdatedAt = "2026-07-06T00:00:00Z"
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, url, nil).Code, "a changed source never serves new bytes under an old URL")
	upstream.asset.UpdatedAt = "2026-07-05T00:00:00Z"

	// Cancelling the browser request stops the copy instead of draining Immich.
	released := make(chan struct{})
	upstream.playback = func(ctx context.Context, _ immich.PlaybackRequest) (immich.Playback, error) {
		return immich.Playback{StatusCode: http.StatusOK, ContentType: "video/mp4", ContentLength: -1, Body: &blockingBody{done: ctx.Done(), err: ctx.Err, released: released}}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRecorder()
		e.ServeHTTP(r, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
	}()
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("stream never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled playback kept streaming")
	}
}

// blockingBody sends one byte, then waits for the request to be cancelled.
type blockingBody struct {
	done     <-chan struct{}
	err      func() error
	released chan struct{}
	sent     bool
	closed   bool
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		p[0] = 'x'
		close(b.released)
		return 1, nil
	}
	<-b.done
	return 0, b.err()
}

func (b *blockingBody) Close() error {
	b.closed = true
	return nil
}
