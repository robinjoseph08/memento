package immich

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
)

// PlaybackRequest carries the only request facts Memento forwards: one byte
// range and whether the caller wants headers alone. Validators stay in Memento
// because its content version, not Immich's ETag, names the cached bytes.
type PlaybackRequest struct {
	Range string
	Head  bool
}

// Playback is Immich's playback response. StatusCode is 200, 206, or 416; the
// caller forwards ContentRange and ContentLength as they arrived and closes
// Body. AcceptRanges reports that Immich advertised byte ranges.
type Playback struct {
	Body          io.ReadCloser
	StatusCode    int
	ContentType   string
	ContentLength int64
	ContentRange  string
	AcceptRanges  bool
}

// Playback opens the playback stream Immich prepares for a video, which is the
// original or its transcode. Nothing is buffered: the body streams through
// the caller's context.
func (c *Client) Playback(ctx context.Context, id string, request PlaybackRequest) (Playback, error) {
	if id == "" {
		return Playback{}, errcodes.ValidationError("An Immich asset ID is required.")
	}
	method := http.MethodGet
	if request.Head {
		method = http.MethodHead
	}
	headers := map[string]string{}
	if request.Range != "" {
		headers["Range"] = request.Range
	}
	response, err := c.sendStream(ctx, method, "/api/assets/"+escapeID(id)+"/video/playback", headers, "asset.view", //nolint:bodyclose // The caller closes the wrapped body.
		http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable)
	if err != nil {
		return Playback{}, err
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(contentType, "video/") {
		contentType = "application/octet-stream"
	}
	return Playback{
		Body:          &thumbnailBody{sanitize: func(err error) error { return transportError(ctx, err) }, body: response.Body},
		StatusCode:    response.StatusCode,
		ContentType:   contentType,
		ContentLength: response.ContentLength,
		ContentRange:  response.Header.Get("Content-Range"),
		AcceptRanges:  strings.EqualFold(response.Header.Get("Accept-Ranges"), "bytes"),
	}, nil
}

// ChapterProbe reads embedded chapters from an authenticated HTTP URL. The
// bundled ffprobe is the production implementation.
type ChapterProbe interface {
	Chapters(ctx context.Context, url string, headers map[string]string) ([]ffprobe.Chapter, error)
}

// Chapters probes the uploaded original through HTTP ranges. The key travels
// only into the probe's request headers; it never reaches the caller.
func (c *Client) Chapters(ctx context.Context, id string) ([]ffprobe.Chapter, error) {
	if id == "" {
		return nil, errcodes.ValidationError("An Immich asset ID is required.")
	}
	if c.Probe == nil {
		return nil, errorstack.Capture(errors.New("chapter probe is not configured on this Immich client"))
	}
	if err := c.validBase(); err != nil {
		return nil, err
	}
	return c.Probe.Chapters(ctx, c.baseURL+"/api/assets/"+escapeID(id)+"/original", map[string]string{"X-Api-Key": c.apiKey})
}

func (c *Client) validBase() error {
	base, err := url.Parse(c.baseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || strings.Contains(c.baseURL, "#") || base.Opaque != "" {
		return errcodes.ValidationError("Check immich_url points to the Immich server, without credentials, a query, or a fragment.")
	}
	return nil
}
