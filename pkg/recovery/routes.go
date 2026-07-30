package recovery

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	publicRecoveryPolicy  = "policy:public_safe"
	curatorRecoveryPolicy = "policy:recovery_curator"
)

// Handler exposes the narrowly scoped Recovery review and release workflow.
type Handler struct {
	service *Service
	auth    *setup.Service
}

func NewHandler(service *Service, auth *setup.Service) *Handler {
	return &Handler{service: service, auth: auth}
}

func (h *Handler) Status(c echo.Context) error {
	response, err := h.service.Status(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Review(c echo.Context) error {
	actor, err := h.curator(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.Review(c.Request().Context(), actor)
	if errors.Is(err, ErrNotHeld) {
		return errcodes.Conflict("Recovery hold is no longer active.")
	}
	if errors.Is(err, ErrFreshCurator) {
		return errcodes.Unauthorized("A fresh Curator Session is required.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) AcknowledgeReview(c echo.Context) error {
	actor, err := h.curator(c, true)
	if err != nil {
		return err
	}
	ctx := h.auth.ContextWithRequestMetadata(c.Request().Context(), c.Request())
	if err := h.service.AcknowledgeReview(ctx, actor); err != nil {
		switch {
		case errors.Is(err, ErrNotHeld):
			return errcodes.Conflict("Recovery hold is no longer active.")
		case errors.Is(err, ErrFreshCurator):
			return errcodes.Unauthorized("A fresh Curator Session is required.")
		default:
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Release(c echo.Context) error {
	actor, err := h.curator(c, true)
	if err != nil {
		return err
	}
	ctx := h.auth.ContextWithRequestMetadata(c.Request().Context(), c.Request())
	if err := h.service.Release(ctx, actor); err != nil {
		switch {
		case errors.Is(err, ErrNotHeld):
			return errcodes.Conflict("Recovery hold is no longer active.")
		case errors.Is(err, ErrFreshCurator):
			return errcodes.Unauthorized("A fresh Curator Session is required.")
		case errors.Is(err, ErrReviewRequired):
			return errcodes.Conflict("Review restored state before releasing Recovery hold.")
		default:
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) curator(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A fresh Curator Session is required.")
	}
	actor, err := h.auth.AuthorizeCurator(c.Request().Context(), cookie.Value,
		c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrCSRF):
			return setup.CuratorSession{}, errcodes.Forbidden("Releasing Recovery hold requires a valid CSRF token.")
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.CuratorSession{}, errcodes.Unauthorized("A fresh Curator Session is required.")
		case errors.Is(err, setup.ErrNotCurator):
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		default:
			return setup.CuratorSession{}, err
		}
	}
	return actor, nil
}

// RegisterRoutes registers the only review surface available while held.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/recovery", noStore)
	status := group.GET("/status", handler.Status)
	status.Name = publicRecoveryPolicy
	review := group.GET("/review", handler.Review)
	review.Name = curatorRecoveryPolicy
	acknowledge := group.POST("/review/complete", handler.AcknowledgeReview)
	acknowledge.Name = curatorRecoveryPolicy
	release := group.POST("/release", handler.Release)
	release.Name = curatorRecoveryPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
