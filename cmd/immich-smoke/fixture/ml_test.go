package fixture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunMLRequiresMachineLearningFaces(t *testing.T) {
	t.Parallel()
	for _, sourceType := range []string{"machine-learning", "manual", "exif"} {
		t.Run(sourceType, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			refreshed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /api/assets":
					assert.Equal(t, "Bearer owner", r.Header.Get("Authorization"))
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "ml-photo", "status": "created"})
				case "POST /api/assets/jobs":
					var body map[string]any
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Equal(t, map[string]any{"assetIds": []any{"ml-photo"}, "name": "refresh-faces"}, body)
					refreshed = true
					w.WriteHeader(http.StatusNoContent)
				case "GET /api/faces":
					assert.Equal(t, "ml-photo", r.URL.Query().Get("id"))
					assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "detected-face", "sourceType": sourceType, "person": nil, "imageWidth": 512, "imageHeight": 512, "boundingBoxX1": 10, "boundingBoxY1": 20, "boundingBoxX2": 40, "boundingBoxY2": 70}})
					if sourceType != "machine-learning" {
						cancel()
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			library := &Library{baseURL: server.URL, secret: "read-key", owner: &api{baseURL: server.URL, token: "owner", http: server.Client()}}
			err := library.RunML(ctx)
			require.True(t, refreshed)
			if sourceType == "machine-learning" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
