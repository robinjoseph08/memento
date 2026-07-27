package visibility

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAuthorizer struct {
	actor    setup.SessionActor
	err      error
	mutation bool
}

func (authorizer *fakeAuthorizer) AuthorizeSession(_ context.Context, _, _ string, mutation bool) (setup.SessionActor, error) {
	authorizer.mutation = mutation
	return authorizer.actor, authorizer.err
}

func visibilityHTTP(authorizer Authorizer) *echo.Echo {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func visibilityRequest(e *echo.Echo, method, path, credential, csrf, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if credential != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestVisibilityRoutesAuthenticateBeforeReadingPrivateState(t *testing.T) {
	for _, path := range []string{
		"/api/visibility-circles", "/api/interest-lists/not-a-recipient", "/api/me/people", "/api/me/interest-list",
	} {
		t.Run(path, func(t *testing.T) {
			authorizer := &fakeAuthorizer{err: setup.ErrUnauthenticated}
			response := visibilityRequest(visibilityHTTP(authorizer), http.MethodGet, path, "invalid", "", "")
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.False(t, authorizer.mutation)
			assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
		})
	}
}

func TestRecipientCannotInspectCircleStructureOrAnotherInterestList(t *testing.T) {
	authorizer := &fakeAuthorizer{actor: setup.SessionActor{Curator: false}}
	for _, path := range []string{"/api/visibility-circles", "/api/interest-lists/not-a-recipient"} {
		response := visibilityRequest(visibilityHTTP(authorizer), http.MethodGet, path, "recipient-session", "", "")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.NotContains(t, response.Body.String(), "circle")
	}
}

func TestEveryVisibilityAndInterestMutationRequiresCSRF(t *testing.T) {
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/visibility-circles"},
		{http.MethodPatch, "/api/visibility-circles/not-an-id"},
		{http.MethodPost, "/api/visibility-circles/not-an-id/archive"},
		{http.MethodPut, "/api/visibility-circles/not-an-id/members/not-a-person"},
		{http.MethodPut, "/api/interest-lists/not-a-recipient/people/not-a-person"},
		{http.MethodPut, "/api/me/interest-list/not-a-person"},
	} {
		t.Run(route.path, func(t *testing.T) {
			authorizer := &fakeAuthorizer{err: setup.ErrCSRF}
			response := visibilityRequest(visibilityHTTP(authorizer), route.method, route.path, "session", "wrong", `{}`)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.True(t, authorizer.mutation)
		})
	}
}

func TestVisibilityRoutePoliciesCannotBeMistakenForContentAuthority(t *testing.T) {
	e := visibilityHTTP(&fakeAuthorizer{})
	policies := map[string]bool{}
	for _, route := range e.Routes() {
		policies[route.Name] = true
		assert.NotContains(t, route.Name, "media")
		assert.NotContains(t, route.Name, "search")
		assert.NotContains(t, route.Name, "comment")
		assert.NotContains(t, route.Name, "archive_download")
		assert.NotContains(t, route.Name, "notification")
	}
	require.True(t, policies[curatorReadPolicy])
	require.True(t, policies[curatorMutationPolicy])
	require.True(t, policies[recipientDiscoveryPolicy])
	require.True(t, policies[recipientInterestPolicy])
	require.True(t, policies[recipientMutationPolicy])
}
