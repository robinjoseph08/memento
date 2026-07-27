package repairs

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
	AuthorizeCurator(ctx context.Context, credential, csrf string, mutation bool) (setup.CuratorSession, error)
}

// Handler exposes private evidence and explicit confirmation actions.
type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) List(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	response, err := h.service.List(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Reconcile(c echo.Context) error {
	if _, err := h.authorize(c, true); err != nil {
		return err
	}
	response, err := h.service.ReconcilePeople(c.Request().Context())
	if errors.Is(err, ErrDependency) {
		return errcodes.ServiceUnavailable("Immich identity evidence is temporarily unavailable.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) LinkPerson(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request LinkPersonRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	response, err := h.service.LinkPerson(c.Request().Context(), actor, request)
	return mutationResult(c, response, err)
}

func (h *Handler) ConfirmPerson(c echo.Context) error { return h.resolve(c, h.service.ConfirmPerson) }
func (h *Handler) RejectPerson(c echo.Context) error  { return h.resolve(c, h.service.RejectPerson) }
func (h *Handler) ConfirmMedia(c echo.Context) error  { return h.resolve(c, h.service.ConfirmMedia) }
func (h *Handler) RejectMedia(c echo.Context) error   { return h.resolve(c, h.service.RejectMedia) }

func (h *Handler) resolve(c echo.Context, action func(context.Context, setup.CuratorSession, uuid.UUID) (MutationResponse, error)) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return errcodes.NotFound("Repair candidate")
	}
	response, err := action(c.Request().Context(), actor, id)
	return mutationResult(c, response, err)
}

func mutationResult(c echo.Context, response MutationResponse, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Repair candidate")
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("The repair confirmation is invalid.")
	case errors.Is(err, ErrConflict):
		return errcodes.Conflict("Repair evidence changed or conflicts with another confirmed link. Reconcile and review again.")
	case errors.Is(err, ErrAlreadyResolved):
		return errcodes.Conflict("Repair candidate was already resolved.")
	case errors.Is(err, ErrDependency):
		return errcodes.ServiceUnavailable("Immich identity evidence is temporarily unavailable.")
	case err != nil:
		return err
	default:
		return c.JSON(http.StatusOK, response)
	}
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	}
	actor, err := h.authorizer.AuthorizeCurator(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return setup.CuratorSession{}, errcodes.Forbidden("Changing repair state without a valid CSRF token")
	case errors.Is(err, setup.ErrNotCurator):
		return setup.CuratorSession{}, errcodes.NotFound("Page")
	case err != nil:
		return setup.CuratorSession{}, err
	default:
		return actor, nil
	}
}

// RegisterRoutes registers Curator-only no-store repair routes.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/repairs", noStore)
	read := group.GET("", handler.List)
	read.Name = curatorReadPolicy
	reconcile := group.POST("/reconcile", handler.Reconcile)
	reconcile.Name = curatorMutationPolicy
	link := group.POST("/people/link", handler.LinkPerson)
	link.Name = curatorMutationPolicy
	confirmPerson := group.POST("/people/:id/confirm", handler.ConfirmPerson)
	confirmPerson.Name = curatorMutationPolicy
	rejectPerson := group.POST("/people/:id/reject", handler.RejectPerson)
	rejectPerson.Name = curatorMutationPolicy
	confirmMedia := group.POST("/media/:id/confirm", handler.ConfirmMedia)
	confirmMedia.Name = curatorMutationPolicy
	rejectMedia := group.POST("/media/:id/reject", handler.RejectMedia)
	rejectMedia.Name = curatorMutationPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
