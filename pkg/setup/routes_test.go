package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesDeclareExplicitSetupAndSessionPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil))

	policies := make(map[string]string)
	for _, route := range e.Routes() {
		policies[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, setupOnlyPolicy, policies["GET /api/setup"])
	assert.Equal(t, setupOnlyPolicy, policies["POST /api/setup/code"])
	assert.Equal(t, setupOnlyPolicy, policies["POST /api/setup/verify"])
	assert.Equal(t, setupOnlyPolicy, policies["POST /api/setup/complete"])
	assert.Equal(t, sessionPolicy, policies["GET /api/session"])
	assert.Equal(t, sessionMutationPolicy, policies["POST /api/session/logout"])
}

func TestSessionCookiesUseHostPrefixAndSecureScope(t *testing.T) {
	for _, test := range []struct {
		name        string
		sessionType string
		persistent  bool
	}{
		{name: "trusted device", sessionType: "trusted", persistent: true},
		{name: "public computer", sessionType: "public", persistent: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := echo.New()
			response := httptest.NewRecorder()
			echoContext := e.NewContext(httptest.NewRequestWithContext(context.Background(), "GET", "/", nil), response)
			setSessionCookie(echoContext, completedSession{Credential: "opaque", SessionType: test.sessionType})

			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, CookieName, cookie.Name)
			assert.Equal(t, "/", cookie.Path)
			assert.Empty(t, cookie.Domain)
			assert.True(t, cookie.Secure)
			assert.True(t, cookie.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.Equal(t, test.persistent, cookie.MaxAge > 0)
		})
	}
}
