package identity

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	echologger "github.com/robinjoseph08/golib/echo/v5/middleware/logger"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"golang.org/x/oauth2"
)

const googleCookieName = "memento_google_login"
const googleLoginLifetime = 10 * time.Minute

type googleTransaction struct {
	state, nonce, verifier string
	expires                time.Time
}

type googleHandlers struct {
	identity     *Handlers
	provider     *GoogleProvider
	mu           sync.Mutex
	transactions map[string]googleTransaction
}

func (h *googleHandlers) start(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	transaction := googleTransaction{state: oauth2.GenerateVerifier(), nonce: oauth2.GenerateVerifier(), verifier: oauth2.GenerateVerifier(), expires: time.Now().Add(googleLoginLifetime)}
	authorization, err := h.provider.AuthorizationURL(c.Request().Context(), transaction.state, transaction.nonce, transaction.verifier)
	if err != nil {
		if !errorstack.IsContextCancellation(c.Request().Context(), err) {
			echologger.FromEchoContext(c).Err(err).Error("Google discovery failed")
		}
		return h.failure(c, "provider_unavailable")
	}
	token := oauth2.GenerateVerifier()
	h.mu.Lock()
	for key, pending := range h.transactions {
		if !pending.expires.After(time.Now()) {
			delete(h.transactions, key)
		}
	}
	if previous, err := c.Cookie(googleCookieName); err == nil {
		delete(h.transactions, previous.Value)
	}
	// Bound unauthenticated memory use without a cleanup goroutine or durable login state.
	if len(h.transactions) >= 4096 {
		h.mu.Unlock()
		return h.failure(c, "provider_unavailable")
	}
	h.transactions[token] = transaction
	h.mu.Unlock()
	h.cookie(c, token, int(googleLoginLifetime.Seconds()), transaction.expires)
	return errorstack.CaptureContext(c.Request().Context(), c.Redirect(http.StatusFound, authorization))
}

func (h *googleHandlers) callback(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	h.cookie(c, "", -1, time.Unix(1, 0))
	cookie, err := c.Cookie(googleCookieName)
	if err != nil {
		return h.failure(c, "invalid_state")
	}
	h.mu.Lock()
	transaction, found := h.transactions[cookie.Value]
	delete(h.transactions, cookie.Value)
	h.mu.Unlock()
	query := c.Request().URL.Query()
	state := query.Get("state")
	if !found || !transaction.expires.After(time.Now()) || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(state), []byte(transaction.state)) != 1 {
		return h.failure(c, "invalid_state")
	}
	if query.Get("error") != "" || len(query["code"]) != 1 || query.Get("code") == "" {
		return h.failure(c, "sign_in_failed")
	}
	claims, err := h.provider.Exchange(c.Request().Context(), query.Get("code"), transaction.nonce, transaction.verifier)
	if err != nil {
		if errors.Is(err, ErrGoogleUnavailable) {
			if !errorstack.IsContextCancellation(c.Request().Context(), err) {
				echologger.FromEchoContext(c).Err(err).Error("Google token exchange failed")
			}
			return h.failure(c, "provider_unavailable")
		}
		if errors.Is(err, ErrUnverifiedIdentity) {
			return h.failure(c, "unverified_identity")
		}
		return h.failure(c, "sign_in_failed")
	}
	session, err := h.identity.module.SignIn(WithBrowser(c.Request().Context(), c.Request().UserAgent()), claims)
	if err != nil {
		if errors.Is(err, ErrAccessDenied) {
			return h.failure(c, "access_denied")
		}
		if errors.Is(err, ErrUnverifiedIdentity) {
			return h.failure(c, "unverified_identity")
		}
		if !errors.Is(err, ErrUnauthenticated) && !errorstack.IsContextCancellation(c.Request().Context(), err) {
			echologger.FromEchoContext(c).Err(err).Error("Google sign-in failed")
		}
		return h.failure(c, "sign_in_failed")
	}
	h.identity.setCookie(c, session.Token, session.ExpiresAt)
	return errorstack.CaptureContext(c.Request().Context(), c.Redirect(http.StatusFound, "/"))
}

func (h *googleHandlers) cookie(c *echo.Context, value string, maxAge int, expires time.Time) {
	// #nosec G124 -- Secure follows the validated public URL, including HTTP localhost in development.
	c.SetCookie(&http.Cookie{Name: googleCookieName, Value: value, Path: "/api/identity/google", HttpOnly: true, Secure: strings.HasPrefix(h.identity.publicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: maxAge, Expires: expires})
}

func (h *googleHandlers) failure(c *echo.Context, code string) error {
	return errorstack.CaptureContext(c.Request().Context(), c.Redirect(http.StatusFound, "/sign-in?error="+code))
}
