package immich_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var readOperations = map[string]func(context.Context, *immich.Client) error{
	"version": func(ctx context.Context, c *immich.Client) error { return c.CheckImport(ctx) },
	"albums":  func(ctx context.Context, c *immich.Client) error { _, err := c.ListAlbums(ctx); return err },
	"album":   func(ctx context.Context, c *immich.Client) error { _, err := c.GetAlbum(ctx, "id"); return err },
	"members": func(ctx context.Context, c *immich.Client) error {
		_, _, err := c.ListMembers(ctx, "id", 1)
		return err
	},
	"asset": func(ctx context.Context, c *immich.Client) error { _, err := c.GetAsset(ctx, "id"); return err },
	"thumbnail": func(ctx context.Context, c *immich.Client) error {
		media, err := c.Thumbnail(ctx, "id")
		if err == nil {
			defer media.Body.Close()
			_, err = io.Copy(io.Discard, media.Body)
		}
		return err
	},
}

func stackCount(err error) int {
	count := 0
	for err != nil {
		if _, ok := err.(interface{ StackTrace() pkgerrors.StackTrace }); ok {
			count++
		}
		err = errors.Unwrap(err)
	}
	return count
}

func TestReadErrorsAreSafeAndActionable(t *testing.T) {
	t.Parallel()
	for name, read := range readOperations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				status int
				code   string
				stacks int
			}{
				{401, "immich_unauthorized", 0}, {403, "immich_permission_denied", 0}, {404, "not_found", 0}, {500, "immich_unavailable", 1},
			} {
				t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
					t.Parallel()
					fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(tc.status)
						_, _ = fmt.Fprint(w, "private-key /secret-path/password")
					}))
					defer fixture.Close()
					err := read(t.Context(), immich.New(fixture.URL, "private-key"))
					require.Error(t, err)
					var coded *errcodes.Error
					require.ErrorAs(t, err, &coded)
					assert.Equal(t, tc.code, coded.Code)
					assert.Equal(t, tc.stacks, stackCount(err))
					assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
					assert.NotContains(t, fmt.Sprintf("%+v", err), "/secret-path/password")
					if tc.status == 403 && name != "version" {
						scope := map[string]string{"albums": "album.read", "album": "album.read", "members": "asset.read and album.read", "asset": "asset.read", "thumbnail": "asset.view"}[name]
						assert.Contains(t, err.Error(), scope)
					}
				})
			}
		})
	}
}

func TestReadsNeverFollowRedirects(t *testing.T) {
	t.Parallel()
	for name, read := range readOperations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var reached atomic.Bool
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/assets/id/original" {
					reached.Store(true)
					return
				}
				target := destination.URL + "/private-key"
				if name == "thumbnail" {
					target = "/api/assets/id/original"
				}
				http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			err := read(t.Context(), immich.New(source.URL, "private-key"))
			require.Error(t, err)
			assert.False(t, reached.Load())
			assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
		})
	}
}

func TestJSONResponseBoundaries(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"malformed":            `[{"id":"private-key"`,
		"trailing JSON":        `[] {"private-key":true}`,
		"trailing junk":        `[] private-key`,
		"oversized JSON":       `[{"id":"large","albumName":"` + strings.Repeat("a", 9<<20) + `"}]`,
		"oversized whitespace": `[]` + strings.Repeat(" ", 9<<20),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer fixture.Close()
			_, err := immich.New(fixture.URL, "private-key").ListAlbums(t.Context())
			require.Error(t, err)
			assert.Equal(t, 1, stackCount(err))
			assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
		})
	}
}

func TestReadCancellationAndDeadlines(t *testing.T) {
	t.Parallel()
	for name, read := range readOperations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
			defer fixture.Close()
			client := immich.New(fixture.URL, "private-key")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := read(ctx, client)
			require.ErrorIs(t, err, context.Canceled)
			assert.Zero(t, stackCount(err))
			ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			err = read(ctx, client)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Zero(t, stackCount(err))
		})
	}
}

func TestClientHasOwnTimeout(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(fixture.Close)
	started := time.Now()
	_, err := immich.New(fixture.URL, "private-key").ListAlbums(t.Context())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, stackCount(err))
	assert.Less(t, time.Since(started), 7*time.Second)
	assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
}

func TestInvalidConfigurationAndNetworkErrorsAreSanitized(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"https://private-key@localhost", "http://localhost/?private-key", "http://localhost/#private-key", "file:///private-key", "http://%private-key", "http://127.0.0.1:0/private-key", "http://localhost/?", "http://localhost/#"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			_, err := immich.New(address, "private-key").ListAlbums(t.Context())
			require.Error(t, err)
			assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
		})
	}
}

func TestEmptyURLQueryAndFragmentDoNotReachImmich(t *testing.T) {
	t.Parallel()
	var reached atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); _, _ = fmt.Fprint(w, "[]") }))
	t.Cleanup(fixture.Close)
	for _, suffix := range []string{"?", "#"} {
		_, err := immich.New(fixture.URL+suffix, "key").ListAlbums(t.Context())
		require.Error(t, err)
	}
	assert.False(t, reached.Load())
}

func TestInvalidReadParametersDoNotReachImmich(t *testing.T) {
	t.Parallel()
	var reached atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); _, _ = fmt.Fprint(w, albumJSON) }))
	t.Cleanup(fixture.Close)
	client := immich.New(fixture.URL, "key")
	_, err := client.GetAlbum(t.Context(), "")
	require.Error(t, err)
	_, err = client.GetAsset(t.Context(), "")
	require.Error(t, err)
	_, err = client.Thumbnail(t.Context(), "")
	require.Error(t, err)
	_, _, err = client.ListMembers(t.Context(), "", 1)
	require.Error(t, err)
	_, _, err = client.ListMembers(t.Context(), "id", 0)
	require.Error(t, err)
	_, _, err = client.ListMembers(t.Context(), "id", -1)
	require.Error(t, err)
	assert.False(t, reached.Load())
}
