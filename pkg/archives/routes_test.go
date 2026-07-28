package archives

import (
	"context"
	"errors"
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

type routeAuthorizer struct {
	actor    setup.SessionActor
	err      error
	mutation bool
}

func (authorizer *routeAuthorizer) AuthorizeSession(_ context.Context, _, _ string, mutation bool) (setup.SessionActor, error) {
	authorizer.mutation = mutation
	return authorizer.actor, authorizer.err
}

func archiveHTTP(authorizer *routeAuthorizer) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(New(nil, nil), authorizer))
	return e
}

func TestArchiveRoutesRequireSessionAndCSRFWithoutTokenHints(t *testing.T) {
	token := strings.Repeat("private-token", 4)
	for _, test := range []struct {
		name     string
		method   string
		path     string
		body     string
		cookie   bool
		authErr  error
		status   int
		mutation bool
	}{
		{name: "part missing Session", method: http.MethodGet, path: "/api/me/archives/parts/1?token=" + token, status: http.StatusUnauthorized},
		{name: "part invalid Session", method: http.MethodGet, path: "/api/me/archives/parts/1?token=" + token, cookie: true, authErr: setup.ErrUnauthenticated, status: http.StatusUnauthorized},
		{name: "plan missing CSRF", method: http.MethodPost, path: "/api/me/archives", body: `{"scope":"event","event_id":"` + uuid.NewString() + `","media_ids":[]}`, cookie: true, authErr: setup.ErrCSRF, status: http.StatusForbidden, mutation: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &routeAuthorizer{err: test.authErr}
			request := httptest.NewRequestWithContext(context.Background(), test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			}
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
			}
			response := httptest.NewRecorder()
			archiveHTTP(authorizer).ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code)
			assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl))
			assert.NotContains(t, response.Body.String(), token)
			assert.Equal(t, test.mutation, authorizer.mutation)
		})
	}
}

func TestArchiveErrorsRemainNonEnumerating(t *testing.T) {
	require.Error(t, archiveError(ErrInvalidSelection))
	require.Error(t, archiveError(ErrNotFound))
	require.Error(t, archiveError(ErrUnavailable))
	require.NoError(t, archiveError(nil))
	unexpected := errors.New("database unavailable")
	require.ErrorIs(t, archiveError(unexpected), unexpected)
	_, err := decodeToken("not-a-token")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestArchiveNamesNeverContainPathSegments(t *testing.T) {
	assert.Equal(t, "Family-Weekend", safeArchiveName(" ../Family / Weekend\\ "))
	assert.Equal(t, "Memento-Event", safeArchiveName("../../"))
	assert.Equal(t, "Family.zip", partFilename("Family", 1, 1))
	assert.Equal(t, "Family-part-2-of-3.zip", partFilename("Family", 2, 3))
}
