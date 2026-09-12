package publishing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
)

type publicationUseCases struct {
	publishing.PublicationUseCases
	published bool
}

func (f *publicationUseCases) PublishAlbum(_ context.Context, id string, request publishing.PublishRequest) (publishing.AlbumDetail, error) {
	if request.ReviewToken == "stale" {
		return publishing.AlbumDetail{}, &errcodes.Error{HTTPCode: 409, Code: "publication_changed", Message: "Review the Album again."}
	}
	f.published = true
	return publishing.AlbumDetail{ID: id, Published: true}, nil
}
func TestPublicationHTTPRequiresCuratorAndReturnsStructuredValidation(t *testing.T) {
	t.Parallel()
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	b, err := binder.New()
	require.NoError(t, err)
	e.Binder = b
	module := &publicationUseCases{}
	allowed := false
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !allowed {
				return echo.ErrForbidden
			}
			return next(c)
		}
	}
	publishing.RegisterPublicationRoutes(e, module, guard)
	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(r, req)
		return r
	}
	for _, action := range []string{"publish", "unpublish", "delete"} {
		require.Equal(t, 403, post("/api/curator/albums/album/"+action, `{}`).Code)
	}
	allowed = true
	missing := post("/api/curator/albums/album/publish", `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, missing.Code)
	require.Contains(t, missing.Body.String(), `"review_token":"Review this Album before publishing."`)
	require.False(t, module.published)
	require.Equal(t, 409, post("/api/curator/albums/album/publish", `{"review_token":"stale"}`).Code)
	require.False(t, module.published)
	require.Equal(t, 200, post("/api/curator/albums/album/publish", `{"review_token":"reviewed"}`).Code)
	require.True(t, module.published)
	missing = post("/api/curator/albums/album/delete", `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, missing.Code)
	require.Contains(t, missing.Body.String(), `"title":"Type the Album title exactly to confirm deletion."`)
}

type viewerUseCases struct {
	publishing.ViewerUseCases
	actor   string
	preview string
	album   string
}

func (f *viewerUseCases) ViewAlbum(_ context.Context, actor, preview, album string) (publishing.ViewerAlbum, error) {
	f.actor, f.preview, f.album = actor, preview, album
	return publishing.ViewerAlbum{ID: album, Title: "Shared", Days: []publishing.ViewerDay{}}, nil
}
func TestViewerHTTPUsesOnlyAuthenticatedIdentityAndExplicitCuratorPreview(t *testing.T) {
	t.Parallel()
	module := &viewerUseCases{}
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("identity.person_id", "signed-in"); return next(c) }
	}
	curator := false
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return person(func(c *echo.Context) error {
			if !curator {
				return echo.ErrForbidden
			}
			return next(c)
		})
	}
	publishing.RegisterViewerRoutes(e, module, person, guard)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		e.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	response := get("/api/albums/album?person=forged&preview=forged")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "signed-in", module.actor)
	require.Empty(t, module.preview)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	for _, field := range []string{"moments", "source_id", "access", "faces"} {
		require.NotContains(t, payload, field)
	}
	require.Equal(t, 403, get("/api/curator/albums/album/preview/alex").Code)
	curator = true
	require.Equal(t, 200, get("/api/curator/albums/album/preview/alex").Code)
	require.Equal(t, "signed-in", module.actor)
	require.Equal(t, "alex", module.preview)
	require.Equal(t, 404, get("/api/curator/albums/album/preview/alex/download").Code)
}
