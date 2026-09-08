package immich_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionDiagnostic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, albums string
		status                int
		usable, supported     bool
		message               string
	}{
		{"healthy", `{"major":3,"minor":1,"patch":0,"prerelease":null,"future":"ignored"}`, `[]`, 200, true, true, "album"},
		{"unsupported but connected", `{"major":2,"minor":7,"patch":0,"prerelease":null}`, `[]`, 200, true, false, "3.0.x"},
		{"invalid key", `{"major":3,"minor":1,"patch":0,"prerelease":null}`, `secret-key`, 401, false, true, "API key"},
		{"permission denied", `{"major":3,"minor":1,"patch":0,"prerelease":null}`, `secret-key`, 403, false, true, "album.read"},
		{"outage", `{"major":3,"minor":1,"patch":0,"prerelease":null}`, `secret-key`, 503, false, true, "unavailable"},
		{"malformed version", `{"secret":"secret-key"}`, `[]`, 200, false, false, "version"},
		{"malformed albums", `{"major":3,"minor":1,"patch":0,"prerelease":null}`, `{}`, 200, false, true, "unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "secret-key", r.Header.Get("X-Api-Key"))
				switch r.URL.Path {
				case "/base/api/server/version":
					_, _ = fmt.Fprint(w, tc.version)
				case "/base/api/albums":
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.albums)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer fixture.Close()
			result := immich.New(fixture.URL+"/base", "secret-key").Check(t.Context())
			assert.Equal(t, tc.usable, result.Usable)
			if result.ImportSupported != nil {
				assert.Equal(t, tc.supported, *result.ImportSupported)
			} else {
				assert.Contains(t, tc.name, "malformed version")
			}
			assert.Contains(t, result.Message, tc.message)
			assert.NotContains(t, result.Message, "secret-key")
		})
	}
}

func TestImportVersionGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version   string
		supported bool
	}{
		{`{"major":3,"minor":0,"patch":0,"prerelease":null}`, true},
		{`{"major":3,"minor":0,"patch":3,"prerelease":null}`, true},
		{`{"major":3,"minor":1,"patch":19,"prerelease":null}`, true},
		{`{"major":3,"minor":2,"patch":0,"prerelease":null}`, false},
		{`{"major":2,"minor":7,"patch":5,"prerelease":null}`, false},
		{`{"major":4,"minor":0,"patch":0,"prerelease":null}`, false},
		{`{"major":3,"minor":1,"patch":0,"prerelease":1}`, false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/server/version", r.URL.Path)
				_, _ = fmt.Fprint(w, tc.version)
			}))
			defer fixture.Close()
			err := immich.New(fixture.URL, "secret-key").CheckImport(t.Context())
			if tc.supported {
				require.NoError(t, err)
			} else {
				var coded *errcodes.Error
				require.ErrorAs(t, err, &coded)
				assert.Equal(t, "immich_unsupported_version", coded.Code)
				assert.Contains(t, err.Error(), "3.0.x and 3.1.x")
			}
		})
	}
}

func TestDiagnosticCancellationAndRedirect(t *testing.T) {
	t.Parallel()
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	t.Cleanup(source.Close)
	result := immich.New(source.URL, "private-key").Check(t.Context())
	assert.False(t, result.Usable)
	assert.False(t, reached.Load(), "redirects must not forward the API key")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result = immich.New(source.URL, "private-key").Check(ctx)
	require.False(t, result.Usable)
	assert.NotContains(t, result.Message, "private-key")
}
