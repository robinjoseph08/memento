package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type draftAuthorizer struct {
	actor        setup.CuratorSession
	err          error
	recipientErr error
	mutation     bool
}

func (authorizer *draftAuthorizer) AuthorizeCurator(_ context.Context, _, _ string, mutation bool) (setup.CuratorSession, error) {
	authorizer.mutation = mutation
	return authorizer.actor, authorizer.err
}

func (authorizer *draftAuthorizer) ContextWithRequestMetadata(ctx context.Context, _ *http.Request) context.Context {
	return ctx
}

func (authorizer *draftAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return setup.SessionActor{}, authorizer.recipientErr
}

func draftHTTP(service *Service, authorizer Authorizer) *echo.Echo {
	e := echo.New()
	requestBinder, err := binder.New()
	if err != nil {
		panic(err)
	}
	e.Binder = requestBinder
	RegisterRoutes(e, NewHandler(service, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func draftRequest(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "session"})
	request.Header.Set(setup.CSRFHeader, "csrf")
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestDraftRoutesAreInvisibleToRecipientsBeforePublication(t *testing.T) {
	authorizer := &draftAuthorizer{err: setup.ErrNotCurator}
	e := draftHTTP(nil, authorizer)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/events", ""},
		{http.MethodGet, "/api/events/11111111-1111-4111-8111-111111111111", ""},
		{http.MethodPost, "/api/events", `{}`},
		{http.MethodPut, "/api/events/11111111-1111-4111-8111-111111111111/organization", `{}`},
		{http.MethodPost, "/api/events/11111111-1111-4111-8111-111111111111/publications", `{}`},
		{http.MethodGet, "/api/events/11111111-1111-4111-8111-111111111111/preview-recipients", ""},
		{http.MethodPost, "/api/events/11111111-1111-4111-8111-111111111111/preview?recipient_person_id=22222222-2222-4222-8222-222222222222", ""},
		{http.MethodPost, "/api/withdrawals", `{}`},
		{http.MethodGet, "/api/loose-items/11111111-1111-4111-8111-111111111111", ""},
		{http.MethodPost, "/api/loose-items", `{}`},
		{http.MethodGet, "/api/sources/11111111-1111-4111-8111-111111111111/media-items", ""},
	} {
		response := draftRequest(e, test.method, test.path, test.body)
		assert.Equal(t, http.StatusNotFound, response.Code, "%s %s", test.method, test.path)
		assert.NotContains(t, response.Body.String(), "Event")
		assert.NotContains(t, response.Body.String(), "Media")
	}
}

func TestDraftMutationsRequireCSRFBeforeBindingOrDatabaseAccess(t *testing.T) {
	authorizer := &draftAuthorizer{err: setup.ErrCSRF}
	e := draftHTTP(nil, authorizer)
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/api/events"},
		{http.MethodPut, "/api/events/11111111-1111-4111-8111-111111111111/organization"},
		{http.MethodPost, "/api/events/11111111-1111-4111-8111-111111111111/publications"},
		{http.MethodPost, "/api/events/11111111-1111-4111-8111-111111111111/preview"},
		{http.MethodPost, "/api/withdrawals"},
		{http.MethodPost, "/api/loose-items"},
	} {
		response := draftRequest(e, test.method, test.path, `{}`)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.True(t, authorizer.mutation)
		assert.Contains(t, response.Body.String(), "valid CSRF token")
	}
}

func TestDraftRoutesRequireAValidCuratorSession(t *testing.T) {
	e := draftHTTP(nil, &draftAuthorizer{})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/events/invalid", nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestDraftRequestValidationRejectsMissingFieldsBeforeServiceAccess(t *testing.T) {
	e := draftHTTP(nil, &draftAuthorizer{})
	for _, test := range []struct{ path, body, field string }{
		{"/api/events", `{"timezone":"UTC"}`, "source_album_ids"},
		{"/api/loose-items", `{"timezone":"UTC"}`, "media_item_id"},
	} {
		response := draftRequest(e, http.MethodPost, test.path, test.body)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Contains(t, response.Body.String(), test.field)
	}
}

func TestPublicationErrorsDescribeTheActualReadinessCheck(t *testing.T) {
	for _, test := range []struct {
		err     error
		message string
	}{
		{ErrVersionConflict, "current editable version"},
		{ErrPublicationNotReady, "approve every Moment Audience"},
		{ErrAudienceNotCurrent, "Recipient's access changed"},
		{ErrNoPublication, "Event not found"},
	} {
		mapped := publicationError(test.err)
		require.Error(t, mapped)
		assert.Contains(t, mapped.Error(), test.message)
	}
}

func TestWithdrawalErrorsRequireReasonAndFreshPublicationForRestoration(t *testing.T) {
	for _, test := range []struct {
		err     error
		message string
	}{
		{ErrWithdrawalInvalid, "provide a reason"},
		{ErrAlreadyWithdrawn, "fresh Publication"},
		{ErrNotFound, "Published content not found"},
	} {
		mapped := withdrawalError(test.err)
		require.Error(t, mapped)
		assert.Contains(t, mapped.Error(), test.message)
	}
}

func TestRecipientProjectionRequiresACompletedSessionBeforeServiceAccess(t *testing.T) {
	authorizer := &draftAuthorizer{recipientErr: setup.ErrUnauthenticated}
	e := draftHTTP(nil, authorizer)
	response := draftRequest(e, http.MethodGet, "/api/me/events/11111111-1111-4111-8111-111111111111", "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Contains(t, response.Body.String(), "valid Recipient Session")
}

func TestPreviewRequiresASelectedRecipientBeforeServiceAccess(t *testing.T) {
	e := draftHTTP(nil, &draftAuthorizer{})
	response := draftRequest(e, http.MethodPost, "/api/events/11111111-1111-4111-8111-111111111111/preview", "")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "Choose a current Recipient")
}

func TestDraftErrorsDescribeEmptyAndOversizedSourceSelections(t *testing.T) {
	for _, test := range []struct {
		err     error
		message string
	}{
		{ErrNoMediaAvailable, "Select at least one available Media item"},
		{ErrSourceTooLarge, "too many Media items to list"},
		{ErrVersionConflict, "changed in another browser"},
	} {
		mapped := draftError(test.err, "Source album")
		require.Error(t, mapped)
		assert.Contains(t, mapped.Error(), test.message)
	}
}

func TestDraftRoutesUseNoStoreAndStableNotFoundErrors(t *testing.T) {
	e := draftHTTP(new(Service), &draftAuthorizer{})
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/events/not-an-id", ""},
		{http.MethodPut, "/api/events/not-an-id/organization", `{}`},
		{http.MethodPost, "/api/events/not-an-id/publications", `{}`},
		{http.MethodGet, "/api/events/not-an-id/preview-recipients", ""},
		{http.MethodPost, "/api/events/not-an-id/preview", ""},
		{http.MethodPost, "/api/withdrawals", `{}`},
		{http.MethodGet, "/api/me/events/not-an-id", ""},
		{http.MethodPost, "/api/events", `{}`},
		{http.MethodGet, "/api/loose-items/not-an-id", ""},
		{http.MethodPost, "/api/loose-items", `{}`},
		{http.MethodGet, "/api/sources/not-an-id/media-items", ""},
	} {
		response := draftRequest(e, test.method, test.path, test.body)
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl), "%s %s", test.method, test.path)
	}

	response := draftRequest(e, http.MethodGet, "/api/events/not-an-id", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "Event not found")
}
