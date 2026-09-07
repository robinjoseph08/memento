package immich_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionDiagnostic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, user string
		status              int
		usable              bool
		message             string
	}{
		{"healthy", `{"major":2,"minor":7,"patch":0,"future":"ignored"}`, `{"id":"owner"}`, 200, true, ""},
		{"invalid key", `{"major":2,"minor":7,"patch":0}`, `secret-key`, 401, false, "API key"},
		{"permission denied", `{"major":2,"minor":7,"patch":0}`, `secret-key`, 403, false, "permission"},
		{"outage", `{"major":2,"minor":7,"patch":0}`, `secret-key`, 503, false, "unavailable"},
		{"malformed version", `{"secret":"secret-key"}`, `{"id":"owner"}`, 200, false, "version"},
		{"malformed identity", `{"major":2,"minor":7,"patch":0}`, `{}`, 200, false, "account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "secret-key", r.Header.Get("X-Api-Key"))
				switch r.URL.Path {
				case "/base/api/server/version":
					_, _ = fmt.Fprint(w, tc.version)
				case "/base/api/users/me":
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.user)
				default:
					http.NotFound(w, r)
				}
			}))
			defer fixture.Close()
			result := immich.New(fixture.URL+"/base", "secret-key").Check(t.Context())
			assert.Equal(t, tc.usable, result.Usable)
			assert.Contains(t, result.Message, tc.message)
			assert.NotContains(t, result.Message, "secret-key")
			if tc.usable {
				assert.Equal(t, "2.7.0", result.Version)
			}
		})
	}
}

func TestDiagnosticCancellationAndRedirect(t *testing.T) {
	t.Parallel()
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	t.Cleanup(source.Close)
	result := immich.New(source.URL, "private-key").Check(t.Context())
	assert.False(t, result.Usable)
	assert.False(t, reached, "redirects must not forward the API key")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result = immich.New(source.URL, "private-key").Check(ctx)
	require.False(t, result.Usable)
	assert.NotContains(t, result.Message, "private-key")
}
