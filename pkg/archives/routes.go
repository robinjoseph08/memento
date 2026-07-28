package archives

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
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

func archiveError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidSelection):
		return errcodes.ValidationError("Select one complete authorized Event or 1 to 1000 distinct authorized Media items.")
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Archive")
	case errors.Is(err, ErrUnavailable):
		return errcodes.ServiceUnavailable("Archive downloads are temporarily unavailable.")
	default:
		return err
	}
}

func (h *Handler) Plan(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request PlanRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	response, err := h.service.Plan(c.Request().Context(), actor, request)
	if mapped := archiveError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, response)
}

func (h *Handler) Part(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	number, err := strconv.Atoi(c.Param("part"))
	if err != nil {
		return errcodes.NotFound("Archive")
	}
	stream, err := h.service.StreamPart(c.Request().Context(), actor, c.QueryParam("token"), number)
	if mapped := archiveError(err); mapped != nil {
		return mapped
	}
	defer stream.Body.Close()
	header := c.Response().Header()
	header.Set(echo.HeaderCacheControl, "private, no-store")
	header.Set(echo.HeaderContentType, "application/zip")
	header.Set(echo.HeaderContentDisposition, mime.FormatMediaType("attachment", map[string]string{"filename": stream.Filename}))
	if stream.ContentLength >= 0 {
		header.Set(echo.HeaderContentLength, strconv.FormatInt(stream.ContentLength, 10))
	}
	c.Response().WriteHeader(http.StatusOK)
	buffer := make([]byte, 32<<10)
	_, err = io.CopyBuffer(c.Response(), stream.Body, buffer)
	return err
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "private, no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/me/archives", noStore)
	plan := group.POST("", handler.Plan)
	plan.Name = "policy:recipient_content_csrf"
	// Keep the secret out of the path because request logging records paths but
	// deliberately omits query values.
	part := group.GET("/parts/:part", handler.Part)
	part.Name = "policy:recipient_content"
}
