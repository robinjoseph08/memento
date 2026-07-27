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

	expected := map[string]string{
		http.MethodGet + " /api/recipients/:person_id":                     curatorReadPolicy,
		http.MethodPost + " /api/recipients/:person_id/designate":          curatorMutationPolicy,
		http.MethodPost + " /api/recipients/:person_id/invitation/send":    curatorMutationPolicy,
		http.MethodPost + " /api/recipients/:person_id/invitation/revoke":  curatorMutationPolicy,
		http.MethodPost + " /api/recipients/:person_id/invitation/reissue": curatorMutationPolicy,
		http.MethodPost + " /api/recipients/:person_id/invitation/remind":  curatorMutationPolicy,
		http.MethodGet + " /api/auth/invitations/inspect":                  invitationInspectPolicy,
		http.MethodPost + " /api/auth/invitations/accept":                  invitationAcceptPolicy,
		http.MethodGet + " /api/onboarding":                                onboardingReadPolicy,
		http.MethodPatch + " /api/onboarding":                              onboardingMutationPolicy,
		http.MethodPost + " /api/onboarding/complete":                      onboardingMutationPolicy,
	}
	for _, route := range e.Routes() {
		key := route.Method + " " + route.Path
		want, ok := expected[key]
		if !ok {
			continue
		}
		assert.Equal(t, want, route.Name, key)
		delete(expected, key)
	}
	assert.Empty(t, expected)
}

func TestRegisteredTokenRoutesPreventCachingAndReferrerLeakage(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	auth := setup.New(nil, nil, config.SecurityConfig{})
	RegisterRoutes(e, NewHandler(New(nil, nil, "https://memento.example", auth), auth))
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
