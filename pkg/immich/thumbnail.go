package immich

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/robinjoseph08/memento/pkg/errcodes"
)

// Thumbnail opens generated media only. The caller must close Body.
func (c *Client) Thumbnail(ctx context.Context, id string) (Thumbnail, error) {
	if id == "" {
		return Thumbnail{}, errcodes.ValidationError("An Immich asset ID is required.")
	}
	response, err := c.request(ctx, http.MethodGet, "/api/assets/"+escapeID(id)+"/thumbnail?size=thumbnail", nil, "asset.view")
	if err != nil {
		return Thumbnail{}, err
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err == nil {
		switch contentType {
		case "image/jpeg", "image/png", "image/webp", "image/avif", "image/gif", "application/octet-stream":
			return Thumbnail{Body: &thumbnailBody{sanitize: func(err error) error { return transportError(ctx, err) }, body: response.Body}, ContentType: contentType}, nil
		}
	}
	_ = response.Body.Close()
	return Thumbnail{}, unreadable("thumbnail content type")
}

// thumbnailBody sanitizes streaming failures just as JSON reads do.
type thumbnailBody struct {
	sanitize func(error) error
	body     io.ReadCloser
	terminal error
}

func (b *thumbnailBody) Read(p []byte) (int, error) {
	if b.terminal != nil {
		return 0, b.terminal
	}
	n, err := b.body.Read(p)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			err = b.sanitize(err)
		}
		b.terminal = err
	}
	return n, err
}

func (b *thumbnailBody) Close() error {
	if err := b.body.Close(); err != nil {
		return b.sanitize(err)
	}
	return nil
}
