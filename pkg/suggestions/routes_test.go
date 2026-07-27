package suggestions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesDeclareSeparateRequesterAndCuratorPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))
	expected := map[string]string{
		"GET /api/invitation-suggestions":                     recipientReadPolicy,
		"POST /api/invitation-suggestions":                    recipientMutationPolicy,
		"POST /api/invitation-suggestions/:id/withdraw":       recipientMutationPolicy,
		"GET /api/invitation-suggestions/curator":             curatorReadPolicy,
		"POST /api/invitation-suggestions/curator/:id/reject": curatorMutationPolicy,
		"POST /api/invitation-suggestions/curator/:id/accept": curatorMutationPolicy,
	}
	for _, route := range e.Routes() {
		key := route.Method + " " + route.Path
		if wanted, ok := expected[key]; ok {
			assert.Equal(t, wanted, route.Name)
			delete(expected, key)
		}
	}
	assert.Empty(t, expected)
}

type authorizationCall struct {
	credential string
	csrfToken  string
	mutation   bool
}

type denyingAuthorizer struct {
	recipientError error
	curatorError   error
	recipientCalls []authorizationCall
	curatorCalls   []authorizationCall
}

func (authorizer *denyingAuthorizer) AuthorizeSession(_ context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error) {
	authorizer.recipientCalls = append(authorizer.recipientCalls, authorizationCall{credential: credential, csrfToken: csrfToken, mutation: mutation})
	if errors.Is(authorizer.recipientError, setup.ErrCSRF) && !mutation {
		return setup.SessionActor{}, nil
	}
	return setup.SessionActor{}, authorizer.recipientError
}

func (authorizer *denyingAuthorizer) AuthorizeCurator(_ context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error) {
	authorizer.curatorCalls = append(authorizer.curatorCalls, authorizationCall{credential: credential, csrfToken: csrfToken, mutation: mutation})
	if errors.Is(authorizer.curatorError, setup.ErrCSRF) && !mutation {
		return setup.CuratorSession{}, nil
	}
	return setup.CuratorSession{}, authorizer.curatorError
}

func TestMutationAuthorizationRejectsMissingCSRFBeforeReadingFreeFormData(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	authorizer := &denyingAuthorizer{recipientError: setup.ErrCSRF, curatorError: setup.ErrCSRF}
	RegisterRoutes(e, NewHandler(nil, authorizer))
	tests := []string{
		"/api/invitation-suggestions",
		"/api/invitation-suggestions/11111111-1111-4111-8111-111111111111/withdraw",
		"/api/invitation-suggestions/curator/11111111-1111-4111-8111-111111111111/reject",
		"/api/invitation-suggestions/curator/11111111-1111-4111-8111-111111111111/accept",
	}
	for _, path := range tests {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "credential"})
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		assert.Equal(t, http.StatusForbidden, response.Code, path)
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl), path)
	}
	calls := append(authorizer.recipientCalls, authorizer.curatorCalls...)
	require.Len(t, calls, len(tests))
	for _, call := range calls {
		assert.Equal(t, "credential", call.credential)
		assert.Empty(t, call.csrfToken)
		assert.True(t, call.mutation, "every suggestion mutation must request CSRF authorization")
	}
}

func TestCuratorRouteHidesFromANonCurator(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(nil, &denyingAuthorizer{curatorError: setup.ErrNotCurator}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/invitation-suggestions/curator", nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "recipient-credential"})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.NotContains(t, response.Body.String(), "Curator")
}

func TestNormalizeSubmissionRequiresExplicitSpokenAnswerAndSafeBoundedText(t *testing.T) {
	validFalse := false
	normalized, email, err := normalizeSubmission(SubmitRequest{
		Name: "  Alex  ", Email: "Alex@example.com", RelationshipContext: "  My cousin  ", SpokeWithPerson: &validFalse,
	})
	require.NoError(t, err)
	assert.Equal(t, "Alex", normalized.Name)
	assert.Equal(t, "My cousin", normalized.RelationshipContext)
	assert.Equal(t, "alex@example.com", email)

	_, _, err = normalizeSubmission(SubmitRequest{Name: "Alex", Email: "alex@example.com", RelationshipContext: "Cousin"})
	require.ErrorIs(t, err, ErrInvalidSuggestion)
	_, _, err = normalizeSubmission(SubmitRequest{Name: "Alex", Email: "alex@example.com", RelationshipContext: "Cousin\x00note", SpokeWithPerson: &validFalse})
	require.ErrorIs(t, err, ErrInvalidSuggestion)
}
