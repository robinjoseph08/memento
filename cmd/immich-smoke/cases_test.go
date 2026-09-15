package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/cmd/immich-smoke/fixture"
	"github.com/robinjoseph08/memento/internal/testmedia"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdditionalCasesThroughPublishingAndMedia(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	photo := func(id, name string) fixture.Asset {
		return fixture.Asset{ID: id, Filename: name, CapturedAt: "2024-07-04T12:00:00-07:00"}
	}
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 64, 48))))
	library := &fixture.Library{
		CasesAlbum: fixture.Album{ID: "cases", Name: "Extra cases", Description: "Extra cases", AssetIDs: []string{"png", "live", "stack-a", "stack-b", "plain"}},
		PNG:        photo("png", "generated.png"), PNGBytes: pngBytes.Bytes(),
		LivePhoto: photo("live", "live-still.jpg"), LiveMotion: fixture.Video{ID: "motion", Filename: "live-motion.webm"},
		Stack: []fixture.Asset{photo("stack-a", "stack-primary.jpg"), photo("stack-b", "stack-secondary.jpg")}, StackID: "stack",
		PlainVideo: fixture.Video{ID: "plain", Filename: "unchaptered.webm", Bytes: testmedia.Plain},
	}
	thumbhash := "test-thumbhash"
	assets := map[string]immich.Asset{}
	for _, a := range append([]fixture.Asset{library.PNG, library.LivePhoto}, library.Stack...) {
		assets[a.ID] = immich.Asset{ID: a.ID, Filename: a.Filename, Kind: "IMAGE", Visibility: "timeline", Checksum: "AAAAAAAAAAAAAAAAAAAAAAAAAAA=", FileCreatedAt: "2024-07-04T19:00:00Z", LocalDateTime: "2024-07-04T12:00:00Z", UpdatedAt: "2024-07-04T19:00:00Z", Thumbhash: &thumbhash}
	}
	live := assets["live"]
	live.LivePhotoVideoID = &library.LiveMotion.ID
	assets["live"] = live
	for _, id := range []string{"stack-a", "stack-b"} {
		a := assets[id]
		a.Stack = &immich.AssetStack{ID: "stack", PrimaryAssetID: "stack-a", AssetCount: 2}
		assets[id] = a
	}
	assets["plain"] = immich.Asset{ID: "plain", Filename: "unchaptered.webm", Kind: "VIDEO", Visibility: "timeline", Checksum: "BBBBBBBBBBBBBBBBBBBBBBBBBBB=", FileCreatedAt: "2020-01-01T00:00:00Z", LocalDateTime: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z", Thumbhash: &thumbhash}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/server/version":
			_, _ = w.Write([]byte(`{"major":3,"minor":1,"patch":0,"prerelease":null}`))
		case r.URL.Path == "/api/albums/cases":
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "cases", Name: "Extra cases", Description: "Extra cases", Count: 5, UpdatedAt: "2024-07-04T19:00:00Z"})
		case r.URL.Path == "/api/search/metadata":
			var body map[string]any
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, true, body["withStacked"])
			members := []immich.Asset{}
			for _, id := range library.CasesAlbum.AssetIDs {
				members = append(members, assets[id])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"items": members, "count": 5, "nextPage": nil}})
		case r.URL.Path == "/api/faces":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(library.PNGBytes)
		case r.URL.Path == "/api/assets/png/original":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(library.PNGBytes)
		case r.URL.Path == "/api/assets/plain/original" || r.URL.Path == "/api/assets/plain/video/playback":
			w.Header().Set("Content-Type", "video/webm")
			http.ServeContent(w, r, "plain.webm", time.Time{}, bytes.NewReader(testmedia.Plain))
		case strings.HasPrefix(r.URL.Path, "/api/assets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/assets/")
			a, ok := assets[id]
			if !assert.True(t, ok, "unexpected asset %s", id) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(a)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	source := immich.New(server.URL, "read-key")
	source.Probe = ffprobe.Command{}
	require.NoError(t, verifyCaseImport(t.Context(), db, library, source, source))
}
