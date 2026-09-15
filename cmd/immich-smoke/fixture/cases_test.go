package fixture

import (
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupCasesKeepsExistingAlbumsSeparate(t *testing.T) {
	t.Parallel()
	uploads := 0
	filenames := map[string]string{}
	linked, stacked := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer owner", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /api/assets":
			if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("assetData")
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer file.Close()
			if header.Filename == "generated.png" {
				_, err = png.Decode(file)
				if !assert.NoError(t, err) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			uploads++
			id := fmt.Sprintf("extra-%d", uploads)
			filenames[header.Filename] = id
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "created"})
		case "PUT /api/assets/extra-3":
			var body map[string]string
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, map[string]string{"livePhotoVideoId": "extra-2"}, body)
			linked = true
		case "POST /api/stacks":
			var body struct {
				AssetIDs []string `json:"assetIds"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, []string{"extra-4", "extra-5"}, body.AssetIDs)
			stacked = true
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "stack"})
		case "POST /api/albums":
			var body struct {
				AssetIDs []string `json:"assetIds"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, []string{"extra-1", "extra-3", "extra-4", "extra-5", "extra-6"}, body.AssetIDs)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "cases"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	library := &Library{Albums: []Album{{ID: "original"}}, Assets: []Asset{{ID: "original-photo"}}, VideoAlbum: Album{ID: "original-video"}, owner: &api{baseURL: server.URL, token: "owner", http: server.Client()}}
	require.NoError(t, library.SetupCases(t.Context()))
	require.True(t, linked)
	require.True(t, stacked)
	require.Equal(t, "cases", library.CasesAlbum.ID)
	require.Equal(t, "extra-1", library.PNG.ID)
	require.Equal(t, "extra-3", library.LivePhoto.ID)
	require.Equal(t, "extra-2", library.LiveMotion.ID)
	require.Equal(t, "extra-6", library.PlainVideo.ID)
	require.Len(t, library.Stack, 2)
	require.Equal(t, []Album{{ID: "original"}}, library.Albums)
	require.Equal(t, []Asset{{ID: "original-photo"}}, library.Assets)
	require.Equal(t, Album{ID: "original-video"}, library.VideoAlbum)
	require.Len(t, filenames, 6)
}
