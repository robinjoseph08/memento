package media

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
)

func (h *viewerHandlers) entryPlayback(c *echo.Context) error   { return h.playback(c, false) }
func (h *viewerHandlers) previewPlayback(c *echo.Context) error { return h.playback(c, true) }

// singleByteRange is the only range form forwarded upstream. Anything else,
// including several ranges, is answered with the whole video, which HTTP
// allows a server to do with any Range header.
var singleByteRange = regexp.MustCompile(`^bytes=(\d+-\d*|-\d+)$`)

// playback proxies Immich's video stream for an authorized Album Entry. One
// byte range passes through with Immich's partial response intact.
func (h *viewerHandlers) playback(c *echo.Context, preview bool) error {
	actorID, _ := c.Get("identity.person_id").(string)
	selected := ""
	if actorID == "" {
		return errcodes.NotFound("Video")
	}
	if preview {
		selected = c.Param("personID")
	} else if c.Param("personID") != actorID {
		return errcodes.NotFound("Video")
	}
	if err := h.authorize(c.Request().Context(), actorID, selected, c.Param("id")); err != nil {
		return err
	}
	return streamPlayback(c, h.module, c.Param("id"))
}

// entryPlayback streams a video to a Curator reviewing it, with the same
// range handling as the viewer routes.
func (h *handlers) entryPlayback(c *echo.Context) error {
	return streamPlayback(c, h.module, c.Param("id"))
}

// streamPlayback answers an already-authorized playback request for one Album
// Entry at the version named in the query.
func streamPlayback(c *echo.Context, m *Module, id string) error {
	ctx := c.Request().Context()
	version := c.QueryParam("v")
	item, err := m.EntryPlayback(ctx, id, version)
	if err != nil {
		return err
	}
	// The stream is Immich's playback copy, which Immich can transcode again
	// without the content version changing, so browsers never store it. A
	// browser caches only the start of a long video anyway.
	header := c.Response().Header()
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Accept-Ranges", "bytes")
	// With no validator of its own, Memento can never match an If-Range, so
	// a client holding bytes from an earlier stream gets the whole video
	// rather than a range it would splice onto them.
	byteRange := strings.TrimSpace(c.Request().Header.Get("Range"))
	if !singleByteRange.MatchString(byteRange) || c.Request().Header.Get("If-Range") != "" {
		byteRange = ""
	}
	playback, err := m.viewerPlayback(ctx, item, immich.PlaybackRequest{Range: byteRange, Head: c.Request().Method == http.MethodHead})
	if err != nil {
		return err
	}
	defer func() { _ = playback.Body.Close() }()
	if playback.ContentRange != "" {
		header.Set("Content-Range", playback.ContentRange)
	}
	if playback.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// Immich's explanation body is not forwarded, so no length is declared.
		return c.NoContent(playback.StatusCode)
	}
	if playback.ContentLength >= 0 {
		header.Set("Content-Length", strconv.FormatInt(playback.ContentLength, 10))
	}
	if c.Request().Method == http.MethodHead {
		header.Set("Content-Type", playback.ContentType)
		return c.NoContent(playback.StatusCode)
	}
	return errorstack.CaptureContext(ctx, c.Stream(playback.StatusCode, playback.ContentType, playback.Body))
}
