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

// playback proxies Immich's video stream for an authorized Album Entry. Memento
// owns the validators: the content version is the ETag, so a matching
// If-None-Match answers 304 and a mismatched If-Range asks for the whole file.
// One byte range passes through with Immich's partial response intact.
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
	return streamPlayback(c, h.module)
}

// entryPlayback streams a video to a Curator reviewing it, with the same
// range and validator semantics as the viewer routes.
func (h *handlers) entryPlayback(c *echo.Context) error { return streamPlayback(c, h.module) }

// streamPlayback answers an already-authorized playback request for the Album
// Entry named in the route.
func streamPlayback(c *echo.Context, m *Module) error {
	ctx := c.Request().Context()
	version := c.QueryParam("v")
	item, err := m.EntryPlayback(ctx, c.Param("id"), version)
	if err != nil {
		return err
	}
	tag := "\"" + version + "\""
	header := c.Response().Header()
	header.Set("Cache-Control", "private, max-age=31536000, immutable")
	header.Set("ETag", tag)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Accept-Ranges", "bytes")
	if etagMatches(c.Request().Header.Get("If-None-Match"), tag) {
		return c.NoContent(http.StatusNotModified)
	}
	byteRange := strings.TrimSpace(c.Request().Header.Get("Range"))
	if !singleByteRange.MatchString(byteRange) || !ifRangeAllows(c.Request().Header.Get("If-Range"), tag) {
		byteRange = ""
	}
	playback, err := m.viewerPlayback(ctx, item.SourceID, version, immich.PlaybackRequest{Range: byteRange, Head: c.Request().Method == http.MethodHead})
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

// etagMatches applies If-None-Match with weak comparison, as browsers revalidate.
func etagMatches(header, tag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == tag {
			return true
		}
	}
	return false
}

// ifRangeAllows says whether a Range may be honored. Absent means yes; a
// strong match of Memento's tag means yes; a date or another tag means the
// browser holds different bytes and must receive the whole video.
func ifRangeAllows(header, tag string) bool {
	header = strings.TrimSpace(header)
	return header == "" || header == tag
}
