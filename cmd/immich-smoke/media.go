package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

func checkMediaEndpoint(ctx context.Context, handler http.Handler, path string) error {
	request := func(method, etag string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, method, path, nil)
		req.Header.Set("If-None-Match", etag)
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	image := request(http.MethodGet, "")
	if image.Code != http.StatusOK || !strings.HasPrefix(http.DetectContentType(image.Body.Bytes()), "image/") || !strings.HasPrefix(image.Header().Get("Content-Type"), "image/") {
		return fmt.Errorf("production media GET did not serve generated image: HTTP %d", image.Code)
	}
	etag := image.Header().Get("ETag")
	const cache = "private, max-age=31536000, immutable"
	if etag == "" || image.Header().Get("Cache-Control") != cache {
		return fmt.Errorf("production media response is not privately content-versioned")
	}
	head := request(http.MethodHead, "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag {
		return fmt.Errorf("production media HEAD differs from GET")
	}
	cached := request(http.MethodGet, etag)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 || cached.Header().Get("Cache-Control") != cache {
		return fmt.Errorf("production media conditional GET did not validate cached thumbnail")
	}
	return nil
}
