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
		{name: "missing photos chronology cookie", path: "/api/me/photos/chronology"},
		{name: "missing favorites chronology cookie", path: "/api/me/favorites/chronology"},
		{name: "invalid thumbnail session", path: "/api/me/media/" + requestedID + "/thumbnail", cookie: true, err: setup.ErrUnauthenticated},
		{name: "invalid preview session", path: "/api/me/media/" + requestedID + "/preview", cookie: true, err: setup.ErrUnauthenticated},
		{name: "invalid video session", path: "/api/me/media/" + requestedID + "/video", cookie: true, err: setup.ErrUnauthenticated},
		{name: "invalid original session", path: "/api/me/media/" + requestedID + "/original", cookie: true, err: setup.ErrUnauthenticated},
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

func TestChronologyRoutesUseRecipientContentPolicy(t *testing.T) {
	e := libraryHTTP(&routeAuthorizer{})
	routes := map[string]string{}
	for _, route := range e.Routes() {
		routes[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, "policy:recipient_content", routes["GET /api/me/photos/chronology"])
	assert.Equal(t, "policy:recipient_content", routes["GET /api/me/favorites/chronology"])
}

func TestCuratorMediaRoutesUseCuratorPolicy(t *testing.T) {
	e := libraryHTTP(&routeAuthorizer{})
	routes := map[string]string{}
	for _, route := range e.Routes() {
		routes[route.Method+" "+route.Path] = route.Name
	}
	for _, path := range []string{
		"GET /api/curator/media/:id",
		"GET /api/curator/media/:id/thumbnail",
		"GET /api/curator/media/:id/preview",
		"GET /api/curator/media/:id/video",
	} {
		assert.Equal(t, "policy:curator", routes[path], path)
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

func TestDateAnchorCursorIsListingScopedAndContainsNoMediaIdentityOrCount(t *testing.T) {
	encoded := encodeCursor(cursor{Kind: cursorKindPhotos, Sort: "2026-07-27", DateAnchor: true})
	require.NotNil(t, encoded)
	decoded, err := decodeCursor(*encoded, cursorKindPhotos)
	require.NoError(t, err)
	assert.True(t, decoded.DateAnchor)
	assert.Equal(t, "2026-07-27", decoded.Sort)
	assert.Empty(t, decoded.ID)

	payload, err := base64.RawURLEncoding.DecodeString(*encoded)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), `"i"`)
	assert.NotContains(t, string(payload), "count")

	_, err = decodeCursor(*encoded, cursorKindFavorites)
	require.ErrorIs(t, err, ErrInvalidCursor)

	undated := encodeCursor(cursor{Kind: cursorKindFavorites, DateAnchor: true})
	decoded, err = decodeCursor(*undated, cursorKindFavorites)
	require.NoError(t, err)
	assert.True(t, decoded.DateAnchor)
	assert.Empty(t, decoded.Sort)

	invalidDate := encodeCursor(cursor{Kind: cursorKindPhotos, Sort: "2026-02-30", DateAnchor: true})
	_, err = decodeCursor(*invalidDate, cursorKindPhotos)
	require.ErrorIs(t, err, ErrInvalidCursor)

	anchorWithID := encodeCursor(cursor{Kind: cursorKindPhotos, Sort: "2026-07-27", ID: uuid.NewString(), DateAnchor: true})
	_, err = decodeCursor(*anchorWithID, cursorKindPhotos)
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

func TestOriginalDispositionUsesAUsableExtensionForEveryAllowedFormatFamily(t *testing.T) {
	id := uuid.New()
	for _, test := range []struct {
		contentType string
		extension   string
	}{
		{contentType: "image/avif", extension: ".avif"},
		{contentType: "image/gif", extension: ".gif"},
		{contentType: "image/heic", extension: ".heic"},
		{contentType: "image/tiff", extension: ".tiff"},
		{contentType: "video/quicktime", extension: ".mov"},
		{contentType: "video/webm", extension: ".webm"},
		{contentType: "application/octet-stream", extension: ".bin"},
		{contentType: "image/vnd.camera.raw", extension: ".bin"},
		{contentType: "video/vnd.camera.raw", extension: ".bin"},
	} {
		t.Run(test.contentType, func(t *testing.T) {
			assert.Equal(t, "attachment; filename=memento-"+id.String()+test.extension,
				originalDisposition(id, test.contentType))
		})
	}
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
