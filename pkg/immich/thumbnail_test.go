package immich_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThumbnailResponseSafety(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		contentType string
		valid       bool
	}{
		{"image/jpeg; upstream=private-key", true},
		{"application/octet-stream", true},
		{"image/svg+xml", false},
		{"text/html; private-key", false},
		{"private-key", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = fmt.Fprint(w, "image")
			}))
			defer fixture.Close()
			thumbnail, err := immich.New(fixture.URL, "private-key").Thumbnail(t.Context(), "asset")
			if !tc.valid {
				require.Error(t, err)
				assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
				return
			}
			require.NoError(t, err)
			defer thumbnail.Body.Close()
			assert.NotContains(t, thumbnail.ContentType, "private-key")
		})
	}
	t.Run("truncated body", func(t *testing.T) {
		t.Parallel()
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
			_, _ = fmt.Fprint(w, "short")
		}))
		defer fixture.Close()
		thumbnail, err := immich.New(fixture.URL, "private-key").Thumbnail(t.Context(), "asset")
		require.NoError(t, err)
		defer thumbnail.Body.Close()
		_, err = io.ReadAll(thumbnail.Body)
		require.Error(t, err)
		assert.Equal(t, 1, stackCount(err))
		assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
	})
	t.Run("body deadline", func(t *testing.T) {
		t.Parallel()
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer fixture.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		thumbnail, err := immich.New(fixture.URL, "private-key").Thumbnail(ctx, "asset")
		require.NoError(t, err)
		defer thumbnail.Body.Close()
		_, err = io.ReadAll(thumbnail.Body)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Zero(t, stackCount(err))
	})
}

func TestPersonThumbnailResponseSafety(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		contentType string
		valid       bool
	}{
		{"image/jpeg; private-key=redacted", true},
		{"application/octet-stream", true},
		{"image/png", true},
		{"image/svg+xml", false},
		{"text/html; private-key=exposed", false},
		{"private-key", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/people/person%2Fopaque%20%3F%23%25/thumbnail", r.RequestURI)
				assert.Equal(t, "private-key", r.Header.Get("X-Api-Key"))
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = fmt.Fprint(w, "person-image")
			}))
			defer fixture.Close()
			thumbnail, err := immich.New(fixture.URL, "private-key").PersonThumbnail(t.Context(), "person/opaque ?#%")
			if !tc.valid {
				require.Error(t, err)
				assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
				return
			}
			require.NoError(t, err)
			defer thumbnail.Body.Close()
			assert.NotContains(t, thumbnail.ContentType, "private-key")
			data, err := io.ReadAll(thumbnail.Body)
			require.NoError(t, err)
			assert.Equal(t, "person-image", string(data))
		})
	}
}

func TestGeneratedThumbnail(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/assets/asset%2Fopaque%20%3F%23%25/thumbnail?size=thumbnail", r.RequestURI)
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = fmt.Fprint(w, "generated-image")
	}))
	t.Cleanup(fixture.Close)
	thumbnail, err := immich.New(fixture.URL, "read-key").Thumbnail(t.Context(), "asset/opaque ?#%")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, thumbnail.Body.Close()) })
	assert.Equal(t, "image/jpeg", thumbnail.ContentType)
	data, err := io.ReadAll(thumbnail.Body)
	require.NoError(t, err)
	assert.Equal(t, "generated-image", string(data))
}
