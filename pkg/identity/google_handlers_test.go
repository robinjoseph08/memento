package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/server"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
)

type googleHTTPModule struct {
	identity.UseCases
	signInError error
}

func (*googleHTTPModule) Claimed(context.Context) (bool, error) { return true, nil }
func (m *googleHTTPModule) SignIn(_ context.Context, claims identity.Claims) (identity.Session, error) {
	if m.signInError != nil {
		return identity.Session{}, m.signInError
	}
	if claims.Subject != "google-subject" || !claims.EmailVerified {
		return identity.Session{}, identity.ErrUnauthenticated
	}
	return identity.Session{Token: "opaque-session", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*googleHTTPModule) Authenticate(context.Context, string) (identity.Session, error) {
	return identity.Session{}, identity.ErrUnauthenticated
}
func (*googleHTTPModule) SignOut(context.Context, string) error { return nil }

func googleHTTP(s *oidcSubstitute, publicURL string, module *googleHTTPModule) *echo.Echo {
	e := echo.New()
	cfg := config.NewForTest()
	cfg.AuthMode = "google"
	cfg.PublicURL = publicURL
	cfg.GoogleClientID = "client-id"
	cfg.GoogleClientSecret = "client-secret"
	identity.RegisterGoogleRoutes(e, cfg, module, identity.WithGoogleIssuer(s.server.URL, s.server.Client()))
	return e
}

func startGoogle(t *testing.T, e *echo.Echo) (*http.Cookie, url.Values) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/identity/google/start", nil)
	req.Header.Set("X-Forwarded-Host", "evil.example.com")
	req.Header.Set("X-Forwarded-Proto", "http")
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	u, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	return cookies[0], u.Query()
}

func googleCallback(e *echo.Echo, cookie *http.Cookie, query url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/identity/google/callback?"+query.Encode(), nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestGoogleLoginUsesCookieNamespace(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	cfg := config.NewForTest()
	cfg.AuthMode = "google"
	cfg.CookieNamespace = "memento_feature_worktree"
	cfg.GoogleClientID = "client-id"
	cfg.GoogleClientSecret = "client-secret"
	e := echo.New()
	identity.RegisterGoogleRoutes(e, cfg, &googleHTTPModule{}, identity.WithGoogleIssuer(s.server.URL, s.server.Client()))

	cookie, _ := startGoogle(t, e)
	require.Equal(t, "memento_feature_worktree_google_login", cookie.Name)
}

func TestGoogleOutageLeavesDatabaseHealthHealthy(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	cfg := config.NewForTest()
	cfg.AuthMode = "google"
	cfg.GoogleClientID = "client-id"
	cfg.GoogleClientSecret = "client-secret"
	app, err := server.New(cfg, db)
	require.NoError(t, err)
	e, ok := app.Handler.(*echo.Echo)
	require.True(t, ok)
	substitute := newOIDCSubstitute(t)
	substitute.discoveryStatus.Store(http.StatusServiceUnavailable)
	identity.RegisterGoogleRoutes(e, cfg, identity.New(db, nil), identity.WithGoogleIssuer(substitute.server.URL, substitute.server.Client()))
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/identity/google/start", nil))
	require.Equal(t, "/sign-in?error=provider_unavailable", rec.Header().Get("Location"))
	rec = httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"healthy":true}`, rec.Body.String())
}

func TestGoogleHTTPFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		modify     func(*oidcSubstitute, *googleHTTPModule, *http.Cookie, url.Values)
	}{
		{"state mismatch", "invalid_state", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { q.Set("state", "wrong") }},
		{"missing state", "invalid_state", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { q.Del("state") }},
		{"duplicate state", "invalid_state", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			q.Add("state", q.Get("state"))
		}},
		{"unknown cookie", "invalid_state", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { c.Value = "unknown" }},
		{"missing cookie", "invalid_state", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { c.Name = "unrelated" }},
		{"consent denied", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			q.Set("error", "access_denied")
			q.Set("error_description", "private-detail")
		}},
		{"missing code", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { q.Del("code") }},
		{"duplicate code", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { q.Add("code", "another") }},
		{"nonce mismatch", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			s.claims["nonce"] = "wrong"
		}},
		{"token rejected", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { s.tokenStatus = 400 }},
		{"provider unavailable", "provider_unavailable", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) { s.tokenStatus = 503 }},
		{"unverified email", "unverified_identity", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			s.claims["email_verified"] = false
		}},
		{"no access", "access_denied", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			m.signInError = identity.ErrAccessDenied
		}},
		{"database failure", "sign_in_failed", func(s *oidcSubstitute, m *googleHTTPModule, c *http.Cookie, q url.Values) {
			m.signInError = errors.New("private-detail")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newOIDCSubstitute(t)
			module := &googleHTTPModule{}
			e := googleHTTP(s, "https://photos.example.com", module)
			cookie, authorization := startGoogle(t, e)
			s.claims["nonce"] = authorization.Get("nonce")
			query := url.Values{"state": {authorization.Get("state")}, "code": {"code"}}
			tc.modify(s, module, cookie, query)
			rec := googleCallback(e, cookie, query)
			require.Equal(t, http.StatusFound, rec.Code)
			require.Equal(t, "/sign-in?error="+tc.want, rec.Header().Get("Location"))
			require.NotContains(t, rec.Body.String(), "private-detail")
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			require.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
			for _, cookie := range rec.Result().Cookies() {
				require.NotEqual(t, identity.CookieName, cookie.Name)
			}
			rec = googleCallback(e, cookie, query)
			require.Equal(t, "/sign-in?error=invalid_state", rec.Header().Get("Location"))
		})
	}
}

func TestGoogleRoutesAreLazyAndRecover(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	s.discoveryStatus.Store(http.StatusServiceUnavailable)
	e := googleHTTP(s, "https://photos.example.com", &googleHTTPModule{})
	require.Zero(t, s.discoveryRequests.Load())
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/identity/google/start", nil))
	require.Equal(t, "/sign-in?error=provider_unavailable", rec.Header().Get("Location"))
	require.Empty(t, rec.Result().Cookies())
	s.discoveryStatus.Store(0)
	_, query := startGoogle(t, e)
	require.NotEmpty(t, query.Get("state"))
}

func TestGoogleRoutesDisabledForFakeAuthentication(t *testing.T) {
	t.Parallel()
	e := echo.New()
	identity.RegisterGoogleRoutes(e, config.NewForTest(), &googleHTTPModule{})
	for _, path := range []string{"/api/identity/google/start", "/api/identity/google/callback"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
	}
}

func TestGoogleConcurrentCallbacksConsumeStateOnce(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	e := googleHTTP(s, "https://photos.example.com", &googleHTTPModule{})
	cookie, authorization := startGoogle(t, e)
	s.claims["nonce"] = authorization.Get("nonce")
	query := url.Values{"state": {authorization.Get("state")}, "code": {"code"}}
	start := make(chan struct{})
	results := make(chan string, 2)
	for range 2 {
		go func() { <-start; results <- googleCallback(e, cookie, query).Header().Get("Location") }()
	}
	close(start)
	require.ElementsMatch(t, []string{"/", "/sign-in?error=invalid_state"}, []string{<-results, <-results})
}

func TestGoogleHTTPReplay(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	e := googleHTTP(s, "https://photos.example.com", &googleHTTPModule{})
	cookie, authorization := startGoogle(t, e)
	s.claims["nonce"] = authorization.Get("nonce")
	query := url.Values{"state": {authorization.Get("state")}, "code": {"code"}}
	rec := googleCallback(e, cookie, query)
	require.Equal(t, "/", rec.Header().Get("Location"))
	rec = googleCallback(e, cookie, query)
	require.Equal(t, "/sign-in?error=invalid_state", rec.Header().Get("Location"))
	for _, cookie := range rec.Result().Cookies() {
		require.NotEqual(t, identity.CookieName, cookie.Name)
	}
}

func TestGoogleHTTPLoginDerivesCallbackFromPublicURL(t *testing.T) {
	t.Parallel()
	for _, publicURL := range []string{"https://photos.example.com", "http://localhost:3579"} {
		t.Run(publicURL, func(t *testing.T) {
			t.Parallel()
			s := newOIDCSubstitute(t)
			e := googleHTTP(s, publicURL, &googleHTTPModule{})
			cookie, query := startGoogle(t, e)
			require.Equal(t, publicURL+"/api/identity/google/callback", query.Get("redirect_uri"))
			require.Equal(t, "openid profile email", query.Get("scope"))
			require.NotEmpty(t, query.Get("state"))
			require.NotEmpty(t, query.Get("nonce"))
			require.NotEmpty(t, query.Get("code_challenge"))
			require.NotEqual(t, cookie.Value, query.Get("state"))
			require.True(t, cookie.HttpOnly)
			require.Equal(t, publicURL == "https://photos.example.com", cookie.Secure)
			require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			require.Empty(t, cookie.Domain)
			require.Equal(t, "/api/identity/google", cookie.Path)
			require.Positive(t, cookie.MaxAge)
			s.claims["nonce"] = query.Get("nonce")
			rec := googleCallback(e, cookie, url.Values{"state": {query.Get("state")}, "code": {"code"}})
			require.Equal(t, http.StatusFound, rec.Code)
			require.Equal(t, "/", rec.Header().Get("Location"))
			cookies := rec.Result().Cookies()
			require.Len(t, cookies, 2)
			require.Equal(t, -1, cookies[0].MaxAge)
			require.Equal(t, identity.CookieName, cookies[1].Name)
			require.Equal(t, "opaque-session", cookies[1].Value)
			require.True(t, cookies[1].HttpOnly)
			require.Equal(t, publicURL == "https://photos.example.com", cookies[1].Secure)
			require.Equal(t, http.SameSiteLaxMode, cookies[1].SameSite)
			require.Equal(t, "/", cookies[1].Path)
			require.Empty(t, cookies[1].Domain)
		})
	}
}
