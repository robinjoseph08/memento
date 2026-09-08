package immich_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlbumRequiredMetadata(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"id", "albumName", "description", "albumThumbnailAssetId", "assetCount", "updatedAt"} {
		t.Run("missing "+field, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			require.NoError(t, json.Unmarshal([]byte(albumJSON), &document))
			delete(document, field)
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode([]any{document}) }))
			defer fixture.Close()
			albums, err := immich.New(fixture.URL, "key").ListAlbums(t.Context())
			require.Error(t, err)
			assert.Empty(t, albums, "invalid metadata must not produce a partial album list")
		})
	}
	for _, body := range []string{
		`null`,
		`[{"id":"x","albumName":"","description":"","albumThumbnailAssetId":null,"assetCount":-1,"updatedAt":"2026-07-01T00:00:00Z"}]`,
		`[{"id":"x","albumName":"","description":"","albumThumbnailAssetId":null,"assetCount":0,"updatedAt":"invalid"}]`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer fixture.Close()
			_, err := immich.New(fixture.URL, "key").ListAlbums(t.Context())
			require.Error(t, err)
		})
	}
	t.Run("empty album optional fields", func(t *testing.T) {
		t.Parallel()
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"id":"empty","albumName":"","description":"","albumThumbnailAssetId":null,"assetCount":0,"updatedAt":"2026-07-01T00:00:00Z"}`)
		}))
		defer fixture.Close()
		album, err := immich.New(fixture.URL, "key").GetAlbum(t.Context(), "empty")
		require.NoError(t, err)
		assert.Nil(t, album.ThumbnailID)
		assert.Empty(t, album.StartDate)
	})
}

const assetJSON = `{"id":"asset/opaque ?#%","checksum":"2jmj7l5rSw0yVb/vlWAYkK/YBwk=","originalFileName":"photo.jpg","type":"IMAGE","visibility":"timeline","localDateTime":"2026-06-01T23:30:00-07:00","fileCreatedAt":"2026-06-02T06:30:00Z","updatedAt":"2026-06-02T07:00:00Z","isOffline":false,"isTrashed":false,"width":4032,"height":3024,"duration":null,"thumbhash":null,"livePhotoVideoId":"live-video","exifInfo":{"timeZone":"America/Los_Angeles","orientation":1,"future":true},"future":"ignored"}`

func TestAssetRequiredMetadata(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"id", "checksum", "originalFileName", "type", "visibility", "localDateTime", "fileCreatedAt", "updatedAt", "isOffline", "isTrashed", "width", "height", "duration", "thumbhash"} {
		t.Run("missing "+field, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
			delete(document, field)
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document) }))
			defer fixture.Close()
			asset, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
			require.Error(t, err)
			assert.Empty(t, asset.ID)
		})
	}
	for field, value := range map[string]any{"id": "", "checksum": "bad", "originalFileName": "", "type": "FUTURE", "localDateTime": "2026-06-01", "fileCreatedAt": "bad", "updatedAt": nil, "isOffline": nil, "isTrashed": nil, "width": -1, "height": -1, "duration": -1, "livePhotoVideoId": ""} {
		t.Run("invalid "+field, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
			document[field] = value
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document) }))
			defer fixture.Close()
			_, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
			require.Error(t, err)
		})
	}
	t.Run("optional metadata absent and nullable fields null", func(t *testing.T) {
		t.Parallel()
		var document map[string]any
		require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
		delete(document, "exifInfo")
		delete(document, "livePhotoVideoId")
		document["width"] = nil
		document["height"] = nil
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document) }))
		defer fixture.Close()
		asset, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
		require.NoError(t, err)
		assert.Nil(t, asset.EXIF)
		assert.Nil(t, asset.LivePhotoVideoID)
		assert.Nil(t, asset.Width)
	})
}

func TestAssetVisibilityContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"timeline", "timeline", true},
		{"archive", "archive", true},
		{"hidden", "hidden", true},
		{"locked", "locked", true},
		{"unknown", "future", false},
		{"empty", "", false},
		{"null", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
			document["visibility"] = tc.value
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(document)
			}))
			defer fixture.Close()
			asset, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
			if tc.valid {
				require.NoError(t, err)
				assert.Equal(t, tc.value, asset.Visibility)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestZeroDimensionsFollowUpstreamContract(t *testing.T) {
	t.Parallel()
	var document map[string]any
	require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
	document["width"] = 0
	document["height"] = 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document) }))
	t.Cleanup(fixture.Close)
	asset, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
	require.NoError(t, err)
	assert.Zero(t, *asset.Width)
	assert.Zero(t, *asset.Height)
}

func TestAssetStackMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stack string
		valid       bool
	}{
		{"summary", `{"id":"stack","primaryAssetId":"primary","assetCount":3}`, true},
		{"absent", `null`, true},
		{"missing primary", `{"id":"stack","assetCount":3}`, false},
		{"missing count", `{"id":"stack","primaryAssetId":"primary"}`, false},
		{"invalid count", `{"id":"stack","primaryAssetId":"primary","assetCount":-1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var document map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
			document["stack"] = json.RawMessage(tc.stack)
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document) }))
			defer fixture.Close()
			asset, err := immich.New(fixture.URL, "key").GetAsset(t.Context(), "asset/opaque ?#%")
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.name == "summary" {
				require.NotNil(t, asset.Stack)
				assert.Equal(t, "primary", asset.Stack.PrimaryAssetID)
				assert.Equal(t, 3, asset.Stack.AssetCount)
			} else {
				assert.Nil(t, asset.Stack)
			}
		})
	}
}

func TestInvalidMembershipNeverTruncates(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{}`, `null`, `{"assets":null}`, `{"assets":{}}`,
		`{"assets":{"items":null,"count":0,"nextPage":null}}`,
		`{"assets":{"items":[],"count":0}}`,
		`{"assets":{"items":[],"nextPage":null}}`,
		`{"assets":{"items":[],"count":1,"nextPage":null}}`,
		`{"assets":{"items":[{}],"count":1,"nextPage":null}}`,
		`{"assets":{"items":[],"count":0,"nextPage":"2"}}`,
		`{"assets":{"items":[` + assetJSON + `,` + assetJSON + `],"count":2,"nextPage":null}}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer fixture.Close()
			members, next, err := immich.New(fixture.URL, "key").ListMembers(t.Context(), "album", 1)
			require.Error(t, err)
			assert.Nil(t, members)
			assert.Zero(t, next)
		})
	}
	for _, nextPage := range []string{"0", "1", "-1", "02", "+2", " 2", "2x", "9007199254740992", "https://evil.test", ""} {
		t.Run("next "+nextPage, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"assets":{"items":[%s],"count":1,"nextPage":%q}}`, assetJSON, nextPage)
			}))
			defer fixture.Close()
			_, _, err := immich.New(fixture.URL, "key").ListMembers(t.Context(), "album", 1)
			require.Error(t, err)
		})
	}
}

func TestMemberPagination(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/search/metadata", r.RequestURI)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var request struct {
			AlbumIDs    []string `json:"albumIds"`
			Page        int      `json:"page"`
			Size        int      `json:"size"`
			WithStacked bool     `json:"withStacked"`
			Order       string   `json:"order"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if !assert.NoError(t, decoder.Decode(&request), "withExif must be omitted to retain assets lacking EXIF") {
			return
		}
		assert.Equal(t, []string{"album/opaque ?#%"}, request.AlbumIDs)
		assert.Equal(t, 250, request.Size)
		assert.True(t, request.WithStacked)
		assert.Equal(t, "asc", request.Order)
		if request.Page == 1 {
			_, _ = fmt.Fprint(w, `{"albums":{"items":[]},"assets":{"items":[`+assetJSON+`],"count":1,"total":1,"nextPage":"2","facets":[],"future":true}}`)
		} else {
			assert.Equal(t, 2, request.Page)
			_, _ = fmt.Fprint(w, `{"assets":{"items":[],"count":0,"total":999,"nextPage":null}}`)
		}
	}))
	t.Cleanup(fixture.Close)
	client := immich.New(fixture.URL, "key")
	members, next, err := client.ListMembers(t.Context(), "album/opaque ?#%", 1)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "asset/opaque ?#%", members[0].ID)
	assert.Equal(t, 2, next, "follow nextPage, never total")
	members, next, err = client.ListMembers(t.Context(), "album/opaque ?#%", next)
	require.NoError(t, err)
	assert.Empty(t, members)
	assert.Zero(t, next)
}

func TestMembersMatchVisibleAlbumMembership(t *testing.T) {
	t.Parallel()
	member := func(id, visibility string) map[string]any {
		var document map[string]any
		require.NoError(t, json.Unmarshal([]byte(assetJSON), &document))
		document["id"] = id
		document["visibility"] = visibility
		delete(document, "livePhotoVideoId")
		return document
	}
	still := member("still", "timeline")
	still["livePhotoVideoId"] = "motion"
	motion := member("motion", "hidden")
	motion["type"] = "VIDEO"
	primary := member("stack-primary", "timeline")
	archived := member("stack-archived", "archive")
	stack := map[string]any{"id": "stack", "primaryAssetId": "stack-primary", "assetCount": 2}
	primary["stack"] = stack
	archived["stack"] = stack
	trashed := member("trashed", "timeline")
	trashed["isTrashed"] = true

	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/search/metadata", r.RequestURI)
		var request struct {
			Page        int  `json:"page"`
			WithStacked bool `json:"withStacked"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			return
		}
		assert.True(t, request.WithStacked)
		if request.Page == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{
				"items": []any{member("other-motion", "hidden")}, "count": 1, "nextPage": "2",
			}})
			return
		}
		assert.Equal(t, 2, request.Page)
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{
			"items": []any{still, motion, primary, archived, member("locked", "locked"), trashed},
			"count": 6, "nextPage": nil,
		}})
	}))
	t.Cleanup(fixture.Close)
	client := immich.New(fixture.URL, "key")
	members, next, err := client.ListMembers(t.Context(), "album", 1)
	require.NoError(t, err)
	assert.Empty(t, members, "a hidden-only page has no visible album members")
	require.Equal(t, 2, next, "filtering must not terminate upstream pagination")
	members, next, err = client.ListMembers(t.Context(), "album", next)
	require.NoError(t, err)
	require.Len(t, members, 3)
	assert.Equal(t, "still", members[0].ID)
	require.NotNil(t, members[0].LivePhotoVideoID)
	assert.Equal(t, "motion", *members[0].LivePhotoVideoID)
	assert.Equal(t, "stack-primary", members[1].ID)
	assert.Equal(t, "stack-archived", members[2].ID)
	assert.Equal(t, "archive", members[2].Visibility)
	assert.Zero(t, next)
}

func TestAssetMetadata(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/assets/asset%2Fopaque%20%3F%23%25", r.RequestURI)
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		_, _ = fmt.Fprint(w, assetJSON)
	}))
	t.Cleanup(fixture.Close)
	asset, err := immich.New(fixture.URL, "read-key").GetAsset(t.Context(), "asset/opaque ?#%")
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01T23:30:00-07:00", asset.LocalDateTime, "preserve the wall-clock representation")
	assert.Equal(t, "2026-06-02T06:30:00Z", asset.FileCreatedAt)
	assert.Equal(t, "live-video", *asset.LivePhotoVideoID)
	assert.Equal(t, "America/Los_Angeles", asset.EXIF["timeZone"])
	assert.Equal(t, 4032, *asset.Width)
	assert.Nil(t, asset.Thumbhash)
	assert.Equal(t, "IMAGE", asset.Kind)
}

const albumJSON = `{"id":"album/opaque ?#%","albumName":"Family","description":"Summer","albumThumbnailAssetId":"cover","assetCount":2,"updatedAt":"2026-07-01T02:03:04Z","startDate":"2026-06-01T12:00:00Z","endDate":"2026-06-02T12:00:00Z","future":{"ignored":true}}`

func TestOpaqueIDsStayWithinReadEndpoints(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ id, path string }{
		{"..", "%2E%2E"}, {".", "%2E"},
		{"https://evil.test/original?key=private#fragment", "https:%2F%2Fevil.test%2Foriginal%3Fkey=private%23fragment"},
		{"%2f../original", "%252f..%2Foriginal"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/albums/"+tc.path, r.RequestURI)
				var document map[string]any
				if !assert.NoError(t, json.Unmarshal([]byte(albumJSON), &document)) {
					return
				}
				document["id"] = tc.id
				_ = json.NewEncoder(w).Encode(document)
			}))
			defer fixture.Close()
			album, err := immich.New(fixture.URL, "private-key").GetAlbum(t.Context(), tc.id)
			require.NoError(t, err)
			assert.Equal(t, tc.id, album.ID)
		})
	}
}

func TestMismatchedResourceIDsAreRejected(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"album", "asset"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "album" {
					_, _ = fmt.Fprint(w, albumJSON)
				} else {
					_, _ = fmt.Fprint(w, assetJSON)
				}
			}))
			defer fixture.Close()
			client := immich.New(fixture.URL, "key")
			var err error
			if kind == "album" {
				_, err = client.GetAlbum(t.Context(), "different")
			} else {
				_, err = client.GetAsset(t.Context(), "different")
			}
			require.Error(t, err)
		})
	}
}

func TestAlbumReads(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		switch r.RequestURI {
		case "/base/api/albums":
			_, _ = fmt.Fprint(w, `[`+albumJSON+`]`)
		case "/base/api/albums/album%2Fopaque%20%3F%23%25":
			_, _ = fmt.Fprint(w, albumJSON)
		default:
			t.Errorf("unexpected read: %s", r.RequestURI)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fixture.Close)
	client := immich.New(fixture.URL+"/base/", "read-key")
	albums, err := client.ListAlbums(t.Context())
	require.NoError(t, err)
	require.Len(t, albums, 1)
	assert.Equal(t, "Family", albums[0].Name)
	assert.Equal(t, "Summer", albums[0].Description)
	assert.Equal(t, 2, albums[0].Count)
	assert.Equal(t, "cover", *albums[0].ThumbnailID)
	album, err := client.GetAlbum(t.Context(), "album/opaque ?#%")
	require.NoError(t, err)
	assert.Equal(t, albums[0], album)
}
