package visibility

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
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

func (authorizer *fakeAuthorizer) AuthorizeInterestSession(_ context.Context, _, _ string, mutation bool) (setup.SessionActor, error) {
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

func TestPeopleSearchAuthenticatesBeforeReadingOrValidatingTheBody(t *testing.T) {
	authorizer := &fakeAuthorizer{err: setup.ErrUnauthenticated}
	response := visibilityRequest(
		visibilityHTTP(authorizer),
		http.MethodPost,
		"/api/me/people/search",
		"invalid",
		"",
		`{"query":`,
	)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.False(t, authorizer.mutation, "directory search is a private read and does not require CSRF")
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
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

func TestMutationBodiesRequireExplicitBooleanIntent(t *testing.T) {
	authorizer := &fakeAuthorizer{actor: setup.SessionActor{Curator: true}}
	for _, test := range []struct {
		path string
		body string
	}{
		{"/api/visibility-circles/11111111-1111-4111-8111-111111111111/members/22222222-2222-4222-8222-222222222222", `{"version":1}`},
		{"/api/interest-lists/11111111-1111-4111-8111-111111111111/people/22222222-2222-4222-8222-222222222222", `{}`},
	} {
		response := visibilityRequest(visibilityHTTP(authorizer), http.MethodPut, test.path, "session", "csrf", test.body)
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Contains(t, response.Body.String(), "required")
	}
}

func TestPeopleSearchBurstLimitUsesAccessAndClientIPWithoutEchoingTheQuery(t *testing.T) {
	limiter := newDirectorySearchLimiter()
	limiter.limit = 1
	firstAccess := uuid.New()
	secondAccess := uuid.New()
	require.True(t, limiter.allow(firstAccess, "192.0.2.1"))
	assert.False(t, limiter.allow(firstAccess, "192.0.2.2"), "one access generation is limited across IPs")
	assert.False(t, limiter.allow(secondAccess, "192.0.2.1"), "one IP is limited across access generations")

	authorizer := &fakeAuthorizer{actor: setup.SessionActor{AccessID: firstAccess}}
	handler := NewHandler(nil, authorizer)
	handler.limiter.limit = 0
	e := echo.New()
	RegisterRoutes(e, handler)
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	response := visibilityRequest(e, http.MethodPost, "/api/me/people/search", "session", "", `{"query":"private guessed name"}`)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.NotContains(t, response.Body.String(), "private guessed name")
	assert.False(t, authorizer.mutation)
}

func TestPeopleSearchRequestBoundsPrivateInput(t *testing.T) {
	request, err := normalizePeopleSearchRequest(PeopleSearchRequest{Query: "  José  "})
	require.NoError(t, err)
	assert.Equal(t, "José", request.Query)
	assert.Equal(t, 25, request.Limit)
	assert.Equal(t, `100\%\_\\`, escapeLike(`100%_\`))

	_, err = normalizePeopleSearchRequest(PeopleSearchRequest{Query: strings.Repeat("a", 201)})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = normalizePeopleSearchRequest(PeopleSearchRequest{Limit: 51})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = normalizePeopleSearchRequest(PeopleSearchRequest{Cursor: strings.Repeat("a", 2049)})
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestVisibilityRoutePoliciesCannotBeMistakenForContentAuthority(t *testing.T) {
	e := visibilityHTTP(&fakeAuthorizer{})
	expected := map[string]string{
		"GET /api/visibility-circles":                             curatorReadPolicy,
		"POST /api/visibility-circles":                            curatorMutationPolicy,
		"PATCH /api/visibility-circles/:id":                       curatorMutationPolicy,
		"POST /api/visibility-circles/:id/archive":                curatorMutationPolicy,
		"PUT /api/visibility-circles/:id/members/:person_id":      curatorMutationPolicy,
		"GET /api/interest-lists/:recipient_id/discoverable":      curatorReadPolicy,
		"GET /api/interest-lists/:recipient_id":                   curatorReadPolicy,
		"PUT /api/interest-lists/:recipient_id/people/:person_id": curatorMutationPolicy,
		"GET /api/me/people":                                      recipientDiscoveryPolicy,
		"POST /api/me/people/search":                              recipientDiscoveryPolicy,
		"GET /api/me/interest-list":                               recipientInterestPolicy,
		"PUT /api/me/interest-list/:person_id":                    recipientMutationPolicy,
	}
	for _, route := range e.Routes() {
		key := route.Method + " " + route.Path
		policy, ok := expected[key]
		if !ok {
			continue
		}
		assert.Equal(t, policy, route.Name, key)
		delete(expected, key)
	}
	assert.Empty(t, expected, "every Visibility route must have an explicit non-content policy")
}
