package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	onboarded    = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	phoneSession = identity.Session{Person: identity.Person{ID: "person", DisplayName: "Alex", OnboardingCompletedAt: &onboarded}, Token: strings.Repeat("t", 43), ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
)

func TestMobileReturnRedirectsToTheAppWithACode(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		appEnv   string
		returnTo string
		status   int
		location string
	}{
		"store app":                  {"production", "memento://sign-in", 302, "memento://sign-in?code=" + strings.Repeat("c", 43)},
		"return link with a query":   {"production", "memento://sign-in?from=web", 302, "memento://sign-in?from=web&code=" + strings.Repeat("c", 43)},
		"Expo Go in development":     {"development", "exp://192.168.1.20:8081/--/sign-in", 302, "exp://192.168.1.20:8081/--/sign-in?code=" + strings.Repeat("c", 43)},
		"Expo Go in production":      {"production", "exp://192.168.1.20:8081/--/sign-in", 400, ""},
		"a website":                  {"development", "https://evil.test/steal", 400, ""},
		"a fragment that would hide": {"production", "memento://sign-in#code=", 400, ""},
		"nothing":                    {"production", "", 400, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config.NewForTest()
			cfg.AppEnv = test.appEnv
			module := &fakeIdentity{claimed: true, expectedToken: "browser-token", session: phoneSession}
			e := identityHTTP(t, cfg, module)
			req := httptest.NewRequest(http.MethodGet, "/api/identity/mobile/return?return_to="+strings.ReplaceAll(test.returnTo, "#", "%23"), nil)
			req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: "browser-token"})
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			require.Equal(t, test.status, recorder.Code, recorder.Body.String())
			assert.Equal(t, test.location, recorder.Header().Get("Location"))
			if test.status == 400 {
				assert.Empty(t, module.issuedFor, "no code is minted for a link that will not reach the app")
				assert.Contains(t, recorder.Body.String(), "invalid_return")
				return
			}
			// The module ended the session behind the cookie; the cookie goes too.
			cookies := recorder.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, -1, cookies[0].MaxAge)
		})
	}
}

func TestMobileReturnRequiresASignedInBrowser(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	e := identityHTTP(t, cfg, &fakeIdentity{claimed: true, expectedToken: "browser-token", session: phoneSession})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/identity/mobile/return?return_to=memento://sign-in", nil))
	assert.Equal(t, 401, recorder.Code)
}

func TestMobileReturnSendsAnUnfinishedPersonToOnboardingFirst(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	session := phoneSession
	session.Person.OnboardingCompletedAt = nil
	module := &fakeIdentity{claimed: true, expectedToken: "browser-token", session: session}
	e := identityHTTP(t, cfg, module)
	req := httptest.NewRequest(http.MethodGet, "/api/identity/mobile/return?return_to=memento://sign-in", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: "browser-token"})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 302, recorder.Code, recorder.Body.String())
	assert.Equal(t, "/welcome?return_to=memento%3A%2F%2Fsign-in", recorder.Header().Get("Location"))
	assert.Empty(t, module.issuedFor, "no code before Onboarding")
}

func TestMobileExchangeAnswersWithABearerSessionAndNoCookie(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	module := &fakeIdentity{claimed: true, session: phoneSession}
	e := identityHTTP(t, cfg, module)
	req := httptest.NewRequest(http.MethodPost, "/api/identity/mobile/exchange", strings.NewReader(`{"code":"`+strings.Repeat("c", 43)+`","platform":" iPhone "}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	assert.Empty(t, recorder.Result().Cookies())
	assert.Equal(t, [2]string{strings.Repeat("c", 43), "iPhone"}, module.exchanged)
	var body identity.MobileSession
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, phoneSession.Token, body.Token)
	assert.Equal(t, phoneSession.Person, body.Person)

	for name, test := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"a used or expired code": {`{"code":"` + strings.Repeat("x", 43) + `","platform":"iPhone"}`, 401, "invalid_code"},
		"nothing to exchange":    {`{"code":"","platform":""}`, 422, "validation_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := identityHTTP(t, cfg, &fakeIdentity{claimed: true, session: phoneSession})
			req := httptest.NewRequest(http.MethodPost, "/api/identity/mobile/exchange", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			assert.Equal(t, test.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), test.code)
		})
	}
}

func TestBearerSessionsNeverTouchCookies(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	module := &fakeIdentity{claimed: true, expectedToken: phoneSession.Token, session: phoneSession}
	module.session.Person.IsCurator = true
	module.session.Renewed = true
	e := identityHTTP(t, cfg, module)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+phoneSession.Token)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 204, recorder.Code, recorder.Body.String())
	assert.Empty(t, recorder.Result().Cookies(), "a renewed bearer session sets no cookie")

	// The bearer credential decides, even when a valid cookie rides along.
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 43))
	req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: phoneSession.Token})
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 401, recorder.Code)
	assert.Empty(t, recorder.Result().Cookies(), "a refused bearer token does not clear the browser's cookie")

	req = httptest.NewRequest(http.MethodGet, "/api/identity/status", nil)
	req.Header.Set("Authorization", "Bearer "+phoneSession.Token)
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code)
	var status identity.Status
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	require.NotNil(t, status.Person)
	assert.Equal(t, "Alex", status.Person.DisplayName)

	req = httptest.NewRequest(http.MethodPost, "/api/identity/sign-out", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+phoneSession.Token)
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 204, recorder.Code)
	assert.Equal(t, phoneSession.Token, module.signedOut)
	assert.Empty(t, recorder.Result().Cookies())

	// Handlers that act as the Person receive the bearer token, not an empty cookie.
	req = httptest.NewRequest(http.MethodGet, "/api/identity/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+phoneSession.Token)
	req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: "stale-browser-token"})
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	assert.Equal(t, phoneSession.Token, module.listedFor)
}
