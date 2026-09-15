package fixture_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/stretchr/testify/require"
)

func TestLegacyProbeUploadsWithDeviceIdentity(t *testing.T) {
	t.Parallel()
	var uploaded atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/server/version":
			_, _ = fmt.Fprint(w, `{"major":2,"minor":7,"patch":5}`)
		case "/api/auth/login":
			_, _ = fmt.Fprint(w, `{"accessToken":"fixture-session"}`)
		case "/api/auth/admin-sign-up", "/api/admin/users":
			_, _ = fmt.Fprint(w, `{}`)
		case "/api/assets":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			if r.FormValue("deviceId") != "memento-smoke" || r.FormValue("deviceAssetId") != "before-midnight.jpg" {
				t.Error("2.x fixture upload needs stable device identity")
			}
			uploaded.Store(true)
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected fixture API %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	_, err := fixture.SetupProbe(t.Context(), server.URL, "v2.7.5")
	require.ErrorContains(t, err, "POST /assets: HTTP 403")
	require.True(t, uploaded.Load())
}

func TestProbeDoesNotChangeNormalVersionGate(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/server/version" {
			_, _ = fmt.Fprint(w, `{"major":2,"minor":7,"patch":5}`)
			return
		}
		writes.Add(1)
		// Stop at signup rather than building the rest of the fixture.
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	_, err := fixture.Setup(t.Context(), server.URL, "v2.7.5")
	require.ErrorContains(t, err, "Import and synchronization")
	require.Zero(t, writes.Load())
	_, err = fixture.SetupProbe(t.Context(), server.URL, "v2.7.5")
	require.ErrorContains(t, err, "POST /auth/admin-sign-up: HTTP 403")
	require.EqualValues(t, 1, writes.Load())
	_, err = fixture.SetupProbe(t.Context(), server.URL, "v2.7.4")
	require.ErrorContains(t, err, "expected stable v2.7.4")
	require.EqualValues(t, 1, writes.Load(), "probe must still verify the exact release before writes")
}
