package setup

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const (
	CookieName = "__Host-memento_session"
	CSRFHeader = "X-Memento-CSRF"

	setupOnlyPolicy       = "policy:setup_only"
	sessionPolicy         = "policy:session"
	sessionMutationPolicy = "policy:session_csrf"
)

// Handler exposes first-browser setup and the resulting browser Session.
type Handler struct {
	service *Service
	limiter *setupLimiter
}

func NewHandler(service *Service) *Handler {
	handler := &Handler{service: service}
	if service != nil {
		handler.limiter = newSetupLimiter(service.security)
	}
	return handler
}

func (h *Handler) Available(c echo.Context) error {
	response, err := h.service.Available(h.requestContext(c))
	if errors.Is(err, ErrSetupComplete) {
		return errcodes.NotFound("Setup")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) RequestCode(c echo.Context) error {
	var request RequestCodeRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if !h.limiter.allowRequestCode(h.clientIP(c), request.Email) {
		return setupRateLimited()
	}
	response, err := h.service.RequestCode(h.requestContext(c), request)
	if err != nil {
		switch {
		case errors.Is(err, ErrSetupComplete):
			return setupConflict()
		case errors.Is(err, ErrInvalidIdentity):
			return errcodes.ValidationError("Enter a valid name and email address.")
		case errors.Is(err, emaildelivery.ErrNotConfigured):
			return errcodes.ServiceUnavailable("SMTP is not configured.")
		default:
			return err
		}
	}
	return c.JSON(http.StatusAccepted, response)
}

func (h *Handler) VerifyCode(c echo.Context) error {
	var request VerifyCodeRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if !h.limiter.allowIP(h.clientIP(c)) {
		return setupRateLimited()
	}
	response, err := h.service.VerifyCode(h.requestContext(c), request)
	if err != nil {
		switch {
		case errors.Is(err, ErrSetupComplete):
			return setupConflict()
		case errors.Is(err, ErrInvalidCode):
			return errcodes.BadRequest("The code is invalid or expired.")
		default:
			return err
		}
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Complete(c echo.Context) error {
	var request CompleteRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if !h.limiter.allowIP(h.clientIP(c)) {
		return setupRateLimited()
	}
	if !secureSetupCompletion(c.Request(), h.service.security.TrustedProxyCIDRs) {
		return errcodes.BadRequest("Setup completion requires HTTPS or a loopback address.")
	}
	completed, err := h.service.complete(h.requestContext(c), request)
	if err != nil {
		switch {
		case errors.Is(err, ErrSetupComplete):
			return setupConflict()
		case errors.Is(err, ErrInvalidToken):
			return errcodes.BadRequest("Setup verification is invalid or expired.")
		case errors.Is(err, ErrInvalidChoices):
			return errcodes.ValidationError("Complete every Onboarding choice.")
		default:
			return err
		}
	}
	setSessionCookie(c, completed)
	return c.JSON(http.StatusCreated, CompleteResponse{Status: "complete", CSRFToken: completed.CSRFToken})
}

func (h *Handler) Session(c echo.Context) error {
	credential, err := sessionCredential(c)
	if err != nil {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	response, err := h.service.Session(h.requestContext(c), credential)
	if errors.Is(err, ErrUnauthenticated) {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Refresh(c echo.Context) error {
	credential, err := sessionCredential(c)
	if err != nil {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	refreshed, err := h.service.refresh(
		h.requestContext(c),
		credential,
		c.Request().Header.Get(CSRFHeader),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnauthenticated):
			return errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, ErrCSRF):
			return errcodes.Forbidden("Changing this Session without a valid CSRF token")
		case errors.Is(err, ErrSessionType):
			return errcodes.BadRequest("Only a Trusted-device Session can be refreshed.")
		default:
			return err
		}
	}
	setSessionCookie(c, refreshed)
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Logout(c echo.Context) error {
	credential, err := sessionCredential(c)
	if err != nil {
		return errcodes.Unauthorized("A valid Session is required.")
	}
	err = h.service.Logout(
		h.requestContext(c),
		credential,
		c.Request().Header.Get(CSRFHeader),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnauthenticated):
			return errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, ErrCSRF):
			return errcodes.Forbidden("Changing this Session without a valid CSRF token")
		default:
			return err
		}
	}
	clearSessionCookie(c)
	return c.NoContent(http.StatusNoContent)
}

// RegisterRoutes registers setup-only and Session-self routes.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	setupGroup := e.Group("/api/setup", noStore)
	available := setupGroup.GET("", handler.Available)
	available.Name = setupOnlyPolicy
	requestCode := setupGroup.POST("/code", handler.RequestCode)
	requestCode.Name = setupOnlyPolicy
	verifyCode := setupGroup.POST("/verify", handler.VerifyCode)
	verifyCode.Name = setupOnlyPolicy
	complete := setupGroup.POST("/complete", handler.Complete)
	complete.Name = setupOnlyPolicy

	sessionGroup := e.Group("/api/session", noStore)
	session := sessionGroup.GET("", handler.Session)
	session.Name = sessionPolicy
	refresh := sessionGroup.POST("/refresh", handler.Refresh)
	refresh.Name = sessionMutationPolicy
	logout := sessionGroup.POST("/logout", handler.Logout)
	logout.Name = sessionMutationPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

func bindJSON(c echo.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return errcodes.UnsupportedMediaType()
	}
	return c.Bind(target)
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	return withRequestMetadata(c.Request().Context(), c.Request(), h.service.security.TrustedProxyCIDRs)
}

func (h *Handler) clientIP(c echo.Context) string {
	return clientIP(c.Request(), h.service.security.TrustedProxyCIDRs)
}

func setupConflict() error {
	return errcodes.Conflict("Setup is no longer available.")
}

func setupRateLimited() error {
	return errcodes.TooManyRequests("Too many setup attempts. Try again later.")
}

func sessionCredential(c echo.Context) (string, error) {
	cookie, err := c.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return "", ErrUnauthenticated
	}
	return cookie.Value, nil
}

func setSessionCookie(c echo.Context, session completedSession) {
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    session.Credential,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if session.SessionType == "trusted" {
		cookie.MaxAge = int(trustedLifetime.Seconds())
		if session.ExpiresAt != nil {
			cookie.Expires = *session.ExpiresAt
		} else {
			cookie.Expires = time.Now().UTC().Add(trustedLifetime)
		}
	}
	c.SetCookie(cookie)
}

func clearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}
