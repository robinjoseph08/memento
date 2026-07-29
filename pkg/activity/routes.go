package activity

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const curatorWorkPolicy = "policy:curator"

type curatorAuthorizer interface {
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
}

type curatorWorkLister interface {
	ListCuratorWork(ctx context.Context) (CuratorWorkResponse, error)
}

// Handler exposes the Curator-only work queue.
type Handler struct {
	service    curatorWorkLister
	authorizer curatorAuthorizer
}

func NewHandler(service curatorWorkLister, authorizer curatorAuthorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) ListCuratorWork(c echo.Context) error {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	if _, err := h.authorizer.AuthorizeCurator(c.Request().Context(), cookie.Value, "", false); err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrNotCurator):
			return errcodes.NotFound("Page")
		default:
			return err
		}
	}
	response, err := h.service.ListCuratorWork(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
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
}
