package comments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authorizerStub struct {
	actor setup.SessionActor
	err   error
}

func (stub authorizerStub) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return stub.actor, stub.err
}

func TestRegisterRoutesExposesCommentPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))
	routes := map[string]string{}
	for _, route := range e.Routes() {
		routes[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, "policy:recipient_content", routes["GET /api/comments/media/:media_id"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["POST /api/comments/media/:media_id"])
	assert.Equal(t, "policy:curator", routes["GET /api/comments/curator"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["PATCH /api/comments/:comment_id"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["DELETE /api/comments/:comment_id"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["PUT /api/comments/media/:media_id/mute"])
	assert.Equal(t, "policy:curator_csrf", routes["POST /api/comments/:comment_id/moderate"])
	assert.Equal(t, "policy:curator", routes["GET /api/comments/:comment_id/moderation-history"])
}

func TestCommentValidationAndAuthorizationErrorsAreSafe(t *testing.T) {
	_, err := normalizeBody(" \n ", 2000)
	require.ErrorIs(t, err, ErrInvalidBody)
	_, err = normalizeBody(strings.Repeat("x", 2001), 2000)
	require.ErrorIs(t, err, ErrInvalidBody)
	body, err := normalizeBody(" hello ", 2000)
	require.NoError(t, err)
	assert.Equal(t, "hello", body)

	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	handler := NewHandler(nil, authorizerStub{err: setup.ErrCSRF})
	RegisterRoutes(e, handler)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/comments/media/not-a-media-id", strings.NewReader(`{"body":"hello"}`))
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "credential"})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.NotContains(t, response.Body.String(), "CSRF token is invalid")
}

func TestCommentMutationContractRequiresCurrentVersion(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/comments/id", nil)
	request.Header.Set("If-Match", "7")
	version, err := commentVersion(e.NewContext(request, httptest.NewRecorder()))
	require.NoError(t, err)
	assert.Equal(t, int64(7), version)

	request.Header.Del("If-Match")
	_, err = commentVersion(e.NewContext(request, httptest.NewRecorder()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "current Comment version")
	assert.Contains(t, interactionError(ErrVersionConflict).Error(), "changed in another browser")
}

func TestCommentListAndCreationContractsRequireBoundedPagesAndRetryKeys(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/comments/curator?limit=100&cursor=opaque", nil)
	page, err := commentPage(e.NewContext(request, httptest.NewRecorder()))
	require.NoError(t, err)
	assert.Equal(t, PageRequest{Cursor: "opaque", Limit: 100}, page)

	request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/comments/curator?limit=101", nil)
	_, err = commentPage(e.NewContext(request, httptest.NewRecorder()))
	require.Error(t, err)

	key := "11111111-1111-4111-8111-111111111111"
	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/comments/media/id", nil)
	request.Header.Set("Idempotency-Key", key)
	parsed, err := idempotencyKey(e.NewContext(request, httptest.NewRecorder()))
	require.NoError(t, err)
	assert.Equal(t, key, parsed.String())
	request.Header.Del("Idempotency-Key")
	_, err = idempotencyKey(e.NewContext(request, httptest.NewRecorder()))
	require.Error(t, err)
}

func TestCommentJobRejectsInvalidPayloadWithoutDatabaseAccess(t *testing.T) {
	service := &Service{}
	err := service.HandleCommentJob(context.Background(), worker.Job{Payload: []byte(`{"activity_id":0}`)})
	require.Error(t, err)
}

func TestInteractionErrorPreservesUnexpectedFailures(t *testing.T) {
	unexpected := errors.New("database unavailable")
	assert.ErrorIs(t, interactionError(unexpected), unexpected)
}
