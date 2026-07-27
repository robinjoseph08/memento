package audiences

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

type Authorizer interface {
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
	ContextWithRequestMetadata(ctx context.Context, request *http.Request) context.Context
}

type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) ReviewMoment(c echo.Context) error    { return h.review(c, targetMoment) }
func (h *Handler) ReviewLooseItem(c echo.Context) error { return h.review(c, targetLoose) }

func (h *Handler) review(c echo.Context, kind string) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := audienceRouteID(c.Param("id"))
	if err != nil {
		return err
	}
	var response Review
	if kind == targetMoment {
		response, err = h.service.ReviewMoment(c.Request().Context(), id)
	} else {
		response, err = h.service.ReviewLooseItem(c.Request().Context(), id)
	}
	if mapped := audienceError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) ConfirmAttendance(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := audienceRouteID(c.Param("id"))
	if err != nil {
		return err
	}
	var request AttendanceRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	version, err := audienceVersion(c)
	if err != nil {
		return err
	}
	response, err := h.service.ConfirmAttendance(h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request()), actor, id, version, request)
	if mapped := audienceError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) RecalculateMoment(c echo.Context) error    { return h.recalculate(c, targetMoment) }
func (h *Handler) RecalculateLooseItem(c echo.Context) error { return h.recalculate(c, targetLoose) }
func (h *Handler) recalculate(c echo.Context, kind string) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := audienceRouteID(c.Param("id"))
	if err != nil {
		return err
	}
	version, err := audienceVersion(c)
	if err != nil {
		return err
	}
	response, err := h.service.Recalculate(h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request()), actor, kind, id, version)
	if mapped := audienceError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) OverrideMoment(c echo.Context) error    { return h.override(c, targetMoment) }
func (h *Handler) OverrideLooseItem(c echo.Context) error { return h.override(c, targetLoose) }
func (h *Handler) override(c echo.Context, kind string) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := audienceRouteID(c.Param("id"))
	if err != nil {
		return err
	}
	var request OverrideRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	version, err := audienceVersion(c)
	if err != nil {
		return err
	}
	response, err := h.service.SetOverride(h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request()), actor, kind, id, version, request)
	if mapped := audienceError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) ApproveMoment(c echo.Context) error    { return h.approve(c, targetMoment) }
func (h *Handler) ApproveLooseItem(c echo.Context) error { return h.approve(c, targetLoose) }
func (h *Handler) approve(c echo.Context, kind string) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := audienceRouteID(c.Param("id"))
	if err != nil {
		return err
	}
	version, err := audienceVersion(c)
	if err != nil {
		return err
	}
	response, err := h.service.Approve(h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request()), actor, kind, id, version)
	if mapped := audienceError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, response)
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
		return setup.CuratorSession{}, errcodes.Forbidden("Changing Attendance or Audience without a valid CSRF token")
	case errors.Is(err, setup.ErrNotCurator):
		return setup.CuratorSession{}, errcodes.NotFound("Page")
	case err != nil:
		return setup.CuratorSession{}, err
	default:
		return actor, nil
	}
}

func audienceRouteID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Audience review")
	}
	return id, nil
}

func audienceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Audience review")
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("Attendance and Audience fields must be valid and unique.")
	case errors.Is(err, ErrPersonUnavailable):
		return errcodes.Conflict("Every confirmed attendee must be a current Person.")
	case errors.Is(err, ErrRecipientIneligible):
		return errcodes.Conflict("Manual Audience overrides require an Eligible Recipient who is not the Curator.")
	case errors.Is(err, ErrAttendanceUnconfirmed):
		return errcodes.Conflict("Confirm Attendance before approving this Moment's Audience.")
	case errors.Is(err, ErrStale):
		return errcodes.Conflict("This Attendance and Audience review changed in another browser. Reload before making another change.")
	default:
		return err
	}
}

func audienceVersion(c echo.Context) (int64, error) {
	raw := strings.TrimSpace(c.Request().Header.Get("If-Match"))
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, errcodes.ValidationError("If-Match must contain the current Attendance and Audience review version.")
	}
	return version, nil
}

func RegisterRoutes(e *echo.Echo, handler *Handler) {
	moments := e.Group("/api/moments", noStore)
	register := func(method, path string, endpoint echo.HandlerFunc, name string) {
		route := moments.Add(method, path, endpoint)
		route.Name = name
	}
	register(http.MethodGet, "/:id/attendance-audience", handler.ReviewMoment, curatorReadPolicy)
	register(http.MethodPut, "/:id/attendance", handler.ConfirmAttendance, curatorMutationPolicy)
	register(http.MethodPost, "/:id/audience/recalculate", handler.RecalculateMoment, curatorMutationPolicy)
	register(http.MethodPut, "/:id/audience/override", handler.OverrideMoment, curatorMutationPolicy)
	register(http.MethodPost, "/:id/audience/approve", handler.ApproveMoment, curatorMutationPolicy)
	loose := e.Group("/api/loose-items", noStore)
	registerLoose := func(method, path string, endpoint echo.HandlerFunc, name string) {
		route := loose.Add(method, path, endpoint)
		route.Name = name
	}
	registerLoose(http.MethodGet, "/:id/attendance-audience", handler.ReviewLooseItem, curatorReadPolicy)
	registerLoose(http.MethodPost, "/:id/audience/recalculate", handler.RecalculateLooseItem, curatorMutationPolicy)
	registerLoose(http.MethodPut, "/:id/audience/override", handler.OverrideLooseItem, curatorMutationPolicy)
	registerLoose(http.MethodPost, "/:id/audience/approve", handler.ApproveLooseItem, curatorMutationPolicy)
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
