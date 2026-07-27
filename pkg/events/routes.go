package events

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	curatorReadPolicy     = "policy:curator"
	curatorMutationPolicy = "policy:curator_csrf"
)

// Authorizer authenticates the sole Curator without exposing Session internals.
type Authorizer interface {
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
	ContextWithRequestMetadata(ctx context.Context, request *http.Request) context.Context
}

// Handler exposes only private Curator drafting routes.
type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	return h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request())
}

func (h *Handler) CreateEvent(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request CreateEventRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	event, err := h.service.CreateEvent(h.requestContext(c), actor, request)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, event)
}

func (h *Handler) GetEvent(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	event, err := h.service.GetEvent(c.Request().Context(), id)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, event)
}

func (h *Handler) SourceMedia(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Source album")
	if err != nil {
		return err
	}
	response, err := h.service.SourceMedia(c.Request().Context(), id)
	if mapped := draftError(err, "Source album"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CreateLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request CreateLooseItemRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	item, created, err := h.service.CreateLooseItem(h.requestContext(c), actor, request)
	if mapped := draftError(err, "Loose item"); mapped != nil {
		return mapped
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, item)
}

func (h *Handler) GetLooseItem(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Loose item")
	if err != nil {
		return err
	}
	item, err := h.service.GetLooseItem(c.Request().Context(), id)
	if mapped := draftError(err, "Loose item"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, item)
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	}
	actor, err := h.authorizer.AuthorizeCurator(
		c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation,
	)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return setup.CuratorSession{}, errcodes.Forbidden("Changing drafts without a valid CSRF token")
	case errors.Is(err, setup.ErrNotCurator):
		return setup.CuratorSession{}, errcodes.NotFound("Page")
	case err != nil:
		return setup.CuratorSession{}, err
	default:
		return actor, nil
	}
}

func draftError(err error, resource string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound(resource)
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("Draft fields must be valid and within their limits.")
	case errors.Is(err, ErrSourceUnavailable):
		return errcodes.Conflict("Every Source album must be available and not ignored.")
	case errors.Is(err, ErrMediaUnavailable) && resource == "Loose item":
		return errcodes.Conflict("The selected Media item is unavailable.")
	case errors.Is(err, ErrMediaUnavailable):
		return errcodes.Conflict("Every selected Media item must belong to a selected Source album and remain available.")
	default:
		return err
	}
}

func routeID(value, resource string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound(resource)
	}
	return id, nil
}

// RegisterRoutes registers private, no-store Curator draft routes.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	events := e.Group("/api/events", noStore)
	createEvent := events.POST("", handler.CreateEvent)
	createEvent.Name = curatorMutationPolicy
	getEvent := events.GET("/:id", handler.GetEvent)
	getEvent.Name = curatorReadPolicy

	looseItems := e.Group("/api/loose-items", noStore)
	createLoose := looseItems.POST("", handler.CreateLooseItem)
	createLoose.Name = curatorMutationPolicy
	getLoose := looseItems.GET("/:id", handler.GetLooseItem)
	getLoose.Name = curatorReadPolicy

	sourceMedia := e.GET("/api/sources/:id/media-items", handler.SourceMedia, noStore)
	sourceMedia.Name = curatorReadPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
