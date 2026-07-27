package recipients

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	curatorReadPolicy       = "policy:curator"
	curatorMutationPolicy   = "policy:curator_csrf"
	invitationInspectPolicy = "policy:token_inspect"
	invitationAcceptPolicy  = "policy:token_exchange"
)

type Handler struct {
	service *Service
	auth    *setup.Service
	limiter *acceptanceLimiter
}

func NewHandler(service *Service, auth *setup.Service, security ...config.SecurityConfig) *Handler {
	handler := &Handler{service: service, auth: auth}
	if len(security) != 0 {
		handler.limiter = newAcceptanceLimiter(security[0])
	}
	return handler
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	return h.auth.ContextWithRequestMetadata(c.Request().Context(), c.Request())
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.auth.AuthorizeCurator(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrCSRF):
			return setup.CuratorSession{}, errcodes.Forbidden("Changing Recipient access without a valid CSRF token")
		case errors.Is(err, setup.ErrNotCurator):
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		default:
			return setup.CuratorSession{}, err
		}
	}
	return actor, nil
}

func personID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return uuid.Nil, errcodes.NotFound("Recipient")
	}
	return id, nil
}

func (h *Handler) Get(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := personID(c)
	if err != nil {
		return err
	}
	response, err := h.service.Get(c.Request().Context(), id)
	if err != nil {
		return recipientError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Designate(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := personID(c)
	if err != nil {
		return err
	}
	var request DesignateRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response, err := h.service.Designate(h.requestContext(c), actor, id, request)
	if err != nil {
		return recipientError(err)
	}
	return c.JSON(http.StatusCreated, response)
}

func (h *Handler) Send(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := personID(c)
	if err != nil {
		return err
	}
	response, err := h.service.Send(h.requestContext(c), actor, id)
	if err != nil {
		return recipientError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Reissue(c echo.Context) error {
	return h.invitationAction(c, h.service.Reissue)
}

func (h *Handler) Revoke(c echo.Context) error {
	return h.invitationAction(c, h.service.Revoke)
}

func (h *Handler) Remind(c echo.Context) error {
	return h.invitationAction(c, h.service.Remind)
}

type invitationAction func(context.Context, setup.CuratorSession, uuid.UUID, uuid.UUID) (Recipient, error)

func (h *Handler) invitationAction(c echo.Context, action invitationAction) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := personID(c)
	if err != nil {
		return err
	}
	var request InvitationActionRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	invitationID, err := uuid.Parse(request.InvitationID)
	if err != nil || invitationID == uuid.Nil {
		return errcodes.ValidationError("Choose a valid Invitation.")
	}
	response, err := action(h.requestContext(c), actor, id, invitationID)
	if err != nil {
		return recipientError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Inspect(c echo.Context) error {
	response, err := h.service.Inspect(c.Request().Context(), c.Request().Header.Get("X-Memento-Invitation"))
	if errors.Is(err, ErrInvitationToken) {
		return invitationTokenError()
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Accept(c echo.Context) error {
	var request TokenRequest
	if err := bindJSON(c, &request); err != nil {
		return invitationTokenError()
	}
	ctx := h.requestContext(c)
	if h.limiter != nil && !h.limiter.allow(setup.RequestMetadataFromContext(ctx).ClientIP, request.Token) {
		return errcodes.TooManyRequests("Too many Invitation attempts. Try again later.")
	}
	response, err := h.service.Accept(ctx, request.Token)
	if errors.Is(err, ErrInvitationToken) {
		return invitationTokenError()
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func recipientError(err error) error {
	switch {
	case errors.Is(err, ErrPersonNotFound), errors.Is(err, ErrRecipientNotFound):
		return errcodes.NotFound("Recipient")
	case errors.Is(err, ErrPersonUnavailable), errors.Is(err, ErrEmailInvalid):
		return errcodes.ValidationError("Choose a current Person and valid login email.")
	case errors.Is(err, ErrAlreadyRecipient):
		return errcodes.Conflict("This Person already has current Recipient access.")
	case errors.Is(err, ErrEmailInUse):
		return errcodes.Conflict("That login email is already assigned to a current Recipient.")
	case errors.Is(err, ErrInvitationExists):
		return errcodes.Conflict("Use the current Invitation or explicitly reissue it.")
	case errors.Is(err, ErrInvitationNotFound), errors.Is(err, ErrInvitationNotLive):
		return errcodes.Conflict("There is no live Invitation for this Pending Recipient.")
	case errors.Is(err, ErrInvitationStale):
		return errcodes.Conflict("The Invitation changed. Refresh before trying again.")
	case errors.Is(err, ErrInvitationNotSent):
		return errcodes.Conflict("Wait for the initial Invitation delivery before sending a reminder.")
	case errors.Is(err, ErrInvitationState):
		return errcodes.Conflict("This Recipient state does not permit Invitation changes.")
	case errors.Is(err, emaildelivery.ErrNotConfigured):
		return errcodes.ServiceUnavailable("SMTP is not configured.")
	default:
		return err
	}
}

func invitationTokenError() error {
	return errcodes.NotFound("Invitation")
}

func bindJSON(c echo.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return errcodes.UnsupportedMediaType()
	}
	return c.Bind(target)
}

func tokenHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		c.Response().Header().Set("Referrer-Policy", "no-referrer")
		return next(c)
	}
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

// RegisterRoutes declares one primary policy for each Curator and token route.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/recipients", noStore)
	get := group.GET("/:person_id", handler.Get)
	get.Name = curatorReadPolicy
	designate := group.POST("/:person_id/designate", handler.Designate)
	designate.Name = curatorMutationPolicy
	send := group.POST("/:person_id/invitation/send", handler.Send)
	send.Name = curatorMutationPolicy
	revoke := group.POST("/:person_id/invitation/revoke", handler.Revoke)
	revoke.Name = curatorMutationPolicy
	reissue := group.POST("/:person_id/invitation/reissue", handler.Reissue)
	reissue.Name = curatorMutationPolicy
	remind := group.POST("/:person_id/invitation/remind", handler.Remind)
	remind.Name = curatorMutationPolicy

	tokens := e.Group("/api/auth/invitations", tokenHeaders)
	inspect := tokens.GET("/inspect", handler.Inspect)
	inspect.Name = invitationInspectPolicy
	accept := tokens.POST("/accept", handler.Accept)
	accept.Name = invitationAcceptPolicy
}
