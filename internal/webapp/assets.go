package webapp

import (
	"bytes"
	"embed"
	"errors"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// embedded contains the Vite production build. The tracked placeholder keeps
// normal Go builds valid before the frontend has been built.
//
//go:embed all:dist
var embedded embed.FS

const (
	titleTag        = "<title>Memento</title>"
	defaultCardPath = "/og-default-v2.png"
)

// PageMetadata describes the HTML metadata for a public browser route.
type PageMetadata struct {
	Title       string
	Description string
}

// MetadataResolver identifies public routes that are safe to describe before
// the browser has authenticated. Returning false leaves the generic app shell
// in place.
type MetadataResolver func(*http.Request) (PageMetadata, bool)

// Handler returns the production frontend handler when a Vite build is
// embedded. Development uses Vite directly, so a source-only build has no
// frontend handler.
func Handler(publicURL string, resolve MetadataResolver) (http.Handler, bool, error) {
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
	handler, err := newHandler(root, publicURL, resolve)
	if err != nil {
		return nil, false, errorstack.Capture(err)
	}
	return handler, true, nil
}

type spaHandler struct {
	files       fs.FS
	fileServer  http.Handler
	index       []byte
	publicURL   string
	resolveMeta MetadataResolver
}

func newHandler(files fs.FS, publicURL string, resolve MetadataResolver) (http.Handler, error) {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(index, []byte(titleTag)) {
		return nil, errors.New("index.html: missing Memento title placeholder")
	}
	return &spaHandler{
		files:       files,
		fileServer:  http.FileServer(http.FS(files)),
		index:       index,
		publicURL:   strings.TrimRight(publicURL, "/"),
		resolveMeta: resolve,
	}, nil
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
		if strings.HasPrefix(requested, "assets/") || requested == strings.TrimPrefix(defaultCardPath, "/") {
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
	index := h.index
	if h.resolveMeta != nil {
		if metadata, ok := h.resolveMeta(request); ok {
			index = h.withMetadata(request, metadata)
		}
	}
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Content-Length", strconv.Itoa(len(index)))
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if request.Method != http.MethodHead {
		_, _ = response.Write(index)
	}
}

func (h *spaHandler) withMetadata(request *http.Request, metadata PageMetadata) []byte {
	pageTitle := metadata.Title
	if pageTitle != "" {
		pageTitle += " | Memento"
	} else {
		pageTitle = "Memento"
	}
	canonical := &url.URL{Path: path.Clean(request.URL.Path)}
	pageURL := h.publicURL + canonical.EscapedPath()
	imageURL := h.publicURL + defaultCardPath
	escape := html.EscapeString
	tags := `<title>` + escape(pageTitle) + `</title>
    <meta name="description" content="` + escape(metadata.Description) + `" />
    <link rel="canonical" href="` + escape(pageURL) + `" />
    <meta property="og:site_name" content="Memento" />
    <meta property="og:type" content="website" />
    <meta property="og:title" content="` + escape(pageTitle) + `" />
    <meta property="og:description" content="` + escape(metadata.Description) + `" />
    <meta property="og:url" content="` + escape(pageURL) + `" />
    <meta property="og:image" content="` + escape(imageURL) + `" />
    <meta property="og:image:width" content="1200" />
    <meta property="og:image:height" content="630" />
    <meta property="og:image:alt" content="Memento" />
    <meta name="twitter:card" content="summary_large_image" />
    <meta name="twitter:title" content="` + escape(pageTitle) + `" />
    <meta name="twitter:description" content="` + escape(metadata.Description) + `" />
    <meta name="twitter:image" content="` + escape(imageURL) + `" />`
	return bytes.Replace(h.index, []byte(titleTag), []byte(tags), 1)
}
