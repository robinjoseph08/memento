package push

import (
	"errors"
	"mime"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	selfReadPolicy     = "policy:recipient_self"
	selfMutationPolicy = "policy:recipient_self_csrf"
)

type Handler struct {
	service *Service
	auth    *setup.Service
}

func NewHandler(service *Service, auth *setup.Service) *Handler {
	return &Handler{service: service, auth: auth}
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.SessionActor, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.auth.AuthorizeSession(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if errors.Is(err, setup.ErrCSRF) {
		return setup.SessionActor{}, errcodes.Forbidden("Changing push notifications without a valid CSRF token")
	}
	if errors.Is(err, setup.ErrUnauthenticated) {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
	}
	return actor, err
}

func (h *Handler) Configuration(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.Configuration(c.Request().Context(), actor)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Enroll(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request SubscriptionRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response, err := h.service.Enroll(c.Request().Context(), actor, request)
	if err != nil {
		return pushError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Reconcile(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request ReconcileRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response, err := h.service.Reconcile(c.Request().Context(), actor, request)
	if err != nil {
		return pushError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Disable(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	if err := h.service.Disable(c.Request().Context(), actor); err != nil {
		return pushError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func bindJSON(c echo.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return errcodes.UnsupportedMediaType()
	}
	return c.Bind(target)
}

func pushError(err error) error {
	switch {
	case errors.Is(err, ErrUnavailable):
		return errcodes.ServiceUnavailable("Push notifications are unavailable.")
	case errors.Is(err, ErrTrustedRequired):
		return errcodes.Forbidden("Enabling push notifications from this Session")
	case errors.Is(err, ErrSubscription), errors.Is(err, ErrEndpointInvalid), errors.Is(err, ErrEndpointPrivate):
		return errcodes.ValidationError("The browser push subscription is invalid.")
	case errors.Is(err, ErrEndpointConflict):
		return errcodes.Conflict("This browser push subscription is already active on another Session.")
	default:
		return err
	}
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/push", noStore)
	configuration := group.GET("", handler.Configuration)
	configuration.Name = selfReadPolicy
	enroll := group.POST("", handler.Enroll)
	enroll.Name = selfMutationPolicy
	reconcile := group.POST("/reconcile", handler.Reconcile)
	reconcile.Name = selfMutationPolicy
	disable := group.DELETE("", handler.Disable)
	disable.Name = selfMutationPolicy
}
