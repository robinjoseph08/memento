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

func TestProbeDoesNotChangeNormalVersionGate(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/server/version" {
			_, _ = fmt.Fprint(w, `{"major":3,"minor":4,"patch":0}`)
			return
		}
		writes.Add(1)
		// Stop at signup rather than building the rest of the fixture.
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	_, err := fixture.Setup(t.Context(), server.URL, "v3.4.0")
	require.ErrorContains(t, err, "Import and synchronization")
	require.Zero(t, writes.Load())
	_, err = fixture.SetupProbe(t.Context(), server.URL, "v3.4.0")
	require.ErrorContains(t, err, "POST /auth/admin-sign-up: HTTP 403")
	require.EqualValues(t, 1, writes.Load())
	_, err = fixture.SetupProbe(t.Context(), server.URL, "v3.4.1")
	require.ErrorContains(t, err, "expected stable v3.4.1")
	require.EqualValues(t, 1, writes.Load(), "probe must still verify the exact release before writes")
}
