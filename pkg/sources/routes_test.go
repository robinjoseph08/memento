package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
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
func (failingConnector) Album(context.Context, uuid.UUID) (immich.AlbumSummary, error) {
	return immich.AlbumSummary{}, errors.New("private URL and API key")
}
func (failingConnector) AlbumAssetsPage(context.Context, uuid.UUID, int) (immich.AssetPage, error) {
	return immich.AssetPage{}, errors.New("private URL and API key")
}
func (failingConnector) AssetExists(context.Context, uuid.UUID) (bool, error) {
	return false, errors.New("private URL and API key")
}

func sourceHTTP(service *Service, authorizer Authorizer) *echo.Echo {
	e := echo.New()
	RegisterRoutes(e, NewHandler(service, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func sourceRequest(e *echo.Echo, method, path string, session, csrf string) *httptest.ResponseRecorder {
	return sourceVersionedRequest(e, method, path, session, csrf, "")
}

func sourceVersionedRequest(e *echo.Echo, method, path string, session, csrf, version string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if session != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: session})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	if version != "" {
		request.Header.Set("If-Match", version)
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
		{http.MethodPost, "/api/sources/not-an-id/reconcile"},
	} {
		response := sourceRequest(e, test.method, test.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, response.Code, "%s %s", test.method, test.path)
		assert.NotContains(t, response.Body.String(), "Immich")
	}
}

func TestSourceMutationsRequireSessionBoundCSRF(t *testing.T) {
	for _, path := range []string{
		"/api/sources/discover",
		"/api/sources/11111111-1111-4111-8111-111111111111/ignore",
		"/api/sources/11111111-1111-4111-8111-111111111111/restore",
		"/api/sources/11111111-1111-4111-8111-111111111111/reconcile",
	} {
		t.Run(path, func(t *testing.T) {
			authorizer := &fakeAuthorizer{err: setup.ErrCSRF}
			e := sourceHTTP(nil, authorizer)
			response := sourceRequest(e, http.MethodPost, path, "opaque-session", "wrong-csrf")
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Equal(t, "opaque-session", authorizer.credential)
			assert.Equal(t, "wrong-csrf", authorizer.csrf)
			assert.True(t, authorizer.mutation)
			assert.NotContains(t, response.Body.String(), "opaque-session")
			assert.NotContains(t, response.Body.String(), "wrong-csrf")
		})
	}
}

func TestSourceReadsDoNotRequireCSRFButStillRequireCuratorRole(t *testing.T) {
	authorizer := &fakeAuthorizer{err: setup.ErrUnauthenticated}
	e := sourceHTTP(nil, authorizer)
	response := sourceRequest(e, http.MethodGet, "/api/sources", "recipient-session", "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.False(t, authorizer.mutation)
	assert.NotContains(t, response.Body.String(), "recipient-session")

	authorizer.err = setup.ErrNotCurator
	response = sourceRequest(e, http.MethodGet, "/api/sources", "recipient-session", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.NotContains(t, response.Body.String(), "Source")
}

func TestDiscoveryDependencyFailureReturnsOnlySafeDiagnostics(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	service := New(nil, failingConnector{}, 10*time.Minute)
	e := sourceHTTP(service, authorizer)
	response := sourceRequest(e, http.MethodPost, "/api/sources/discover", "opaque-session", "csrf")
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	serialized := response.Body.String()
	assert.Contains(t, serialized, "Immich is temporarily unavailable")
	assert.NotContains(t, serialized, "private")
}

func TestSourceListRejectsInvalidFiltersAndPaginationBeforeDatabaseAccess(t *testing.T) {
	e := sourceHTTP(nil, &fakeAuthorizer{})
	for path, message := range map[string]string{
		"/api/sources?disposition=private": "Disposition must be unreviewed, ignored, or drafted.",
		"/api/sources?cursor=not-a-cursor": "Cursor is invalid.",
		"/api/sources?limit=101":           "Limit must be between 1 and 100.",
		"/api/sources?limit=word":          "Limit must be between 1 and 100.",
	} {
		response := sourceRequest(e, http.MethodGet, path, "session", "")
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Contains(t, response.Body.String(), message)
	}
}

func TestReconciliationJobsRejectInvalidPayloadPermanently(t *testing.T) {
	service := &Service{}
	err := service.HandleReconciliationJob(context.Background(), worker.Job{Payload: []byte(`{"source_album_id":"not-an-id"}`)})
	require.EqualError(t, err, "invalid_source_reconciliation_payload")
}

func TestSourceTriageRequiresOptimisticVersion(t *testing.T) {
	e := sourceHTTP(nil, &fakeAuthorizer{})
	response := sourceRequest(e, http.MethodPost, "/api/sources/11111111-1111-4111-8111-111111111111/ignore", "session", "csrf")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "If-Match")
}
