package webapp

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestHandlerServesBuiltAsset(t *testing.T) {
	t.Parallel()

	handler := newHandler(testFiles())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "console.log('app')", recorder.Body.String())
	assert.Equal(t, "public, max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
}

func TestHandlerFallsBackToIndexForClientRoute(t *testing.T) {
	t.Parallel()

	handler := newHandler(testFiles())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings/profile", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "<html>app</html>", recorder.Body.String())
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}

func TestHandlerFallsBackToIndexForDottedClientRoute(t *testing.T) {
	t.Parallel()

	handler := newHandler(testFiles())
	request := httptest.NewRequest(http.MethodGet, "/users/jane.doe", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "<html>app</html>", recorder.Body.String())
}

func TestHandlerReturnsNotFoundForMissingAsset(t *testing.T) {
	t.Parallel()

	handler := newHandler(testFiles())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "<html>app</html>")
}

func testFiles() fs.FS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
}
