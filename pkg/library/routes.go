package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
)

type Authorizer interface {
	AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error)
}

type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.SessionActor, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Recipient Session is required.")
	}
	actor, err := h.authorizer.AuthorizeSession(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Recipient Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return setup.SessionActor{}, errcodes.Forbidden("This action requires a valid CSRF token")
	case err != nil:
		return setup.SessionActor{}, err
	default:
		return actor, nil
	}
}

func libraryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidCursor):
		return errcodes.ValidationError("Use a valid library cursor and a limit from 1 to 100.")
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Content")
	default:
		return err
	}
}

func (h *Handler) Photos(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.Photos(c.Request().Context(), actor, c.QueryParam("limit"), c.QueryParam("cursor"), false)
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Favorites(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.Photos(c.Request().Context(), actor, c.QueryParam("limit"), c.QueryParam("cursor"), true)
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Events(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.Events(c.Request().Context(), actor, c.QueryParam("limit"), c.QueryParam("cursor"))
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Event(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return errcodes.NotFound("Content")
	}
	response, err := h.service.Event(c.Request().Context(), actor, id, c.QueryParam("limit"), c.QueryParam("cursor"))
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) NewForYou(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.NewForYou(c.Request().Context(), actor)
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) MarkSeen(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("publication_id"))
	if err != nil || id == uuid.Nil {
		return errcodes.NotFound("Content")
	}
	if mapped := libraryError(h.service.MarkSeen(c.Request().Context(), actor, id)); mapped != nil {
		return mapped
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Thumbnail(c echo.Context) error {
	return h.streamRepresentation(c, representationThumbnail)
}

func (h *Handler) Preview(c echo.Context) error {
	return h.streamRepresentation(c, representationPreview)
}

func (h *Handler) Video(c echo.Context) error {
	return h.streamRepresentation(c, representationVideo)
}

func (h *Handler) Original(c echo.Context) error {
	return h.streamRepresentation(c, representationOriginal)
}

func (h *Handler) streamRepresentation(c echo.Context, kind representation) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return errcodes.NotFound("Content")
	}
	request := immichMediaRequest(c.Request().Header)
	var response immich.MediaResponse
	switch kind {
	case representationThumbnail:
		response, err = h.service.Thumbnail(c.Request().Context(), actor, id, request)
	case representationPreview:
		response, err = h.service.Preview(c.Request().Context(), actor, id, request)
	case representationVideo:
		response, err = h.service.Video(c.Request().Context(), actor, id, request)
	case representationOriginal:
		response, err = h.service.Original(c.Request().Context(), actor, id, request)
	default:
		err = ErrNotFound
	}
	if mapped := libraryError(err); mapped != nil {
		return mapped
	}
	if response.Body != nil {
		defer response.Body.Close()
	}
	headers := c.Response().Header()
	headers.Set(echo.HeaderCacheControl, "private, no-cache")
	if response.ContentType != "" {
		headers.Set(echo.HeaderContentType, response.ContentType)
	}
	if response.ContentLength >= 0 {
		headers.Set(echo.HeaderContentLength, strconv.FormatInt(response.ContentLength, 10))
	}
	for name, value := range map[string]string{
		"Content-Range": response.ContentRange, "Accept-Ranges": response.AcceptRanges,
		"ETag": response.ETag, "Last-Modified": response.LastModified,
	} {
		if value != "" {
			headers.Set(name, value)
		}
	}
	if kind == representationOriginal && (response.StatusCode == 0 || response.StatusCode == http.StatusOK || response.StatusCode == http.StatusPartialContent) {
		headers.Set(echo.HeaderContentDisposition, originalDisposition(id, response.ContentType))
	}
	status := response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	c.Response().WriteHeader(status)
	if response.Body == nil || status == http.StatusNotModified || status == http.StatusRequestedRangeNotSatisfiable {
		return nil
	}
	buffer := make([]byte, 32<<10)
	_, err = io.CopyBuffer(c.Response(), response.Body, buffer)
	return err
}

func immichMediaRequest(header http.Header) immich.MediaRequest {
	return immich.MediaRequest{
		Range: header.Get("Range"), IfRange: header.Get("If-Range"),
		IfNoneMatch: header.Get("If-None-Match"), IfModifiedSince: header.Get("If-Modified-Since"),
	}
}

func originalDisposition(id uuid.UUID, contentType string) string {
	extension := ""
	switch contentType {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	case "image/webp":
		extension = ".webp"
	case "video/mp4":
		extension = ".mp4"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("memento-%s%s", id, extension)})
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "private, no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	me := e.Group("/api/me", noStore)
	photos := me.GET("/photos", handler.Photos)
	photos.Name = "policy:recipient_content"
	favorites := me.GET("/favorites", handler.Favorites)
	favorites.Name = "policy:recipient_content"
	events := me.GET("/events", handler.Events)
	events.Name = "policy:recipient_content"
	event := me.GET("/events/:id", handler.Event)
	event.Name = "policy:recipient_content"
	newForYou := me.GET("/new-for-you", handler.NewForYou)
	newForYou.Name = "policy:recipient_content"
	seen := me.POST("/new-for-you/:publication_id/seen", handler.MarkSeen)
	seen.Name = "policy:recipient_content_csrf"
	thumbnail := me.GET("/media/:id/thumbnail", handler.Thumbnail)
	thumbnail.Name = "policy:recipient_content"
	preview := me.GET("/media/:id/preview", handler.Preview)
	preview.Name = "policy:recipient_content"
	video := me.GET("/media/:id/video", handler.Video)
	video.Name = "policy:recipient_content"
	original := me.GET("/media/:id/original", handler.Original)
	original.Name = "policy:recipient_content"
}
