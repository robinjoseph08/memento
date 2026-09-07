package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

const CookieName = "memento_session"

// UseCases is the HTTP boundary; persistence and claim ordering stay in Module.
type UseCases interface {
	Claimed(context.Context) (bool, error)
	SignIn(context.Context, Claims) (Session, error)
	Authenticate(context.Context, string) (Session, error)
	SignOut(context.Context, string) error
}

type Handlers struct {
	module              UseCases
	publicURL, authMode string
}

func (h *Handlers) status(c *echo.Context) error {
	claimed, err := h.module.Claimed(c.Request().Context())
	if err != nil {
		return err
	}
	result := Status{Claimed: claimed, AuthMode: h.authMode}
	if claimed {
		session, err := h.authenticate(c)
		if err != nil && !errors.Is(err, ErrUnauthenticated) {
			return err
		}
		if err == nil {
			result.Person = &session.Person
		}
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}

func (h *Handlers) fakeSignIn(c *echo.Context) error {
	var request SignInRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	session, err := h.module.SignIn(c.Request().Context(), FakeClaims(request))
	if err != nil {
		return err
	}
	h.setCookie(c, session.Token, session.ExpiresAt)
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, session.Person))
}

func (h *Handlers) signOut(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	if cookie, err := c.Cookie(CookieName); err == nil {
		if err := h.module.SignOut(c.Request().Context(), cookie.Value); err != nil {
			return err
		}
	}
	h.clearCookie(c)
	return errorstack.CaptureContext(c.Request().Context(), c.NoContent(http.StatusNoContent))
}

func (h *Handlers) me(c *echo.Context) error {
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, c.Get("identity.person")))
}

func (h *Handlers) authenticate(c *echo.Context) (Session, error) {
	cookie, err := c.Cookie(CookieName)
	if err != nil {
		return Session{}, ErrUnauthenticated
	}
	session, err := h.module.Authenticate(c.Request().Context(), cookie.Value)
	if errors.Is(err, ErrUnauthenticated) {
		h.clearCookie(c)
	}
	if err != nil {
		return Session{}, err
	}
	// Only a daily renewal needs a new cookie. Ordinary responses must not
	// overwrite a newer sign-in cookie if they arrive late.
	if session.Renewed {
		h.setCookie(c, cookie.Value, session.ExpiresAt)
	}
	return session, nil
}

// RequireCurator prevents normal application access until setup and sign-in complete.
func (h *Handlers) RequireCurator(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		claimed, err := h.module.Claimed(c.Request().Context())
		if err != nil {
			return err
		}
		if !claimed {
			return &errcodes.Error{HTTPCode: 409, Code: "setup_required", Message: "Claim this installation before continuing."}
		}
		session, err := h.authenticate(c)
		if err != nil {
			return err
		}
		if !session.Person.IsCurator {
			return ErrAccessDenied
		}
		c.Set("identity.person", session.Person)
		return next(c)
	}
}

// RequireSetupOrCurator exposes installation diagnostics only to installers or Curators.
func (h *Handlers) RequireSetupOrCurator(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		claimed, err := h.module.Claimed(c.Request().Context())
		if err != nil {
			return err
		}
		if !claimed {
			return next(c)
		}
		return h.RequireCurator(next)(c)
	}
}

func (h *Handlers) setCookie(c *echo.Context, token string, expires time.Time) {
	c.SetCookie(&http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode, Expires: expires})
}

func (h *Handlers) clearCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
