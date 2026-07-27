package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesDeclareExplicitSessionPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))
	policies := map[string]string{}
	for _, route := range e.Routes() {
		policies[route.Method+" "+route.Path] = route.Name
	}
	for _, route := range []string{"POST /api/auth/sign-in/request", "POST /api/auth/sign-in/verify"} {
		assert.Equal(t, publicAuthPolicy, policies[route])
	}
	assert.Equal(t, selfReadPolicy, policies["GET /api/sessions"])
	for _, route := range []string{"PATCH /api/sessions/:session_id", "DELETE /api/sessions/:session_id", "POST /api/sessions/sign-out-all", "POST /api/me/email-change/request", "POST /api/me/email-change/complete"} {
		assert.Equal(t, selfMutationPolicy, policies[route], route)
	}
	assert.Equal(t, curatorReadPolicy, policies["GET /api/recipients/:person_id/sessions"])
	for _, route := range []string{"POST /api/recipients/:person_id/email-recovery/request", "POST /api/recipients/:person_id/email-recovery/complete"} {
		assert.Equal(t, curatorMutationPolicy, policies[route], route)
	}
}

func TestClearCookieUsesHostOnlySecureScope(t *testing.T) {
	e := echo.New()
	response := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil), response)
	clearCookie(c)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, setup.CookieName, cookie.Name)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, -1, cookie.MaxAge)
}
