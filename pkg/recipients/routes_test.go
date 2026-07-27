package recipients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
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

func TestRegisteredTokenRoutesPreventCachingAndReferrerLeakage(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	auth := setup.New(nil, nil, config.SecurityConfig{})
	RegisterRoutes(e, NewHandler(New(nil, nil, "https://memento.example"), auth))
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/auth/invitations/inspect", ""},
		{http.MethodPost, "/api/auth/invitations/accept", `{"token":"invalid"}`},
	}
	for _, test := range tests {
		request := httptest.NewRequestWithContext(context.Background(), test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		}
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		require.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		assert.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
		assert.Contains(t, response.Body.String(), `"code":"not_found"`)
		assert.NotContains(t, response.Body.String(), "Recipient")
	}
}
