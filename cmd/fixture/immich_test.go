package main

import (
	"context"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImmichFixture(t *testing.T) {
	t.Parallel()
	fixture := newImmichFixture(true)
	request := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Api-Key", key)
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, req)
		return response
	}
	assert.Equal(t, http.StatusServiceUnavailable, request("GET", "/api/server/version", "", "").Code)
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true}`, "").Code)
	version := request("GET", "/api/server/version", "", "")
	assert.Equal(t, http.StatusOK, version.Code)
	assert.JSONEq(t, `{"major":3,"minor":1,"patch":0,"prerelease":null}`, version.Body.String())
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/api/users/me", "", "wrong").Code)
	owner := request("GET", "/api/users/me", "", "fixture-only-key")
	assert.Equal(t, http.StatusOK, owner.Code)
	assert.JSONEq(t, `{"id":"fixture-owner"}`, owner.Body.String())
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true,"unauthorized":true}`, "").Code)
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/api/users/me", "", "fixture-only-key").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, request("POST", "/api/users/me", `{}`, "fixture-only-key").Code)
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/api/albums", "", "fixture-only-key").Code)
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true,"unsupported":true}`, "").Code)
	assert.JSONEq(t, `{"major":3,"minor":2,"patch":0,"prerelease":null}`, request("GET", "/api/server/version", "", "").Body.String())
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true}`, "").Code)
	assert.JSONEq(t, `{"major":3,"minor":1,"patch":0,"prerelease":null}`, request("GET", "/api/server/version", "", "").Body.String())
	assert.Equal(t, http.StatusBadRequest, request("POST", "/__fixture/state", `{"unauthorized":true}`, "").Code)
	req := httptest.NewRequest(http.MethodPost, "/__fixture/state", strings.NewReader(`{"available":true}`))
	req.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	fixture.ServeHTTP(response, req)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestFixtureImportContract(t *testing.T) {
	t.Parallel()
	fixture := newImmichFixture(false)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	client := immich.New(server.URL, fixtureAPIKey)
	require.NoError(t, client.CheckImport(t.Context()))
	albums, err := client.ListAlbums(t.Context())
	require.NoError(t, err)
	require.Len(t, albums, 33)
	album, err := client.GetAlbum(t.Context(), "fixture-album-coast")
	require.NoError(t, err)
	assert.Equal(t, 6, album.Count)
	members, next, err := client.ListMembers(t.Context(), album.ID, 1)
	require.NoError(t, err)
	require.Len(t, members, 6)
	assert.Zero(t, next)
	assert.Equal(t, "fixture-asset-02", members[1].ID)
	assert.Equal(t, "fixture-asset-03", members[2].ID)
	assert.Equal(t, members[1].LocalDateTime, members[2].LocalDateTime)
	assert.Equal(t, "2026-06-01T23:59:59-07:00", members[0].LocalDateTime)
	for _, member := range members {
		asset, err := client.GetAsset(t.Context(), member.ID)
		require.NoError(t, err)
		assert.Equal(t, member.ID, asset.ID)
		request := httptest.NewRequest(http.MethodGet, "/api/assets/"+asset.ID+"/thumbnail?size=thumbnail", nil)
		request.Header.Set("X-Api-Key", fixtureAPIKey)
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		decoded, format, err := image.Decode(response.Body)
		require.NoError(t, err)
		assert.Contains(t, []string{"png", "jpeg"}, format)
		assert.Equal(t, 320, decoded.Bounds().Dx())
		assert.Equal(t, 240, decoded.Bounds().Dy())
	}
	family, _, err := client.ListMembers(t.Context(), "fixture-album-family", 1)
	require.NoError(t, err)
	assert.Equal(t, members[1].ID, family[0].ID)
	assert.Equal(t, members[3].ID, family[1].ID)
	large, next, err := client.ListMembers(t.Context(), "workbench-large-moment", 1)
	require.NoError(t, err)
	require.Len(t, large, 101)
	assert.Zero(t, next)
	faces, err := client.ListFaces(t.Context(), "workbench-asset-001")
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, "immich-alex-a", faces[0].ID)
	thumbnail, err := client.PersonThumbnail(t.Context(), faces[0].ID)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, thumbnail.Body.Close()) })
	decoded, _, err := image.Decode(thumbnail.Body)
	require.NoError(t, err)
	assert.Equal(t, 320, decoded.Bounds().Dx())
	for _, path := range []string{"/api/albums/fixture-album-coast/assets", "/api/assets/fixture-asset-01/original"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Api-Key", fixtureAPIKey)
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, req)
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
}

func TestFixtureMembershipPagination(t *testing.T) {
	t.Parallel()
	fixture := newImmichFixture(false)
	for page := 1; page <= 4; page++ {
		body := map[string]any{"albumIds": []string{"fixture-album-coast"}, "page": page, "size": 2, "withStacked": true, "order": "asc"}
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/search/metadata", strings.NewReader(string(encoded)))
		req.Header.Set("X-Api-Key", fixtureAPIKey)
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code)
		var document struct {
			Assets struct {
				Items    []sourceAsset `json:"items"`
				Count    int           `json:"count"`
				NextPage *string       `json:"nextPage"`
			} `json:"assets"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &document))
		assert.Len(t, document.Assets.Items, document.Assets.Count)
		if page < 3 {
			require.NotNil(t, document.Assets.NextPage)
		} else {
			assert.Nil(t, document.Assets.NextPage)
		}
		if page == 4 {
			assert.Empty(t, document.Assets.Items)
		}
	}
}

func TestFixtureCheckpoints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, path string }{
		{"asset-metadata", "/api/assets/fixture-asset-02"},
		{"import-release", "/api/assets/fixture-asset-06"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newImmichFixture(false)
			control := func(mode string) {
				response := httptest.NewRecorder()
				fixture.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/__fixture/checkpoints/"+test.name, strings.NewReader(`{"mode":"`+mode+`"}`)))
				require.Equal(t, http.StatusOK, response.Code)
			}
			request := func(ctx context.Context) *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, test.path, nil)
				req.Header.Set("X-Api-Key", fixtureAPIKey)
				response := httptest.NewRecorder()
				fixture.ServeHTTP(response, req)
				return response
			}
			control("fail")
			assert.Equal(t, http.StatusServiceUnavailable, request(t.Context()).Code)
			control("pause")
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(t.Context()) }()
			require.Eventually(t, func() bool {
				fixture.mu.RLock()
				defer fixture.mu.RUnlock()
				return fixture.checkpoints[test.name].Waiting == 1
			}, time.Second, time.Millisecond)
			control("open")
			select {
			case response := <-done:
				assert.Equal(t, http.StatusOK, response.Code)
			case <-time.After(time.Second):
				t.Fatal("checkpoint did not release")
			}
			control("pause")
			ctx, cancel := context.WithCancel(t.Context())
			go func() { done <- request(ctx) }()
			require.Eventually(t, func() bool {
				fixture.mu.RLock()
				defer fixture.mu.RUnlock()
				return fixture.checkpoints[test.name].Waiting == 1
			}, time.Second, time.Millisecond)
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("canceled request remained blocked")
			}
			fixture.mu.RLock()
			assert.Equal(t, 3, fixture.checkpoints[test.name].Hits)
			assert.Zero(t, fixture.checkpoints[test.name].Waiting)
			assert.Equal(t, 3, fixture.requests[http.MethodGet+" "+test.path])
			fixture.mu.RUnlock()
		})
	}
}
