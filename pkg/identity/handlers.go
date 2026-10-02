package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/version"
)

const CookieName = "memento_session"

// UseCases is the HTTP boundary; persistence and claim ordering stay in Module.
type UseCases interface {
	AuthenticationUseCases
	PeopleUseCases
	ProfileUseCases
	FaceUseCases
	AdmissionUseCases
}

type AuthenticationUseCases interface {
	Claimed(context.Context) (bool, error)
	SignIn(context.Context, Claims) (Session, error)
	Authenticate(context.Context, string) (Session, error)
	SignOut(context.Context, string) error
	SignInCodesAvailable() bool
	RequestSignInCode(context.Context, RequestSignInCodeRequest) error
	VerifySignInCode(context.Context, VerifySignInCodeRequest) (Session, error)
	IssueHandoffCode(context.Context, string) (string, error)
	ExchangeHandoffCode(context.Context, string, string) (Session, error)
}

type FaceUseCases interface {
	LinkFace(context.Context, string, string, LinkFaceRequest) (PersonDetail, error)
	CreatePersonFromFace(context.Context, string, CreatePersonFromFaceRequest) (PersonDetail, error)
	IgnoreFace(context.Context, string, string) error
	SetPersonAvatar(context.Context, string, string, SetPersonAvatarRequest) (PersonDetail, error)
}

type PeopleUseCases interface {
	CreatePerson(context.Context, string, CreatePersonRequest) (Person, error)
	ListPeople(context.Context, string, string) ([]PersonSummary, error)
	GetPerson(context.Context, string, string) (PersonDetail, error)
	UpdatePerson(context.Context, string, string, UpdatePersonRequest) (Person, error)
	Preauthorize(context.Context, string, string, PreauthorizeRequest) (Preauthorization, error)
	RevokePreauthorization(context.Context, string, string, string) error
}

type ProfileUseCases interface {
	Profile(context.Context, string) (Profile, error)
	UpdateProfile(context.Context, string, UpdateProfileRequest) (Profile, error)
	Sessions(context.Context, string) ([]BrowserSession, error)
	SignOutEverywhere(context.Context, string) error
	UnlinkEmail(context.Context, string, string, string) error
}

type Handlers struct {
	module                            UseCases
	publicURL                         string
	googleSignIn, fakeSignInAvailable bool
	cookieName                        string
	development                       bool
}

func newHandlers(cfg *config.Config, module UseCases) *Handlers {
	return &Handlers{
		module:              module,
		publicURL:           cfg.PublicURL,
		googleSignIn:        cfg.GoogleConfigured(),
		fakeSignInAvailable: cfg.FakeSignInAvailable(),
		cookieName:          cfg.CookieNamespace + "_session",
		development:         cfg.AppEnv == "development",
	}
}

func (h *Handlers) status(c *echo.Context) error {
	claimed, err := h.module.Claimed(c.Request().Context())
	if err != nil {
		return err
	}
	result := Status{Claimed: claimed, SignInCodes: h.module.SignInCodesAvailable(), SignInMethods: []SignInMethod{}, Version: version.Version}
	if result.SignInCodes {
		result.SignInMethods = append(result.SignInMethods, SignInMethodCode)
	}
	if h.googleSignIn {
		result.SignInMethods = append(result.SignInMethods, SignInMethodGoogle)
		result.AuthMode = "google"
	}
	if h.fakeSignInAvailable {
		result.SignInMethods = append(result.SignInMethods, SignInMethodFake)
		if result.AuthMode == "" {
			result.AuthMode = "fake"
		}
	}
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
	session, err := h.module.SignIn(WithBrowser(c.Request().Context(), c.Request().UserAgent()), FakeClaims(request))
	if err != nil {
		return err
	}
	h.setCookie(c, session.Token, session.ExpiresAt)
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, session.Person))
}

func (h *Handlers) requestSignInCode(c *echo.Context) error {
	var request RequestSignInCodeRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	if err := h.module.RequestSignInCode(c.Request().Context(), request); err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.NoContent(http.StatusNoContent))
}

// verifySignInCode reports the Access Request outcomes as results rather than
// failures, because the page moves on to its next step for each of them.
func (h *Handlers) verifySignInCode(c *echo.Context) error {
	var request VerifySignInCodeRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	session, err := h.module.VerifySignInCode(WithBrowser(c.Request().Context(), c.Request().UserAgent()), request)
	result := SignInCodeResult{Outcome: "signed_in"}
	switch {
	case errors.Is(err, ErrNameRequired):
		result.Outcome = "name_required"
	case errors.Is(err, ErrAccessRequested):
		result.Outcome = "requested"
	case err != nil:
		return err
	default:
		h.setCookie(c, session.Token, session.ExpiresAt)
		result.Person = &session.Person
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}

func (h *Handlers) signOut(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	token, bearer := h.credential(c)
	if token != "" {
		if err := h.module.SignOut(c.Request().Context(), token); err != nil {
			return err
		}
	}
	if !bearer {
		h.clearCookie(c)
	}
	return errorstack.CaptureContext(c.Request().Context(), c.NoContent(http.StatusNoContent))
}

func (h *Handlers) me(c *echo.Context) error {
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, c.Get("identity.person")))
}

// credential is the session token a request carries. The Mobile App sends a
// bearer header, and it wins over any cookie: a browser can only attach that
// header through a cross-origin request that CORS already refuses, so the
// cookie's own checks are never bypassed by a header riding along.
func (h *Handlers) credential(c *echo.Context) (token string, bearer bool) {
	if authorization := c.Request().Header.Get("Authorization"); strings.HasPrefix(authorization, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")), true
	}
	if cookie, err := c.Cookie(h.cookieName); err == nil {
		return cookie.Value, false
	}
	return "", false
}

func (h *Handlers) authenticate(c *echo.Context) (Session, error) {
	token, bearer := h.credential(c)
	if token == "" {
		return Session{}, ErrUnauthenticated
	}
	session, err := h.module.Authenticate(c.Request().Context(), token)
	if errors.Is(err, ErrUnauthenticated) && !bearer {
		h.clearCookie(c)
	}
	if err != nil {
		return Session{}, err
	}
	// Only a daily renewal needs a new cookie. Ordinary responses must not
	// overwrite a newer sign-in cookie if they arrive late. The app keeps its
	// token itself, so a bearer session sets nothing.
	if session.Renewed && !bearer {
		h.setCookie(c, token, session.ExpiresAt)
	}
	return session, nil
}

// RequirePerson admits every active signed-in Person, without inventing onboarding completion.
func (h *Handlers) RequirePerson(next echo.HandlerFunc) echo.HandlerFunc {
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
		c.Set("identity.person", session.Person)
		// Feature handlers receive identity only from this authenticated guard.
		c.Set("identity.person_id", session.Person.ID)
		c.Set("identity.is_curator", session.Person.IsCurator)
		return next(c)
	}
}

// RequireCurator restricts administration to active Curators.
func (h *Handlers) RequireCurator(next echo.HandlerFunc) echo.HandlerFunc {
	return h.RequirePerson(func(c *echo.Context) error {
		person, ok := c.Get("identity.person").(Person)
		if !ok || !person.IsCurator {
			return ErrAccessDenied
		}
		return next(c)
	})
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
	c.SetCookie(&http.Cookie{Name: h.cookieName, Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode, Expires: expires})
}

func (h *Handlers) clearCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{Name: h.cookieName, Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
