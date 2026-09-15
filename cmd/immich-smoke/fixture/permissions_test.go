package fixture

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingPermissionsAreReportedWithoutSecrets(t *testing.T) {
	t.Parallel()
	for _, permission := range []string{"album.read", "asset.download", "asset.read", "asset.view", "face.read", "person.read"} {
		t.Run(permission, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/api-keys" {
					assert.Equal(t, "Bearer owner-session", r.Header.Get("Authorization"))
					var body struct {
						Permissions []string `json:"permissions"`
					}
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Len(t, body.Permissions, 5)
					assert.NotContains(t, body.Permissions, permission)
					for _, p := range body.Permissions {
						assert.True(t, slices.Contains(readPermissions, p))
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"secret": "never-print-key", "apiKey": map[string]any{"permissions": body.Permissions}})
					return
				}
				assert.Equal(t, "never-print-key", r.Header.Get("X-Api-Key"))
				assert.Empty(t, r.Header.Get("Authorization"))
				paths := map[string]string{"album.read": "/api/albums", "asset.download": "/api/assets/asset/original", "asset.read": "/api/assets/asset", "asset.view": "/api/assets/asset/thumbnail", "face.read": "/api/faces", "person.read": "/api/people/person/thumbnail"}
				assert.Equal(t, paths[permission], r.URL.Path)
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"message":"never-print-key owner-session"}`)
			}))
			t.Cleanup(server.Close)
			library := &Library{baseURL: server.URL, owner: &api{baseURL: server.URL, token: "owner-session", http: server.Client()}, Assets: []Asset{{ID: "asset"}}, Person: Person{ID: "person"}}
			err := library.PermissionFailure(t.Context(), permission)
			require.ErrorContains(t, err, "Enable "+permission)
			require.NotContains(t, err.Error(), "never-print-key")
			require.NotContains(t, err.Error(), "owner-session")
		})
	}
}
