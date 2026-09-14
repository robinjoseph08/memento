package media

import (
	"context"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
)

// AuthorizeEntry applies Publishing's evaluator before any media response.
type AuthorizeEntry func(context.Context, string, string, string) error

type viewerHandlers struct {
	module    *Module
	authorize AuthorizeEntry
}

func (h *viewerHandlers) entryThumbnail(c *echo.Context) error   { return h.image(c, false, false) }
func (h *viewerHandlers) entryPreview(c *echo.Context) error     { return h.image(c, false, true) }
func (h *viewerHandlers) previewThumbnail(c *echo.Context) error { return h.image(c, true, false) }
func (h *viewerHandlers) previewPreview(c *echo.Context) error   { return h.image(c, true, true) }

// image serves one generated variant: the small thumbnail or the large
// preview. preview selects a Curator's selected-Person context.
func (h *viewerHandlers) image(c *echo.Context, preview, large bool) error {
	actorID, _ := c.Get("identity.person_id").(string)
	selected := ""
	if actorID == "" {
		return errcodes.NotFound("Thumbnail")
	}
	if preview {
		selected = c.Param("personID")
	} else if c.Param("personID") != actorID {
		return errcodes.NotFound("Thumbnail")
	}
	if err := h.authorize(c.Request().Context(), actorID, selected, c.Param("id")); err != nil {
		return err
	}
	version := c.QueryParam("v")
	sourceID, err := h.module.EntryThumbnail(c.Request().Context(), c.Param("id"), version)
	if err != nil {
		return err
	}
	return serveVersioned(c, version, func() (immich.Thumbnail, error) {
		return h.module.viewerImage(c.Request().Context(), sourceID, version, large)
	})
}

// entryOriginal streams a photo's or video's uploaded file as an attachment.
// Downloads are never cached or served in ranges; a preview context has no
// such route.
func (h *viewerHandlers) entryOriginal(c *echo.Context) error {
	actorID, _ := c.Get("identity.person_id").(string)
	if actorID == "" || c.Param("personID") != actorID {
		return errcodes.NotFound("Download")
	}
	if err := h.authorize(c.Request().Context(), actorID, "", c.Param("id")); err != nil {
		return err
	}
	version := c.QueryParam("v")
	item, err := h.module.EntryOriginal(c.Request().Context(), c.Param("id"), version)
	if err != nil {
		return err
	}
	header := c.Response().Header()
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Disposition", attachment(item.Filename))
	// HEAD confirms the photo is still downloadable without asking Immich to
	// start sending the file.
	if c.Request().Method == http.MethodHead {
		if err := h.module.checkOriginal(c.Request().Context(), item.SourceID, version); err != nil {
			return err
		}
		return c.NoContent(http.StatusOK)
	}
	original, err := h.module.viewerOriginal(c.Request().Context(), item.SourceID, version)
	if err != nil {
		return err
	}
	defer func() { _ = original.Body.Close() }()
	if original.Length >= 0 {
		header.Set("Content-Length", strconv.FormatInt(original.Length, 10))
	}
	return errorstack.CaptureContext(c.Request().Context(), c.Stream(http.StatusOK, original.ContentType, original.Body))
}

// attachment names the download after the imported filename, keeping only
// its base name so an odd source path cannot steer the browser.
func attachment(filename string) string {
	name := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if name == "." || name == ".." || name == "/" || name == "" {
		name = "download"
	}
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": name}); value != "" {
		return value
	}
	return "attachment"
}

type handlers struct{ module *Module }

func (h *handlers) sourceCover(c *echo.Context) error {
	image, err := h.module.SourceCover(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return serveImage(c, image, "private, no-store", "")
}

// sourceAssetThumbnail previews an Immich asset a synchronization review
// lists before it belongs to any Album. It is Curator-only and never cached.
func (h *handlers) sourceAssetThumbnail(c *echo.Context) error {
	image, err := h.module.SourceAssetThumbnail(c.Request().Context(), c.Param("id"))
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
	actorID, _ := c.Get("identity.person_id").(string)
	curator, _ := c.Get("identity.is_curator").(bool)
	if !curator && (actorID == "" || c.Param("id") != actorID) {
		return &errcodes.Error{HTTPCode: http.StatusForbidden, Code: "access_denied", Message: "Only Curators can view other people's avatars."}
	}
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
