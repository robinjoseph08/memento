package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAuthorizer struct {
	err        error
	credential string
	csrf       string
	mutation   bool
}

func (authorizer *fakeAuthorizer) AuthorizeCurator(_ context.Context, credential, csrf string, mutation bool) (setup.CuratorSession, error) {
	authorizer.credential = credential
	authorizer.csrf = csrf
	authorizer.mutation = mutation
	return setup.CuratorSession{}, authorizer.err
}

type failingConnector struct{}

func (failingConnector) Check(context.Context) error { return errors.New("private URL and API key") }
func (failingConnector) OwnedAlbums(context.Context) ([]immich.AlbumSummary, error) {
	panic("owned album discovery must not run after failed validation")
}

func sourceHTTP(service *Service, authorizer Authorizer) *echo.Echo {
	e := echo.New()
	RegisterRoutes(e, NewHandler(service, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func sourceRequest(e *echo.Echo, method, path string, session, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if session != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: session})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestSourceRoutesRequireCuratorSessionBeforeAccessingService(t *testing.T) {
	e := sourceHTTP(nil, &fakeAuthorizer{})
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/sources"},
		{http.MethodGet, "/api/sources/not-an-id"},
		{http.MethodPost, "/api/sources/discover"},
		{http.MethodPost, "/api/sources/not-an-id/ignore"},
		{http.MethodPost, "/api/sources/not-an-id/restore"},
	} {
		response := sourceRequest(e, test.method, test.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, response.Code, "%s %s", test.method, test.path)
		assert.NotContains(t, response.Body.String(), "Immich")
	}
}

func TestSourceMutationsRequireSessionBoundCSRF(t *testing.T) {
	authorizer := &fakeAuthorizer{err: setup.ErrCSRF}
	e := sourceHTTP(nil, authorizer)
	response := sourceRequest(e, http.MethodPost, "/api/sources/discover", "opaque-session", "wrong-csrf")
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, "opaque-session", authorizer.credential)
	assert.Equal(t, "wrong-csrf", authorizer.csrf)
	assert.True(t, authorizer.mutation)
	assert.NotContains(t, response.Body.String(), "opaque-session")
	assert.NotContains(t, response.Body.String(), "wrong-csrf")
}

func TestSourceReadsDoNotRequireCSRFButStillRequireCuratorRole(t *testing.T) {
	authorizer := &fakeAuthorizer{err: setup.ErrUnauthenticated}
	e := sourceHTTP(nil, authorizer)
	response := sourceRequest(e, http.MethodGet, "/api/sources", "recipient-session", "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.False(t, authorizer.mutation)
	assert.NotContains(t, response.Body.String(), "recipient-session")
}

func TestDiscoveryDependencyFailureReturnsOnlySafeDiagnostics(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	service := New(nil, failingConnector{})
	e := sourceHTTP(service, authorizer)
	response := sourceRequest(e, http.MethodPost, "/api/sources/discover", "opaque-session", "csrf")
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	serialized := response.Body.String()
	assert.Contains(t, serialized, "Immich could not be validated")
	assert.NotContains(t, serialized, "private")
}

func TestSourceListRejectsInvalidPaginationBeforeDatabaseAccess(t *testing.T) {
	e := sourceHTTP(nil, &fakeAuthorizer{})
	for path, message := range map[string]string{
		"/api/sources?page=0":     "Page must be a positive number.",
		"/api/sources?limit=101":  "Limit must be between 1 and 100.",
		"/api/sources?limit=word": "Limit must be between 1 and 100.",
	} {
		response := sourceRequest(e, http.MethodGet, path, "session", "")
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Contains(t, response.Body.String(), message)
	}
}
