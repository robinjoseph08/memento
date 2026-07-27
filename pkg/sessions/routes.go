package sessions

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	publicAuthPolicy      = "policy:public_safe"
	selfReadPolicy        = "policy:recipient_self"
	selfMutationPolicy    = "policy:recipient_self_csrf"
	curatorReadPolicy     = "policy:curator"
	curatorMutationPolicy = "policy:curator_csrf"
)

type Handler struct {
	service *Service
	auth    *setup.Service
	limiter *limiter
}

func NewHandler(service *Service, auth *setup.Service) *Handler {
	h := &Handler{service: service, auth: auth}
	if service != nil {
		h.limiter = newLimiter(service.security)
	}
	return h
}
func (h *Handler) requestContext(c echo.Context) context.Context {
	return h.auth.ContextWithRequestMetadata(c.Request().Context(), c.Request())
}
func (h *Handler) credential(c echo.Context) (string, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return "", errcodes.Unauthorized("A valid Session is required.")
	}
	return cookie.Value, nil
}
func (h *Handler) authorize(c echo.Context, mutation bool) (setup.SessionActor, error) {
	credential, err := h.credential(c)
	if err != nil {
		return setup.SessionActor{}, err
	}
	actor, err := h.auth.AuthorizeSession(c.Request().Context(), credential, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		if errors.Is(err, setup.ErrCSRF) {
			return setup.SessionActor{}, errcodes.Forbidden("Changing Sessions without a valid CSRF token")
		}
		if errors.Is(err, setup.ErrUnauthenticated) {
			return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
		}
		return setup.SessionActor{}, err
	}
	return actor, nil
}
func (h *Handler) curator(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	credential, err := h.credential(c)
	if err != nil {
		return setup.CuratorSession{}, err
	}
	actor, err := h.auth.AuthorizeCurator(c.Request().Context(), credential, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		if errors.Is(err, setup.ErrCSRF) {
			return setup.CuratorSession{}, errcodes.Forbidden("Changing Recipient identity without a valid CSRF token")
		}
		if errors.Is(err, setup.ErrNotCurator) {
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		}
		if errors.Is(err, setup.ErrUnauthenticated) {
			return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
		}
		return setup.CuratorSession{}, err
	}
	return actor, nil
}

func (h *Handler) RequestSignIn(c echo.Context) error {
	var request SignInRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	ctx := h.requestContext(c)
	metadata := setup.RequestMetadataFromContext(ctx)
	if !h.limiter.allow(metadata.ClientIP, request.Email) {
		return errcodes.TooManyRequests("Too many sign-in attempts. Try again later.")
	}
	response, err := h.service.RequestSignIn(ctx, request)
	if err != nil {
		if errors.Is(err, ErrInvalidIdentity) {
			return errcodes.ValidationError("Enter a valid email address.")
		}
		if errors.Is(err, emaildelivery.ErrNotConfigured) {
			return errcodes.ServiceUnavailable("Email sign-in is unavailable.")
		}
		return err
	}
	return c.JSON(http.StatusAccepted, response)
}
func (h *Handler) VerifySignIn(c echo.Context) error {
	var request SignInVerifyRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	ctx := h.requestContext(c)
	response, err := h.service.VerifySignIn(ctx, request)
	if err != nil {
		if errors.Is(err, ErrInvalidCode) {
			return errcodes.BadRequest("The sign-in code is invalid or expired.")
		}
		return err
	}
	setup.SetSessionCookie(c, response.session)
	return c.JSON(http.StatusOK, response)
}
func (h *Handler) List(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.ListSelf(c.Request().Context(), actor)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}
func parseID(c echo.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Session")
	}
	return id, nil
}
func (h *Handler) Rename(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := parseID(c, "session_id")
	if err != nil {
		return err
	}
	var request RenameRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	err = h.service.Rename(h.requestContext(c), actor, id, request)
	if errors.Is(err, ErrInvalidLabel) {
		return errcodes.ValidationError("Use a Session name of 80 characters or fewer.")
	}
	if errors.Is(err, ErrInvalidSession) {
		return errcodes.NotFound("Session")
	}
	if err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
func (h *Handler) Revoke(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := parseID(c, "session_id")
	if err != nil {
		return err
	}
	current, err := h.service.Revoke(h.requestContext(c), actor, id)
	if errors.Is(err, ErrInvalidSession) {
		return errcodes.NotFound("Session")
	}
	if err != nil {
		return err
	}
	if current {
		clearCookie(c)
	}
	return c.NoContent(http.StatusNoContent)
}
func (h *Handler) SignOutAll(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	if err := h.service.SignOutAll(h.requestContext(c), actor); err != nil {
		return err
	}
	clearCookie(c)
	return c.NoContent(http.StatusNoContent)
}
func (h *Handler) StartEmailChange(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request EmailChangeRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	ctx := h.requestContext(c)
	metadata := setup.RequestMetadataFromContext(ctx)
	if !h.limiter.allowIdentity("email-change", metadata.ClientIP, actor.AccessID.String(), request.NewEmail) {
		return errcodes.TooManyRequests("Too many email-change requests. Try again later.")
	}
	response, err := h.service.StartEmailChange(ctx, actor, request)
	if err != nil {
		return identityError(err)
	}
	return c.JSON(http.StatusAccepted, response)
}
func (h *Handler) CompleteEmailChange(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request EmailChangeCompleteRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response, err := h.service.CompleteEmailChange(h.requestContext(c), actor, request)
	if err != nil {
		return identityError(err)
	}
	setup.SetSessionCookie(c, response.session)
	return c.JSON(http.StatusOK, response)
}
func (h *Handler) CuratorList(c echo.Context) error {
	if _, err := h.curator(c, false); err != nil {
		return err
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	response, err := h.service.ListRecipient(c.Request().Context(), personID)
	if errors.Is(err, ErrInvalidSession) {
		return errcodes.NotFound("Recipient")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}
func (h *Handler) StartRecovery(c echo.Context) error {
	actor, err := h.curator(c, true)
	if err != nil {
		return err
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	var request RecoveryRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	ctx := h.requestContext(c)
	metadata := setup.RequestMetadataFromContext(ctx)
	if !h.limiter.allowIdentity("email-recovery", metadata.ClientIP, actor.PersonID.String(), request.NewEmail) {
		return errcodes.TooManyRequests("Too many email-recovery requests. Try again later.")
	}
	response, err := h.service.StartRecovery(ctx, actor, personID, request)
	if err != nil {
		return identityError(err)
	}
	return c.JSON(http.StatusAccepted, response)
}
func (h *Handler) CompleteRecovery(c echo.Context) error {
	actor, err := h.curator(c, true)
	if err != nil {
		return err
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	var request RecoveryCompleteRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if err := h.service.CompleteRecovery(h.requestContext(c), actor, personID, request); err != nil {
		return identityError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
func identityError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidIdentity):
		return errcodes.ValidationError("Enter a valid new email address.")
	case errors.Is(err, ErrEmailInUse):
		return errcodes.Conflict("That login email is already assigned to a current Recipient.")
	case errors.Is(err, ErrEmailUnchanged):
		return errcodes.ValidationError("Enter a different email address.")
	case errors.Is(err, ErrInvalidCode):
		return errcodes.BadRequest("The verification code is invalid or expired.")
	case errors.Is(err, ErrChangeNotFound), errors.Is(err, ErrRecoveryNotFound):
		return errcodes.Conflict("This email verification is no longer available.")
	case errors.Is(err, ErrRecoveryCurator):
		return errcodes.Conflict("Use signed-in email change for the Curator.")
	case errors.Is(err, emaildelivery.ErrNotConfigured):
		return errcodes.ServiceUnavailable("Required email is unavailable.")
	default:
		return err
	}
}
func bindJSON(c echo.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return errcodes.UnsupportedMediaType()
	}
	return c.Bind(target)
}
func clearCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{Name: setup.CookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}
func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

// RegisterRoutes declares one primary authorization policy for every Session route.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	auth := e.Group("/api/auth/sign-in", noStore)
	start := auth.POST("/request", handler.RequestSignIn)
	start.Name = publicAuthPolicy
	verify := auth.POST("/verify", handler.VerifySignIn)
	verify.Name = publicAuthPolicy
	group := e.Group("/api/sessions", noStore)
	list := group.GET("", handler.List)
	list.Name = selfReadPolicy
	rename := group.PATCH("/:session_id", handler.Rename)
	rename.Name = selfMutationPolicy
	revoke := group.DELETE("/:session_id", handler.Revoke)
	revoke.Name = selfMutationPolicy
	all := group.POST("/sign-out-all", handler.SignOutAll)
	all.Name = selfMutationPolicy
	me := e.Group("/api/me/email-change", noStore)
	changeStart := me.POST("/request", handler.StartEmailChange)
	changeStart.Name = selfMutationPolicy
	changeComplete := me.POST("/complete", handler.CompleteEmailChange)
	changeComplete.Name = selfMutationPolicy
	recipients := e.Group("/api/recipients", noStore)
	inspect := recipients.GET("/:person_id/sessions", handler.CuratorList)
	inspect.Name = curatorReadPolicy
	recoveryStart := recipients.POST("/:person_id/email-recovery/request", handler.StartRecovery)
	recoveryStart.Name = curatorMutationPolicy
	recoveryComplete := recipients.POST("/:person_id/email-recovery/complete", handler.CompleteRecovery)
	recoveryComplete.Name = curatorMutationPolicy
}
