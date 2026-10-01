package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codeHTTP(t *testing.T, module *identity.Module) (*echo.Echo, *config.Config) {
	t.Helper()
	e := echo.New()
	b, err := binder.New()
	require.NoError(t, err)
	e.Binder = b
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	cfg := config.NewForTest()
	identity.RegisterRoutes(e, cfg, module)
	return e, cfg
}

func postJSON(e *echo.Echo, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &value), recorder.Body.String())
	return value
}

func TestSignInCodeHTTP(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	recorder := &notifications.Recorder{}
	module.Sender = recorder
	e, cfg := codeHTTP(t, module)

	status := httptest.NewRecorder()
	e.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/identity/status", nil))
	assert.True(t, decode[identity.Status](t, status).SignInCodes)

	rec := postJSON(e, "/api/identity/sign-in-code", `{"email":"not an address"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, decode[errcodes.ErrorResponse](t, rec).Error.Fields, "email")

	rec = postJSON(e, "/api/identity/sign-in-code", `{"email":"owner@example.test"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Len(t, recorder.Sent(), 1)
	code := sixDigits.FindString(recorder.Sent()[0].Subject)

	rec = postJSON(e, "/api/identity/sign-in-code/verify", `{"email":"owner@example.test","code":"000000x"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, decode[errcodes.ErrorResponse](t, rec).Error.Fields, "code")

	// The first verified address claims the Installation and gets a session
	// labeled by its browser.
	rec = postJSON(e, "/api/identity/sign-in-code/verify", `{"email":"owner@example.test","code":"`+code[:3]+` `+code[3:]+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	result := decode[identity.SignInCodeResult](t, rec)
	assert.Equal(t, "signed_in", result.Outcome)
	require.NotNil(t, result.Person)
	assert.True(t, result.Person.IsCurator)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, cfg.CookieNamespace+"_session", cookies[0].Name)
	sessions, err := module.Sessions(t.Context(), cookies[0].Value)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "Safari on iPhone", sessions[0].Device)

	// An unknown address is asked for a name, then its request is recorded.
	rec = postJSON(e, "/api/identity/sign-in-code", `{"email":"stranger@example.test"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	code = sixDigits.FindString(recorder.Sent()[1].Subject)
	rec = postJSON(e, "/api/identity/sign-in-code/verify", `{"email":"stranger@example.test","code":"`+code+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, identity.SignInCodeResult{Outcome: "name_required"}, decode[identity.SignInCodeResult](t, rec))
	assert.Empty(t, rec.Result().Cookies())
	rec = postJSON(e, "/api/identity/sign-in-code/verify", `{"email":"stranger@example.test","code":"`+code+`","display_name":"Jordan"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, identity.SignInCodeResult{Outcome: "requested"}, decode[identity.SignInCodeResult](t, rec))
	assert.Empty(t, rec.Result().Cookies())

	// A mail failure is a server error with the page's copy.
	recorder.BeforeSend = func(context.Context, notifications.Message) error {
		return &notifications.DeliveryError{Outcome: notifications.OutcomeTransient, Summary: "The mail server could not be reached.", Cause: errors.New("dial refused")}
	}
	rec = postJSON(e, "/api/identity/sign-in-code", `{"email":"someone@example.test"}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	failure := decode[errcodes.ErrorResponse](t, rec).Error
	assert.Equal(t, "sign_in_code_unsent", failure.Code)
	assert.Equal(t, "The sign-in code email could not be sent.", failure.Message)

	// Without mail the page hides the code form and the route refuses.
	module.Sender = nil
	status = httptest.NewRecorder()
	e.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/identity/status", nil))
	assert.False(t, decode[identity.Status](t, status).SignInCodes)
	rec = postJSON(e, "/api/identity/sign-in-code", `{"email":"owner@example.test"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}
