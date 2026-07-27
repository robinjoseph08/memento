package sources

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

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
}

// Handler exposes the private Curator Source album inbox.
type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) Discover(c echo.Context) error {
	if err := h.authorize(c, true); err != nil {
		return err
	}
	response, err := h.service.Discover(c.Request().Context())
	if errors.Is(err, ErrInvalidConfiguration) {
		return errcodes.ValidationError("Immich must be version v3.0.3 with a valid API key containing exactly the required read permissions.")
	}
	if errors.Is(err, ErrDependency) {
		return errcodes.ServiceUnavailable("Immich is temporarily unavailable. Try discovery again.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) List(c echo.Context) error {
	if err := h.authorize(c, false); err != nil {
		return err
	}
	disposition := c.QueryParam("disposition")
	if disposition == "" {
		disposition = "unreviewed"
	}
	limit, err := positiveQuery(c.QueryParam("limit"), 50)
	if err != nil || limit > 100 {
		return errcodes.ValidationError("Limit must be between 1 and 100.")
	}
	response, err := h.service.List(c.Request().Context(), disposition, c.QueryParam("cursor"), limit)
	if errors.Is(err, ErrInvalidTransition) {
		return errcodes.ValidationError("Disposition must be unreviewed or ignored.")
	}
	if errors.Is(err, ErrInvalidCursor) {
		return errcodes.ValidationError("Cursor is invalid.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Get(c echo.Context) error {
	if err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := sourceID(c)
	if err != nil {
		return err
	}
	album, err := h.service.Get(c.Request().Context(), id)
	if errors.Is(err, ErrNotFound) {
		return errcodes.NotFound("Source album")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, album)
}

func (h *Handler) Ignore(c echo.Context) error {
	return h.mutateDisposition(c, h.service.Ignore)
}

func (h *Handler) Restore(c echo.Context) error {
	return h.mutateDisposition(c, h.service.Restore)
}

func (h *Handler) Reconcile(c echo.Context) error {
	if err := h.authorize(c, true); err != nil {
		return err
	}
	id, err := sourceID(c)
	if err != nil {
		return err
	}
	response, err := h.service.QueueReconciliation(c.Request().Context(), id)
	if errors.Is(err, ErrNotFound) {
		return errcodes.NotFound("Source album")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, response)
}

func (h *Handler) mutateDisposition(c echo.Context, mutate func(context.Context, uuid.UUID, int64) (Album, error)) error {
	if err := h.authorize(c, true); err != nil {
		return err
	}
	id, err := sourceID(c)
	if err != nil {
		return err
	}
	version, err := sourceVersion(c)
	if err != nil {
		return err
	}
	album, err := mutate(c.Request().Context(), id, version)
	switch {
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Source album")
	case errors.Is(err, ErrInvalidTransition):
		return errcodes.Conflict("Source album is already in the requested state.")
	case errors.Is(err, ErrStaleVersion):
		return errcodes.Conflict("Source album changed. Refresh and try again.")
	case err != nil:
		return err
	default:
		return c.JSON(http.StatusOK, album)
	}
}

func (h *Handler) authorize(c echo.Context, mutation bool) error {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return errcodes.Unauthorized("A valid Curator Session is required.")
	}
	_, err = h.authorizer.AuthorizeCurator(
		c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation,
	)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return errcodes.Unauthorized("A valid Curator Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return errcodes.Forbidden("Changing Source albums without a valid CSRF token")
	case errors.Is(err, setup.ErrNotCurator):
		return errcodes.NotFound("Page")
	case err != nil:
		return err
	default:
		return nil
	}
}

func sourceID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Source album")
	}
	return id, nil
}

func sourceVersion(c echo.Context) (int64, error) {
	raw := strings.TrimSpace(c.Request().Header.Get("If-Match"))
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, errcodes.ValidationError("If-Match must contain the current Source album version.")
	}
	return version, nil
}

var errPositiveQuery = errors.New("query value must be positive")

func positiveQuery(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errPositiveQuery
	}
	return value, nil
}

// RegisterRoutes registers private, no-store Curator Source routes.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/sources", noStore)
	list := group.GET("", handler.List)
	list.Name = curatorReadPolicy
	discover := group.POST("/discover", handler.Discover)
	discover.Name = curatorMutationPolicy
	detail := group.GET("/:id", handler.Get)
	detail.Name = curatorReadPolicy
	ignore := group.POST("/:id/ignore", handler.Ignore)
	ignore.Name = curatorMutationPolicy
	restore := group.POST("/:id/restore", handler.Restore)
	restore.Name = curatorMutationPolicy
	reconcile := group.POST("/:id/reconcile", handler.Reconcile)
	reconcile.Name = curatorMutationPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
