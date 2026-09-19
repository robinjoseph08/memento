package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/internal/webapp"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/stretchr/testify/require"
)

func TestCompressionSkipsEveryMediaRoute(t *testing.T) {
	t.Parallel()
	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	passthrough := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	media.RegisterRoutes(e, nil, passthrough, passthrough)
	media.RegisterViewerRoutes(e, nil, nil, passthrough, passthrough)
	media.RegisterSignedRoutes(e, nil, nil, "", passthrough)
	// Exercise the production route inventory through the server middleware,
	// with a fixed byte source so this test isolates transport behavior.
	paths := map[string]bool{"/api/media": true, "/api/media/future/stream": true}
	for _, route := range e.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/media/") {
			paths[route.Path] = true
		}
	}
	for path := range paths {
		e.Match([]string{http.MethodGet, http.MethodHead}, path, echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
			w.Header().Set("ETag", `"version"`)
			http.ServeContent(w, r, "clip.mp4", time.Time{}, strings.NewReader("0123456789abcdef"))
		})))
	}
	for path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			path := strings.NewReplacer(":id", "entry", ":personID", "person", ":sourceID", "source").Replace(path)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, byteRange := range []string{"", "bytes=4-7", "bytes=99-"} {
					request := httptest.NewRequest(method, path+"?v=version", nil)
					request.Header.Set("Range", byteRange)
					plain := httptest.NewRecorder()
					srv.Handler.ServeHTTP(plain, request)
					request.Header.Set("Accept-Encoding", "gzip")
					compressed := httptest.NewRecorder()
					srv.Handler.ServeHTTP(compressed, request)
					require.Equal(t, plain.Code, compressed.Code)
					require.Equal(t, plain.Body.String(), compressed.Body.String())
					require.Empty(t, compressed.Header().Get("Content-Encoding"))
					require.Equal(t, plain.Header(), compressed.Header())
					if method == http.MethodHead && compressed.Code < http.StatusBadRequest {
						require.Empty(t, compressed.Body.String())
					}
				}
			}
		})
	}
}

func TestEmbeddedFrontendCompression(t *testing.T) {
	t.Parallel()
	frontend, available, err := webapp.Handler("https://photos.example.test", publicPageMetadata)
	require.NoError(t, err)
	if !available {
		t.Skip("run mise build:web to test embedded production assets")
	}
	srv, err := newServer(config.NewForTest(), frontend)
	require.NoError(t, err)
	index := compressionRequest(srv.Handler, "/", "")
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^\"]+\.(?:js|css))"`).FindAllStringSubmatch(index.Body.String(), -1)
	require.GreaterOrEqual(t, len(assets), 2, "the production shell must link JS and CSS")
	paths := []string{"/", "/index.html", "/sign-in", "/albums/example"}
	for _, asset := range assets {
		paths = append(paths, asset[1])
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			plain := compressionRequest(srv.Handler, path, "")
			compressed := compressionRequest(srv.Handler, path, "gzip")
			require.Equal(t, http.StatusOK, compressed.Code)
			require.Equal(t, "gzip", compressed.Header().Get("Content-Encoding"))
			require.Contains(t, compressed.Header().Values("Vary"), "Accept-Encoding")
			result := compressed.Result()
			defer result.Body.Close()
			require.Empty(t, result.Header.Get("Content-Length"), "the original byte length must not survive compression")
			require.Equal(t, plain.Body.Bytes(), decodedBody(t, compressed))
			cache := "no-cache"
			if strings.HasPrefix(path, "/assets/") {
				cache = "public, max-age=31536000, immutable"
			} else {
				require.Equal(t, "frame-ancestors 'none'", compressed.Header().Get("Content-Security-Policy"))
				require.Equal(t, "no-referrer", compressed.Header().Get("Referrer-Policy"))
			}
			require.Equal(t, cache, plain.Header().Get("Cache-Control"))
			require.Equal(t, cache, compressed.Header().Get("Cache-Control"))
		})
	}
}

func TestJSONCompressionPreservesResponses(t *testing.T) {
	t.Parallel()
	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.GET("/api/example", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"title": "Summer photos"})
	})
	e.GET("/api/failure", func(_ *echo.Context) error { return echo.ErrForbidden })
	for _, path := range []string{"/api/example", "/health", "/api/missing", "/api/failure"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			plain := compressionRequest(srv.Handler, path, "")
			compressed := compressionRequest(srv.Handler, path, "gzip")
			require.Equal(t, plain.Code, compressed.Code)
			require.Contains(t, compressed.Header().Values("Vary"), "Accept-Encoding")
			require.Empty(t, plain.Header().Get("Content-Encoding"))
			if plain.Code == http.StatusOK {
				require.Equal(t, "gzip", compressed.Header().Get("Content-Encoding"))
			}
			require.Equal(t, plain.Body.Bytes(), decodedBody(t, compressed))
			for _, header := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options"} {
				require.Equal(t, plain.Header().Get(header), compressed.Header().Get(header))
			}
			require.Empty(t, compressed.Header().Get("Content-Security-Policy"))
			require.Empty(t, compressed.Header().Get("Referrer-Policy"))
			unsupported := compressionRequest(srv.Handler, path, "br")
			require.Empty(t, unsupported.Header().Get("Content-Encoding"))
			require.Equal(t, plain.Body.Bytes(), unsupported.Body.Bytes())
		})
	}
}

func compressionRequest(handler http.Handler, path, encoding string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Accept-Encoding", encoding)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodedBody(t *testing.T, response *httptest.ResponseRecorder) []byte {
	t.Helper()
	if response.Header().Get("Content-Encoding") == "" {
		return response.Body.Bytes()
	}
	require.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	return body
}
