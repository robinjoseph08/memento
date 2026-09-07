package webapp

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// embedded contains the Vite production build. The tracked placeholder keeps
// normal Go builds valid before the frontend has been built.
//
//go:embed all:dist
var embedded embed.FS

// Handler returns the production frontend handler when a Vite build is
// embedded. Development uses Vite directly, so a source-only build has no
// frontend handler.
func Handler() (http.Handler, bool, error) {
	root, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, false, errorstack.Capture(err)
	}
	if _, err := fs.Stat(root, "index.html"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, errorstack.Capture(err)
	}
	return newHandler(root), true, nil
}

type spaHandler struct {
	files      fs.FS
	fileServer http.Handler
}

func newHandler(files fs.FS) http.Handler {
	return &spaHandler{
		files:      files,
		fileServer: http.FileServer(http.FS(files)),
	}
}

func (h *spaHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	requested := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if requested == "." || requested == "" {
		h.serveIndex(response, request)
		return
	}
	if !fs.ValidPath(requested) {
		http.NotFound(response, request)
		return
	}

	info, err := fs.Stat(h.files, requested)
	if err == nil && !info.IsDir() {
		if strings.HasPrefix(requested, "assets/") {
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		h.fileServer.ServeHTTP(response, request)
		return
	}
	if err == nil && info.IsDir() {
		if _, indexErr := fs.Stat(h.files, path.Join(requested, "index.html")); indexErr == nil {
			h.fileServer.ServeHTTP(response, request)
			return
		}
	}

	if requested == "assets" || strings.HasPrefix(requested, "assets/") {
		http.NotFound(response, request)
		return
	}
	if path.Ext(requested) != "" && !strings.Contains(request.Header.Get("Accept"), "text/html") {
		http.NotFound(response, request)
		return
	}
	h.serveIndex(response, request)
}

func (h *spaHandler) serveIndex(response http.ResponseWriter, request *http.Request) {
	cloned := request.Clone(request.Context())
	cloned.URL.Path = "/"
	response.Header().Set("Cache-Control", "no-cache")
	h.fileServer.ServeHTTP(response, cloned)
}
