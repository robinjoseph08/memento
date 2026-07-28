package comments

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

func interactionError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidMute):
		return errcodes.NotFound("Comment")
	case errors.Is(err, ErrInvalidBody):
		return errcodes.ValidationError("Use non-empty Comment text up to 2,000 characters and a moderation reason up to 500 characters.")
	case errors.Is(err, ErrNotCurator):
		return errcodes.Forbidden("Curator authority is required")
	case errors.Is(err, ErrVersionConflict):
		return errcodes.Conflict("This Comment changed in another browser. Reload the current Comment before changing it again.")
	case errors.Is(err, ErrIdempotencyConflict):
		return errcodes.Conflict("This Comment retry key was already used for another request. Reload the thread before posting again.")
	case errors.Is(err, ErrInvalidCursor):
		return errcodes.ValidationError("Use a valid Comment cursor and a limit from 1 to 100.")
	default:
		return err
	}
}

func mediaID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("media_id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Comment")
	}
	return id, nil
}

func commentID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("comment_id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Comment")
	}
	return id, nil
}

func commentVersion(c echo.Context) (int64, error) {
	version, err := strconv.ParseInt(c.Request().Header.Get("If-Match"), 10, 64)
	if err != nil || version < 1 {
		return 0, errcodes.ValidationError("If-Match must contain the current Comment version.")
	}
	return version, nil
}

func commentPage(c echo.Context) (PageRequest, error) {
	page := PageRequest{Cursor: c.QueryParam("cursor"), Limit: 50}
	if raw := c.QueryParam("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return PageRequest{}, errcodes.ValidationError("Use a valid Comment cursor and a limit from 1 to 100.")
		}
		page.Limit = limit
	}
	return page, nil
}

func idempotencyKey(c echo.Context) (uuid.UUID, error) {
	key, err := uuid.Parse(c.Request().Header.Get("Idempotency-Key"))
	if err != nil || key == uuid.Nil {
		return uuid.Nil, errcodes.ValidationError("Idempotency-Key must contain a UUID for this Comment submission.")
	}
	return key, nil
}

func (h *Handler) List(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := mediaID(c)
	if err != nil {
		return err
	}
	page, err := commentPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.List(c.Request().Context(), actor, id, page)
	if mapped := interactionError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Create(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := mediaID(c)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	var request BodyRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	response, err := h.service.Create(c.Request().Context(), actor, id, key, request)
	if mapped := interactionError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, response)
}

func (h *Handler) Edit(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := commentID(c)
	if err != nil {
		return err
	}
	version, err := commentVersion(c)
	if err != nil {
		return err
	}
	var request BodyRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	response, err := h.service.Edit(c.Request().Context(), actor, id, version, request)
	if mapped := interactionError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Delete(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := commentID(c)
	if err != nil {
		return err
	}
	version, err := commentVersion(c)
	if err != nil {
		return err
	}
	if mapped := interactionError(h.service.Delete(c.Request().Context(), actor, id, version)); mapped != nil {
		return mapped
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Mute(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := mediaID(c)
	if err != nil {
		return err
	}
	var request MuteRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	if request.Muted == nil {
		return errcodes.ValidationError("Choose whether Comment notifications are muted.")
	}
	if mapped := interactionError(h.service.SetMuted(c.Request().Context(), actor, id, *request.Muted)); mapped != nil {
		return mapped
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Moderate(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := commentID(c)
	if err != nil {
		return err
	}
	version, err := commentVersion(c)
	if err != nil {
		return err
	}
	var request ModerateRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	if mapped := interactionError(h.service.Moderate(c.Request().Context(), actor, id, version, request)); mapped != nil {
		return mapped
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) CuratorList(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	page, err := commentPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.CuratorList(c.Request().Context(), actor, page)
	if mapped := interactionError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) ModerationHistory(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := commentID(c)
	if err != nil {
		return err
	}
	page, err := commentPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.ModerationHistory(c.Request().Context(), actor, id, page)
	if mapped := interactionError(err); mapped != nil {
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
	group := e.Group("/api/comments", noStore)
	list := group.GET("/media/:media_id", handler.List)
	list.Name = "policy:recipient_content"
	create := group.POST("/media/:media_id", handler.Create)
	create.Name = "policy:recipient_content_csrf"
	curator := group.GET("/curator", handler.CuratorList)
	curator.Name = "policy:curator"
	edit := group.PATCH("/:comment_id", handler.Edit)
	edit.Name = "policy:recipient_content_csrf"
	remove := group.DELETE("/:comment_id", handler.Delete)
	remove.Name = "policy:recipient_content_csrf"
	mute := group.PUT("/media/:media_id/mute", handler.Mute)
	mute.Name = "policy:recipient_content_csrf"
	moderate := group.POST("/:comment_id/moderate", handler.Moderate)
	moderate.Name = "policy:curator_csrf"
	history := group.GET("/:comment_id/moderation-history", handler.ModerationHistory)
	history.Name = "policy:curator"
}
