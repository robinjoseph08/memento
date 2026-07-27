//go:build integration

package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sessionHTTP(t *testing.T, f fixture) *echo.Echo {
	t.Helper()
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(f.service, f.auth))
	return e
}

func TestSessionMutationsRequireSessionBoundCSRFAndJSON(t *testing.T) {
	f := newFixture(t)
	e := sessionHTTP(t, f)
	request := func(contentType, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/sessions/"+f.sessionID.String(), strings.NewReader(`{"label":"Personal laptop"}`))
		req.Header.Set(echo.HeaderContentType, contentType)
		if csrf != "" {
			req.Header.Set(setup.CSRFHeader, csrf)
		}
		req.AddCookie(&http.Cookie{Name: setup.CookieName, Value: f.credential})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, req)
		return response
	}
	assert.Equal(t, http.StatusForbidden, request(echo.MIMEApplicationJSON, "").Code)
	assert.Equal(t, http.StatusUnsupportedMediaType, request(echo.MIMEApplicationForm, f.csrf).Code)
	assert.Equal(t, http.StatusNoContent, request(echo.MIMEApplicationJSON, f.csrf).Code)
	var label string
	require.NoError(t, f.db.NewRaw(`SELECT label FROM sessions WHERE id = ?`, f.sessionID).Scan(context.Background(), &label))
	assert.Equal(t, "Personal laptop", label)
}

func TestSignInVerificationSetsProtectedPublicSessionCookie(t *testing.T) {
	f := newFixture(t)
	e := sessionHTTP(t, f)
	challenge := insertChallenge(t, f, 0x55, "12345678")
	body := `{"challenge_id":"` + challenge + `","code":"12345678","session_type":"public"}`
	verify := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/sign-in/verify", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		response := httptest.NewRecorder()
		e.ServeHTTP(response, req)
		return response
	}
	response := verify()
	require.Equal(t, http.StatusOK, response.Code)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, setup.CookieName, cookie.Name)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Zero(t, cookie.MaxAge, "Public-computer cookie must be nonpersistent")
	assert.True(t, cookie.Expires.IsZero())
	session, err := f.auth.Session(context.Background(), cookie.Value)
	require.NoError(t, err)
	assert.Equal(t, "public", session.SessionType)

	assert.Equal(t, http.StatusBadRequest, verify().Code)
}

func TestSignInHandlerEnforcesConfiguredRateLimit(t *testing.T) {
	f := newFixture(t)
	e := sessionHTTP(t, f)
	for attempt := 1; attempt <= f.service.security.SignInEmailLimit+1; attempt++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/sign-in/request", strings.NewReader(`{"email":"unknown@example.com"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.RemoteAddr = "192.0.2." + string(rune('0'+attempt)) + ":1234"
		response := httptest.NewRecorder()
		e.ServeHTTP(response, req)
		if attempt <= f.service.security.SignInEmailLimit {
			assert.Equal(t, http.StatusAccepted, response.Code)
		} else {
			assert.Equal(t, http.StatusTooManyRequests, response.Code)
		}
	}
}

func TestCuratorSessionAndRecoveryRoutesEnforceRoleAndCSRF(t *testing.T) {
	f := newFixture(t)
	e := sessionHTTP(t, f)
	path := "/api/recipients/" + f.personID.String() + "/sessions"
	request := func(method, target, body, csrf string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
		if body != "" {
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		}
		if csrf != "" {
			req.Header.Set(setup.CSRFHeader, csrf)
		}
		if cookie {
			req.AddCookie(&http.Cookie{Name: setup.CookieName, Value: f.credential})
		}
		response := httptest.NewRecorder()
		e.ServeHTTP(response, req)
		return response
	}
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, path, "", "", false).Code)
	assert.Equal(t, http.StatusNotFound, request(http.MethodGet, path, "", "", true).Code, "non-Curators must not discover privileged routes")
	_, err := f.db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, f.personID).Exec(context.Background())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, request(http.MethodGet, path, "", "", true).Code)
	recoveryPath := "/api/recipients/" + f.personID.String() + "/email-recovery/request"
	body := `{"new_email":"recovered@example.com"}`
	assert.Equal(t, http.StatusForbidden, request(http.MethodPost, recoveryPath, body, "", true).Code)
	assert.Equal(t, http.StatusConflict, request(http.MethodPost, recoveryPath, body, f.csrf, true).Code, "the Curator self-recovery domain guard must run only after role and CSRF authorization")
}
