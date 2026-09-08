package webapp

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIndex = "<html><head><title>Memento</title></head><body>app</body></html>"

func TestHandlerRequiresTitlePlaceholder(t *testing.T) {
	t.Parallel()

	_, err := newHandler(fstest.MapFS{
		"index.html": {Data: []byte("<html>app</html>")},
	}, "https://photos.example.test", nil)
	assert.EqualError(t, err, "index.html: missing Memento title placeholder")
}

func TestHandlerServesBuiltAsset(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "console.log('app')", recorder.Body.String())
	assert.Equal(t, "public, max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
}

func TestHandlerServesVersionedCardImageImmutably(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, defaultCardPath, nil))

		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "public, max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
		if method == http.MethodGet {
			assert.Equal(t, "image", recorder.Body.String())
		} else {
			assert.Empty(t, recorder.Body.String())
		}
	}
}

func TestHandlerFallsBackToIndexForClientRoute(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings/profile", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, testIndex, recorder.Body.String())
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}

func TestHandlerFallsBackToIndexForDottedClientRoute(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, nil)
	request := httptest.NewRequest(http.MethodGet, "/users/jane.doe", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, testIndex, recorder.Body.String())
}

func TestHandlerRendersMetadataForPublicRoute(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, func(request *http.Request) (PageMetadata, bool) {
		if path.Clean(request.URL.Path) != "/sign-in" {
			return PageMetadata{}, false
		}
		return PageMetadata{
			Title:       "Sign in",
			Description: "Sign in to see shared photos.",
		}, true
	})
	request := httptest.NewRequest(http.MethodGet, "/%73ign-in/?return=/albums", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "<title>Sign in | Memento</title>")
	assert.Contains(t, recorder.Body.String(), `property="og:title" content="Sign in | Memento"`)
	assert.Contains(t, recorder.Body.String(), `property="og:description" content="Sign in to see shared photos."`)
	assert.Contains(t, recorder.Body.String(), `property="og:url" content="https://photos.example.test/sign-in"`)
	assert.Contains(t, recorder.Body.String(), `property="og:image" content="https://photos.example.test/og-default-v2.png"`)
	assert.Contains(t, recorder.Body.String(), `name="twitter:card" content="summary_large_image"`)
	assert.NotContains(t, recorder.Body.String(), "return=/albums")
}

func TestHandlerDoesNotDescribePrivateRoute(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, func(_ *http.Request) (PageMetadata, bool) {
		return PageMetadata{}, false
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/curator/people/person-id", nil))

	assert.Equal(t, testIndex, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "og:title")
}

func TestHandlerEscapesMetadata(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, func(_ *http.Request) (PageMetadata, bool) {
		return PageMetadata{
			Title:       `<script>alert("title")</script>`,
			Description: `photos "for" <friends>`,
		}, true
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/invite/token", nil))

	assert.NotContains(t, recorder.Body.String(), "<script>")
	assert.NotContains(t, recorder.Body.String(), "<friends>")
	assert.Contains(t, recorder.Body.String(), "&lt;script&gt;")
	assert.Contains(t, recorder.Body.String(), "&#34;for&#34;")
}

func TestHandlerReturnsNoIndexBodyForHead(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, func(_ *http.Request) (PageMetadata, bool) {
		return PageMetadata{Title: "Sign in"}, true
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/sign-in", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Body.String())
	assert.NotEmpty(t, recorder.Header().Get("Content-Length"))
	assert.Equal(t, "text/html; charset=utf-8", recorder.Header().Get("Content-Type"))
}

func TestHandlerReturnsNotFoundForMissingAsset(t *testing.T) {
	t.Parallel()

	handler := testHandler(t, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), testIndex)
}

func testHandler(t *testing.T, resolve MetadataResolver) http.Handler {
	t.Helper()
	handler, err := newHandler(testFiles(), "https://photos.example.test", resolve)
	require.NoError(t, err)
	return handler
}

func testFiles() fs.FS {
	return fstest.MapFS{
		"index.html":        {Data: []byte(testIndex)},
		"og-default-v2.png": {Data: []byte("image")},
		"assets/app.js":     {Data: []byte("console.log('app')")},
	}
}
