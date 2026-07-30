package activity

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

const curatorWorkPolicy = "policy:curator"

type curatorAuthorizer interface {
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
}

type curatorWorkLister interface {
	ListCuratorWork(ctx context.Context, page WorkPageRequest) (CuratorWorkResponse, error)
	ListCuratorActivity(ctx context.Context, page PageRequest) (CuratorActivityResponse, error)
	MarkRead(ctx context.Context, curatorID uuid.UUID, request MarkReadRequest) error
}

// Handler exposes the Curator-only work queue.
type Handler struct {
	service    curatorWorkLister
	authorizer curatorAuthorizer
}

func NewHandler(service curatorWorkLister, authorizer curatorAuthorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.authorizer.AuthorizeCurator(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrNotCurator):
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		case errors.Is(err, setup.ErrCSRF):
			return setup.CuratorSession{}, errcodes.Forbidden("This action requires a valid CSRF token")
		default:
			return setup.CuratorSession{}, err
		}
	}
	return actor, nil
}

func (h *Handler) ListCuratorWork(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return errcodes.ValidationError("Use a work queue limit from 1 to 100.")
		}
		limit = parsed
	}
	response, err := h.service.ListCuratorWork(c.Request().Context(), WorkPageRequest{
		Cursor: c.QueryParam("cursor"), Limit: limit,
	})
	if errors.Is(err, ErrInvalidCursor) {
		return errcodes.ValidationError("Use a valid work queue cursor and limit from 1 to 100.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) ListCuratorActivity(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return errcodes.ValidationError("Use an activity limit from 1 to 100.")
		}
		limit = parsed
	}
	unread := false
	if raw := c.QueryParam("unread"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return errcodes.ValidationError("Use true or false for the unread filter.")
		}
		unread = parsed
	}
	response, err := h.service.ListCuratorActivity(c.Request().Context(), PageRequest{
		Category: c.QueryParam("category"), Cursor: c.QueryParam("cursor"), Limit: limit, Unread: unread,
	})
	if errors.Is(err, ErrInvalidCursor) {
		return errcodes.ValidationError("Use a valid activity category, cursor, and limit from 1 to 100.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) MarkRead(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request MarkReadRequest
	if err := c.Bind(&request); err != nil {
		return errcodes.ValidationError("Use a valid Curator item read state.")
	}
	if err := h.service.MarkRead(c.Request().Context(), actor.PersonID, request); err != nil {
		switch {
		case errors.Is(err, ErrInvalidReadState):
			return errcodes.ValidationError("Use a valid Curator item read state.")
		case errors.Is(err, ErrVersionConflict):
			return errcodes.Conflict("This Curator item changed. Review the latest version.")
		case errors.Is(err, ErrWorkNotFound):
			return errcodes.NotFound("Curator item")
		default:
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func curatorWorkHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

// RegisterRoutes exposes delivery problems on the Curator's intended activity work surface.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	work := e.GET("/api/activity/curator/work", handler.ListCuratorWork, curatorWorkHeaders)
	work.Name = curatorWorkPolicy
	feed := e.GET("/api/activity/curator", handler.ListCuratorActivity, curatorWorkHeaders)
	feed.Name = curatorWorkPolicy
	read := e.POST("/api/activity/curator/read", handler.MarkRead, curatorWorkHeaders)
	read.Name = curatorWorkPolicy
}
