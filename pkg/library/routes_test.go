package library

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
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

func libraryHTTP(authorizer *routeAuthorizer) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(New(nil, nil), authorizer))
	return e
}

func TestLibraryRoutesRequireACompletedRecipientSessionWithoutIdentifierHints(t *testing.T) {
	requestedID := uuid.NewString()
	for _, test := range []struct {
		name   string
		path   string
		cookie bool
		err    error
	}{
		{name: "missing cookie", path: "/api/me/photos"},
		{name: "invalid session", path: "/api/me/media/" + requestedID + "/thumbnail", cookie: true, err: setup.ErrUnauthenticated},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &routeAuthorizer{err: test.err}
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, test.path, nil)
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
			}
			response := httptest.NewRecorder()
			libraryHTTP(authorizer).ServeHTTP(response, request)
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl))
			assert.NotContains(t, response.Body.String(), requestedID)
		})
	}
}

func TestLibraryMutationRequiresCSRFAndInvalidIDsAreNotFound(t *testing.T) {
	authorizer := &routeAuthorizer{actor: setup.SessionActor{PersonID: uuid.New(), AccessID: uuid.New()}, err: setup.ErrCSRF}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/me/new-for-you/"+uuid.NewString()+"/seen", nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	response := httptest.NewRecorder()
	libraryHTTP(authorizer).ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	assert.True(t, authorizer.mutation)

	authorizer.err = nil
	request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/me/events/not-an-id", nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	response = httptest.NewRecorder()
	libraryHTTP(authorizer).ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestLibraryCursorSupportsUndatedMediaAndRejectsOtherCollectionsAndTrailingData(t *testing.T) {
	id := uuid.NewString()
	encoded := encodeCursor(cursor{Kind: cursorKindPhotos, ID: id})
	require.NotNil(t, encoded)
	decoded, err := decodeCursor(*encoded, cursorKindPhotos)
	require.NoError(t, err)
	assert.Equal(t, id, decoded.ID)
	assert.Empty(t, decoded.Sort)

	_, err = decodeCursor(*encoded, cursorKindFavorites)
	require.ErrorIs(t, err, ErrInvalidCursor)

	trailing := base64.RawURLEncoding.EncodeToString([]byte(`{"k":"photos","s":"date","i":"` + id + `"} {}`))
	_, err = decodeCursor(trailing, cursorKindPhotos)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestEventMediaCursorCarriesOnlyAuthorizedResourceAndPublicationContext(t *testing.T) {
	mediaID, eventID, publicationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	encoded := encodeCursor(cursor{
		Kind: cursorKindEventMedia, ID: mediaID,
		ResourceID: eventID, PublicationID: publicationID,
	})
	require.NotNil(t, encoded)
	decoded, err := decodeCursor(*encoded, cursorKindEventMedia)
	require.NoError(t, err)
	assert.Equal(t, mediaID, decoded.ID)
	assert.Equal(t, eventID, decoded.ResourceID)
	assert.Equal(t, publicationID, decoded.PublicationID)
	assert.Empty(t, decoded.Sort, "Event pagination must not expose source placement ordinals")

	withOrdinal := encodeCursor(cursor{
		Kind: cursorKindEventMedia, Sort: "17", ID: mediaID,
		ResourceID: eventID, PublicationID: publicationID,
	})
	_, err = decodeCursor(*withOrdinal, cursorKindEventMedia)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestLibraryErrorMappingKeepsCursorAndContentFailuresDistinct(t *testing.T) {
	validation := libraryError(ErrInvalidCursor)
	notFound := libraryError(ErrNotFound)
	require.Error(t, validation)
	require.Error(t, notFound)
	assert.NotEqual(t, validation, notFound)
	require.NoError(t, libraryError(nil))
	unexpected := errors.New("database unavailable")
	assert.ErrorIs(t, libraryError(unexpected), unexpected)
}
