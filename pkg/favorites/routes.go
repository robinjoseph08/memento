package favorites

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
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

func favoriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFoundMessage("This Media is unavailable.")
	case errors.Is(err, ErrNotCurator):
		return errcodes.Forbidden("Curator authority is required")
	case errors.Is(err, ErrInvalidCursor):
		return errcodes.ValidationError("Use a valid Favorite cursor and a limit from 1 to 100.")
	default:
		return err
	}
}

func parseID(c echo.Context, parameter string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(parameter))
	if err != nil || id == uuid.Nil {
		if parameter == "recipient_id" {
			return uuid.Nil, errcodes.NotFound("Recipient")
		}
		return uuid.Nil, errcodes.NotFoundMessage("This Media is unavailable.")
	}
	return id, nil
}

func (h *Handler) Get(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := parseID(c, "media_id")
	if err != nil {
		return err
	}
	response, err := h.service.Get(c.Request().Context(), actor, id)
	if mapped := favoriteError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Add(c echo.Context) error {
	return h.set(c, true)
}

func (h *Handler) Remove(c echo.Context) error {
	return h.set(c, false)
}

func (h *Handler) set(c echo.Context, favorite bool) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := parseID(c, "media_id")
	if err != nil {
		return err
	}
	response, err := h.service.Set(c.Request().Context(), actor, id, favorite)
	if mapped := favoriteError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CuratorList(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := parseID(c, "recipient_id")
	if err != nil {
		return err
	}
	page := PageRequest{Cursor: c.QueryParam("cursor"), Limit: 50}
	if raw := c.QueryParam("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > 100 {
			return errcodes.ValidationError("Use a valid Favorite cursor and a limit from 1 to 100.")
		}
		page.Limit = limit
	}
	response, err := h.service.CuratorList(c.Request().Context(), actor, id, page)
	if errors.Is(err, ErrNotFound) {
		return errcodes.NotFound("Recipient")
	}
	if mapped := favoriteError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "private, no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/favorites", noStore)
	get := group.GET("/:media_id", handler.Get)
	get.Name = "policy:recipient_content"
	add := group.PUT("/:media_id", handler.Add)
	add.Name = "policy:recipient_content_csrf"
	remove := group.DELETE("/:media_id", handler.Remove)
	remove.Name = "policy:recipient_content_csrf"
	curator := group.GET("/curator/recipients/:recipient_id", handler.CuratorList)
	curator.Name = "policy:curator"
}
