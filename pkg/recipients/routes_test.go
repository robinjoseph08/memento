package recipients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvitationRoutesUseSafeMethodsAndExplicitPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))

	expected := map[string]struct {
		method string
		name   string
	}{
		"/api/recipients/:person_id":                    {http.MethodGet, curatorReadPolicy},
		"/api/recipients/:person_id/designate":          {http.MethodPost, curatorMutationPolicy},
		"/api/recipients/:person_id/invitation/send":    {http.MethodPost, curatorMutationPolicy},
		"/api/recipients/:person_id/invitation/revoke":  {http.MethodPost, curatorMutationPolicy},
		"/api/recipients/:person_id/invitation/reissue": {http.MethodPost, curatorMutationPolicy},
		"/api/recipients/:person_id/invitation/remind":  {http.MethodPost, curatorMutationPolicy},
		"/api/auth/invitations/inspect":                 {http.MethodGet, invitationInspectPolicy},
		"/api/auth/invitations/accept":                  {http.MethodPost, invitationAcceptPolicy},
	}
	for _, route := range e.Routes() {
		want, ok := expected[route.Path]
		if !ok {
			continue
		}
		assert.Equal(t, want.method, route.Method, route.Path)
		assert.Equal(t, want.name, route.Name, route.Path)
		delete(expected, route.Path)
	}
	assert.Empty(t, expected)
}

func TestTokenHeadersPreventCachingAndReferrerLeakage(t *testing.T) {
	e := echo.New()
	e.GET("/token", tokenHeaders(func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/token", nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
}
