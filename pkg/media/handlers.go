package media

import (
	"mime"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
)

type handlers struct{ module *Module }

func (h *handlers) sourceCover(c *echo.Context) error {
	image, err := h.module.SourceCover(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return serveImage(c, image, "private, no-store", "")
}

func (h *handlers) faceThumbnail(c *echo.Context) error {
	version := c.QueryParam("v")
	sourceID, err := h.module.FaceThumbnail(c.Request().Context(), c.Param("sourceID"), version)
	if err != nil {
		return err
	}
	return serveVersioned(c, version, func() (immich.Thumbnail, error) {
		return h.module.personThumbnail(c.Request().Context(), sourceID)
	})
}

func (h *handlers) personAvatar(c *echo.Context) error {
	version := c.QueryParam("v")
	sourceID, err := h.module.PersonAvatar(c.Request().Context(), c.Param("id"), version)
	if err != nil {
		return err
	}
	return serveVersioned(c, version, func() (immich.Thumbnail, error) {
		return h.module.personThumbnail(c.Request().Context(), sourceID)
	})
}

func (h *handlers) entryThumbnail(c *echo.Context) error {
	version := c.QueryParam("v")
	sourceID, err := h.module.EntryThumbnail(c.Request().Context(), c.Param("id"), version)
	if err != nil {
		return err
	}
	return serveVersioned(c, version, func() (immich.Thumbnail, error) {
		return h.module.generatedThumbnail(c.Request().Context(), sourceID, version)
	})
}

// serveVersioned answers an already-authorized, content-versioned image with
// immutable private caching, skipping the upstream read on a matching ETag.
func serveVersioned(c *echo.Context, version string, open func() (immich.Thumbnail, error)) error {
	tag := "\"" + version + "\""
	const cache = "private, max-age=31536000, immutable"
	for candidate := range strings.SplitSeq(c.Request().Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == tag {
			c.Response().Header().Set("ETag", tag)
			c.Response().Header().Set("Cache-Control", cache)
			return c.NoContent(http.StatusNotModified)
		}
	}
	image, err := open()
	if err != nil {
		return err
	}
	return serveImage(c, image, cache, tag)
}

func serveImage(c *echo.Context, image immich.Thumbnail, cache, tag string) error {
	defer func() { _ = image.Body.Close() }()
	contentType, _, err := mime.ParseMediaType(image.ContentType)
	if err != nil {
		return errcodes.NotFound("Thumbnail")
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/webp", "image/avif", "image/gif":
	default:
		return errcodes.NotFound("Thumbnail")
	}
	c.Response().Header().Set("Cache-Control", cache)
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	if tag != "" {
		c.Response().Header().Set("ETag", tag)
	}
	if c.Request().Method == http.MethodHead {
		c.Response().Header().Set("Content-Type", contentType)
		return c.NoContent(http.StatusOK)
	}
	return errorstack.CaptureContext(c.Request().Context(), c.Stream(http.StatusOK, contentType, image.Body))
}
