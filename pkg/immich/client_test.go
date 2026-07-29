package immich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const permissionsJSON = `["album.read","asset.download","asset.read","asset.view","face.read","person.read"]`

func clientConfig(serverURL string) config.ImmichConfig {
	return config.ImmichConfig{URL: serverURL, APIKey: "secret-key", HealthTimeout: 10 * time.Second}
}

func contractServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
}

func validContract(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/server/version":
		_, _ = w.Write([]byte(`{"major":3,"minor":0,"patch":3,"prerelease":null}`))
		return true
	case "/api/api-keys/me":
		_, _ = w.Write([]byte(`{"permissions":` + permissionsJSON + `}`))
		return true
	default:
		return false
	}
}

func TestArchiveInfoValidatesCurrentLivePhotoExpansionAndArchiveStreamsExactIDs(t *testing.T) {
	primary, second, companion := uuid.New(), uuid.New(), uuid.New()
	var archiveBody string
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/assets/" + primary.String():
			_, _ = w.Write([]byte(`{"id":"` + primary.String() + `","livePhotoVideoId":"` + companion.String() + `"}`))
		case "/api/assets/" + second.String():
			_, _ = w.Write([]byte(`{"id":"` + second.String() + `","livePhotoVideoId":null}`))
		case "/api/download/info":
			assert.Equal(t, http.MethodPost, r.Method)
			contents, readErr := io.ReadAll(r.Body)
			assert.NoError(t, readErr)
			assert.JSONEq(t, `{"assetIds":["`+primary.String()+`","`+second.String()+`"]}`, string(contents))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"totalSize":30,"archives":[{"size":30,"assetIds":["` + primary.String() + `","` + second.String() + `","` + companion.String() + `"]}]}`))
		case "/api/download/archive":
			contents, readErr := io.ReadAll(r.Body)
			assert.NoError(t, readErr)
			archiveBody = string(contents)
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Length", "3")
			_, _ = w.Write([]byte("zip"))
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)

	parts, err := client.ArchiveInfo(context.Background(), []uuid.UUID{primary, second})
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Equal(t, []uuid.UUID{primary, second, companion}, parts[0].AssetIDs)
	assert.Equal(t, primary, parts[0].CompanionOf[companion])

	response, err := client.Archive(context.Background(), parts[0].AssetIDs)
	require.NoError(t, err)
	contents, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, "zip", string(contents))
	assert.Equal(t, int64(3), response.ContentLength)
	assert.JSONEq(t, `{"assetIds":["`+primary.String()+`","`+second.String()+`","`+companion.String()+`"]}`, archiveBody)
}

func TestArchiveInfoRejectsUnexpectedExpansionAndMalformedParts(t *testing.T) {
	primary, unexpected := uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/assets/") {
			_, _ = w.Write([]byte(`{"id":"` + primary.String() + `","livePhotoVideoId":null}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"totalSize":10,"archives":[{"size":10,"assetIds":["` + primary.String() + `","` + unexpected.String() + `"]}]}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	_, err = client.ArchiveInfo(context.Background(), []uuid.UUID{primary})
	require.EqualError(t, err, "Immich returned an invalid response")
}

func TestArchiveInfoRejectsOmittedCurrentLivePhotoCompanion(t *testing.T) {
	primary, companion := uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/assets/" + primary.String():
			_, _ = w.Write([]byte(`{"id":"` + primary.String() + `","livePhotoVideoId":"` + companion.String() + `"}`))
		case "/api/download/info":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"totalSize":10,"archives":[{"size":10,"assetIds":["` + primary.String() + `"]}]}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)

	_, err = client.ArchiveInfo(context.Background(), []uuid.UUID{primary})
	require.EqualError(t, err, "Immich returned an invalid response")
}

func TestCheckValidatesVersionAPIKeyAndExactLeastPrivilegePermissions(t *testing.T) {
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, validContract(w, r), "unexpected request %s", r.URL.Path)
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	require.NoError(t, client.Check(context.Background()))
}

func TestCheckRejectsUnsupportedVersionsAndInvalidPermissionSets(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		permissions string
		want        string
	}{
		{"older version", `{"major":3,"minor":0,"patch":2,"prerelease":null}`, permissionsJSON, "Immich version is unsupported"},
		{"duplicate version member", `{"major":4,"major":3,"minor":0,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"case-variant version member", `{"Major":3,"minor":0,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"case-colliding version member", `{"major":4,"Major":3,"minor":0,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"prerelease", `{"major":3,"minor":0,"patch":3,"prerelease":1}`, permissionsJSON, "Immich version is unsupported"},
		{"missing major", `{"minor":0,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"null major", `{"major":null,"minor":0,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"missing minor", `{"major":3,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"null minor", `{"major":3,"minor":null,"patch":3,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"missing patch", `{"major":3,"minor":0,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"null patch", `{"major":3,"minor":0,"patch":null,"prerelease":null}`, permissionsJSON, "Immich returned an invalid response"},
		{"missing permission", `{"major":3,"minor":0,"patch":3,"prerelease":null}`, `["album.read"]`, "Immich API key permissions are invalid"},
		{"extra permission", `{"major":3,"minor":0,"patch":3,"prerelease":null}`, `["all","album.read","asset.download","asset.read","asset.view","face.read","person.read"]`, "Immich API key permissions are invalid"},
		{"duplicate permission", `{"major":3,"minor":0,"patch":3,"prerelease":null}`, `["album.read","album.read","asset.read","asset.view","face.read","person.read"]`, "Immich API key permissions are invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/server/version" {
					_, _ = w.Write([]byte(test.version))
					return
				}
				assert.Equal(t, "/api/api-keys/me", r.URL.Path)
				_, _ = w.Write([]byte(`{"permissions":` + test.permissions + `}`))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			require.EqualError(t, client.Check(context.Background()), test.want)
		})
	}
}

func TestCheckRejectsAmbiguousPermissionMembers(t *testing.T) {
	for name, body := range map[string]string{
		"missing":        `{}`,
		"null":           `{"permissions":null}`,
		"duplicate":      `{"permissions":["all"],"permissions":` + permissionsJSON + `}`,
		"case variant":   `{"Permissions":` + permissionsJSON + `}`,
		"case collision": `{"permissions":["all"],"Permissions":` + permissionsJSON + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/server/version" {
					_, _ = w.Write([]byte(`{"major":3,"minor":0,"patch":3,"prerelease":null}`))
					return
				}
				_, _ = w.Write([]byte(body))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			require.EqualError(t, client.Check(context.Background()), "Immich returned an invalid response")
		})
	}
}

func TestOwnedAlbumsNormalizesAndStripsRawImmichFields(t *testing.T) {
	albumID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/albums", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("isOwned"))
		_, _ = w.Write([]byte(`[{` +
			`"id":"` + albumID.String() + `","albumName":"  Family trip  ","description":" Notes ","assetCount":7,` +
			`"createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-02-03T04:05:06Z",` +
			`"startDate":"2025-01-01T00:00:00-05:00","endDate":"2025-01-02T00:00:00-05:00",` +
			`"lastModifiedAssetTimestamp":"2026-02-01T00:00:00Z",` +
			`"albumUsers":[{"id":"private-owner"}],"originalPath":"/private/path","libraryId":"private-library","people":[{"name":"Private"}]` +
			`}]`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	albums, err := client.OwnedAlbums(context.Background())
	require.NoError(t, err)
	require.Len(t, albums, 1)
	assert.Equal(t, albumID, albums[0].SourceID)
	assert.Equal(t, "Family trip", albums[0].Name)
	assert.Equal(t, "Notes", albums[0].Description)
	assert.Equal(t, 7, albums[0].AssetCount)
	assert.Equal(t, "2025-01-01T05:00:00Z", albums[0].StartDate.Format(time.RFC3339))
	serialized, err := json.Marshal(albums[0])
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "private")
}

func TestOwnedAlbumsRejectsMalformedOrDuplicateSummaries(t *testing.T) {
	id := uuid.New().String()
	validFields := `"albumName":"Album","description":"","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"`
	validAlbum := `{"id":"` + id + `",` + validFields + `}`
	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON", `{`},
		{"null album list", `null`},
		{"missing album name", `[{"id":"` + id + `","description":"","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"null album name", `[{"id":"` + id + `","albumName":null,"description":"","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"missing description", `[{"id":"` + id + `","albumName":"Album","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"null description", `[{"id":"` + id + `","albumName":"Album","description":null,"assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"missing asset count", `[{"id":"` + id + `","albumName":"Album","description":"","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"null asset count", `[{"id":"` + id + `","albumName":"Album","description":"","assetCount":null,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"invalid ID", `[{"id":"private",` + validFields + `}]`},
		{"invalid timestamp", `[{"id":"` + id + `","albumName":"Album","description":"","assetCount":0,"createdAt":"private-path","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"negative count", `[{"id":"` + id + `","albumName":"Album","description":"","assetCount":-1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"unstorable count", `[{"id":"` + id + `","albumName":"Album","description":"","assetCount":2147483648,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`},
		{"duplicate ID", `[` + validAlbum + `,` + validAlbum + `]`},
		{"duplicate ID member", `[{"id":"` + uuid.NewString() + `","id":"` + id + `",` + validFields + `}]`},
		{"case-colliding ID member", `[{"id":"private","ID":"` + id + `",` + validFields + `}]`},
		{"null optional start date", `[{"id":"` + id + `",` + validFields + `,"startDate":null}]`},
		{"empty optional end date", `[{"id":"` + id + `",` + validFields + `,"endDate":""}]`},
		{"null optional last modified", `[{"id":"` + id + `",` + validFields + `,"lastModifiedAssetTimestamp":null}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) })
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			_, err = client.OwnedAlbums(context.Background())
			require.EqualError(t, err, "Immich returned an invalid response")
			assert.NotContains(t, err.Error(), "private")
		})
	}
}

func TestAssetExistsDistinguishesPresentAndDeletedAssets(t *testing.T) {
	assetID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/assets/"+assetID.String(), r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"` + assetID.String() + `"}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)

	exists, err := client.AssetExists(context.Background(), assetID)
	require.NoError(t, err)
	assert.True(t, exists)

	missingServer := contractServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	defer missingServer.Close()
	client, err = New(clientConfig(missingServer.URL), missingServer.Client())
	require.NoError(t, err)
	exists, err = client.AssetExists(context.Background(), assetID)
	require.NoError(t, err)
	assert.False(t, exists)

	deletedServer := contractServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	defer deletedServer.Close()
	client, err = New(clientConfig(deletedServer.URL), deletedServer.Client())
	require.NoError(t, err)
	exists, err = client.AssetExists(context.Background(), assetID)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestAssetExistsRejectsMalformedAndUnauthorizedResponses(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "malformed", status: http.StatusOK, body: `{`, want: "Immich returned an invalid response"},
		{name: "mismatched identity", status: http.StatusOK, body: `{"id":"` + uuid.NewString() + `"}`, want: "Immich returned an invalid response"},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"message":"private"}`, want: "Immich API key is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)

			exists, err := client.AssetExists(context.Background(), assetID)
			assert.False(t, exists)
			require.EqualError(t, err, test.want)
			assert.NotContains(t, err.Error(), "private")
		})
	}
}

func TestAssetDeliveryAvailableRequiresEveryRelevantRepresentation(t *testing.T) {
	for _, mediaType := range []string{"image", "video"} {
		t.Run(mediaType, func(t *testing.T) {
			assetID := uuid.New()
			var paths []string
			server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
				assert.Equal(t, "bytes=0-0", r.Header.Get("Range"))
				contentType := "image/jpeg"
				if strings.HasSuffix(r.URL.Path, "/video/playback") {
					contentType = "video/mp4"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte("available"))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)

			available, err := client.AssetDeliveryAvailable(context.Background(), assetID, mediaType)
			require.NoError(t, err)
			assert.True(t, available)
			want := []string{
				"/api/assets/" + assetID.String() + "/thumbnail?size=thumbnail",
				"/api/assets/" + assetID.String() + "/thumbnail?size=preview",
				"/api/assets/" + assetID.String() + "/original?",
			}
			if mediaType == "video" {
				want = append(want, "/api/assets/"+assetID.String()+"/video/playback?")
			}
			assert.Equal(t, want, paths)
		})
	}
}

func TestAssetDeliveryAvailableFailsClosed(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		name       string
		status     int
		want       bool
		wantErr    string
		failedPath string
	}{
		{name: "missing derivative", status: http.StatusNotFound, failedPath: "/thumbnail", want: false},
		{name: "missing original", status: http.StatusNotFound, failedPath: "/original", want: false},
		{name: "bad request original", status: http.StatusBadRequest, failedPath: "/original", wantErr: "Immich validation failed"},
		{name: "unauthorized original", status: http.StatusUnauthorized, failedPath: "/original", wantErr: "Immich API key is invalid"},
		{name: "empty original", status: http.StatusOK, failedPath: "/original", wantErr: "Immich returned an invalid response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
				failed := strings.Contains(r.URL.Path, test.failedPath)
				if failed {
					w.WriteHeader(test.status)
					return
				}
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write([]byte("available"))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)

			available, err := client.AssetDeliveryAvailable(context.Background(), assetID, "image")
			assert.Equal(t, test.want, available)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, test.wantErr)
			}
		})
	}
}

func TestAssetDeliveryAvailableBoundsBodyReadsAfterHeaders(t *testing.T) {
	assetID := uuid.New()
	headersSent := make(chan struct{})
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersSent)
		<-r.Context().Done()
	})
	defer server.Close()
	cfg := clientConfig(server.URL)
	cfg.HealthTimeout = 50 * time.Millisecond
	client, err := New(cfg, server.Client())
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, availableErr := client.AssetDeliveryAvailable(context.Background(), assetID, "image")
		result <- availableErr
	}()
	select {
	case <-headersSent:
	case <-time.After(time.Second):
		t.Fatal("delivery probe did not receive response headers")
	}
	select {
	case availableErr := <-result:
		require.EqualError(t, availableErr, "Immich returned an invalid response")
	case <-time.After(time.Second):
		t.Fatal("delivery probe body read exceeded its deadline")
	}
}

func TestAlbumAssetsPageUsesPinnedPaginationAndReturnsOnlyNormalizedRepairEvidence(t *testing.T) {
	albumID, assetID := uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/search/metadata", r.URL.Path)
		var request struct {
			AlbumIDs   []string `json:"albumIds"`
			Page       int      `json:"page"`
			Size       int      `json:"size"`
			WithExif   bool     `json:"withExif"`
			WithPeople bool     `json:"withPeople"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			return
		}
		assert.Equal(t, []string{albumID.String()}, request.AlbumIDs)
		assert.Equal(t, 2, request.Page)
		assert.Equal(t, 1000, request.Size)
		assert.False(t, request.WithExif)
		assert.False(t, request.WithPeople)
		_, _ = w.Write([]byte(`{"assets":{"count":1,"items":[{` +
			`"id":"` + assetID.String() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00.000Z",` +
			`"originalPath":"/secret/source.jpg","libraryId":"secret-library","people":[{"name":"Private face"}]` +
			`}],"nextPage":null,"total":1},"albums":{"items":[]}}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	page, err := client.AlbumAssetsPage(context.Background(), albumID, 2)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, assetID, page.Items[0].SourceID)
	assert.Equal(t, "image", page.Items[0].MediaType)
	require.NotNil(t, page.Items[0].LocalDateTime)
	assert.Equal(t, "2026-01-01T10:00:00.000Z", *page.Items[0].LocalDateTime)
	assert.Nil(t, page.NextPage)
	assert.Equal(t, "/secret/source.jpg", page.Items[0].OriginalPath)
	serialized, err := json.Marshal(page)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "secret-library")
	assert.NotContains(t, string(serialized), "Private face")
}

func TestPeopleAndFacesNormalizePrivateRepairEvidence(t *testing.T) {
	personID, assetID, faceID := uuid.New(), uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/people":
			assert.Equal(t, "true", r.URL.Query().Get("withHidden"))
			assert.Equal(t, "1", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`{"people":[{"id":"` + personID.String() + `","name":"  Family member  ","isHidden":true,"thumbnailPath":"private"}],"total":1,"hidden":1,"hasNextPage":false}`))
		case "/api/faces":
			assert.Equal(t, assetID.String(), r.URL.Query().Get("id"))
			_, _ = w.Write([]byte(`[{"id":"` + faceID.String() + `","imageWidth":1200,"imageHeight":800,"boundingBoxX1":10,"boundingBoxY1":20,"boundingBoxX2":110,"boundingBoxY2":220,"person":{"id":"` + personID.String() + `","name":"private"}}]`))
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	people, err := client.People(context.Background())
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, PersonSummary{SourceID: personID, Name: "Family member", Hidden: true}, people[0])
	faces, err := client.Faces(context.Background(), assetID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, faceID, faces[0].SourceID)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, personID, *faces[0].PersonID)
	assert.Equal(t, 10, faces[0].X1)
	assert.Equal(t, 220, faces[0].Y2)
}

func TestPeoplePaginationUsesHasNextPageWhenTotalIncludesFilteredPeople(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"people":[{"id":"` + firstID.String() + `","name":"First","isHidden":false}],"total":3,"hidden":1,"hasNextPage":true}`))
		case "2":
			_, _ = w.Write([]byte(`{"people":[{"id":"` + secondID.String() + `","name":"Second","isHidden":false}],"total":3,"hidden":1,"hasNextPage":false}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	people, err := client.People(context.Background())
	require.NoError(t, err)
	require.Len(t, people, 2)
	assert.Equal(t, firstID, people[0].SourceID)
	assert.Equal(t, secondID, people[1].SourceID)
}

func TestPeopleAndFacesRejectCaseDriftAndMissingPersonFields(t *testing.T) {
	assetID, faceID, personID := uuid.New(), uuid.New(), uuid.New()
	t.Run("People response", func(t *testing.T) {
		server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"People":[],"total":0,"hidden":0}`))
		})
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		_, err = client.People(context.Background())
		require.EqualError(t, err, "Immich returned an invalid response")
	})

	for name, person := range map[string]string{
		"missing person":             "",
		"case variant face":          `,"Person":null`,
		"case variant nested person": `,"person":{"ID":"` + personID.String() + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"` + faceID.String() + `","imageWidth":100,"imageHeight":80,"boundingBoxX1":1,"boundingBoxY1":2,"boundingBoxX2":20,"boundingBoxY2":30` + person + `}]`))
			})
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			_, err = client.Faces(context.Background(), assetID)
			require.EqualError(t, err, "Immich returned an invalid response")
		})
	}
}

func TestFacesIdentifyDeletedAssets(t *testing.T) {
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	_, err = client.Faces(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAlbumAssetsNormalizeChecksumForRepairWithoutForwardingBase64(t *testing.T) {
	albumID, assetID := uuid.New(), uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"assets":{"count":1,"items":[{"id":"` + assetID.String() + `","type":"IMAGE","width":1,"height":1,"localDateTime":"2026-01-01T00:00:00Z","fileCreatedAt":"2025-12-31T23:00:00Z","checksum":"ERERERERERERERERERERERERERE=","originalFileName":" family.jpg ","originalPath":" /private/family.jpg "}],"nextPage":null,"total":1}}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	page, err := client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "1111111111111111111111111111111111111111", page.Items[0].Checksum)
	assert.Equal(t, "2025-12-31T23:00:00Z", page.Items[0].CaptureAt)
	assert.Equal(t, "family.jpg", page.Items[0].Filename)
	assert.Equal(t, "/private/family.jpg", page.Items[0].OriginalPath)
}

func TestAlbumAssetsPagePreservesUnzonedAndUnknownCaptureDates(t *testing.T) {
	albumID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"assets":{"count":2,"items":[` +
			`{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00"},` +
			`{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":null}` +
			`],"nextPage":null,"total":2}}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	page, err := client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.NotNil(t, page.Items[0].LocalDateTime)
	assert.Equal(t, "2026-01-01T10:00:00", *page.Items[0].LocalDateTime)
	assert.Nil(t, page.Items[1].LocalDateTime)
}

func TestAlbumSummaryUsesPinnedDetailContract(t *testing.T) {
	albumID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/albums/"+albumID.String(), r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"` + albumID.String() + `","albumName":"Album","description":"","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	album, err := client.Album(context.Background(), albumID)
	require.NoError(t, err)
	assert.Equal(t, albumID, album.SourceID)

	_, err = client.Album(context.Background(), uuid.Nil)
	require.EqualError(t, err, "Immich returned an invalid response")
}

func TestAlbumAndMembershipMissingResponsesIdentifyMissingSourceAlbum(t *testing.T) {
	albumID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/albums/" + albumID.String():
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusBadRequest)
		case "/api/search/metadata":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusBadRequest)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)

	_, err = client.Album(context.Background(), albumID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAlbumSummaryRejectsMismatchedIdentity(t *testing.T) {
	requestedID := uuid.New()
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"` + uuid.NewString() + `","albumName":"Album","description":"","assetCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	_, err = client.Album(context.Background(), requestedID)
	require.EqualError(t, err, "Immich returned an invalid response")
}

func TestAlbumAssetsPaginationContractContinuesBeyondOneThousand(t *testing.T) {
	albumID := uuid.New()
	items := make([]map[string]any, assetPageSize)
	for index := range items {
		items[index] = map[string]any{
			"id": uuid.NewString(), "type": "IMAGE", "width": nil, "height": nil,
			"localDateTime": "2026-01-01T10:00:00Z",
		}
	}
	firstPayload, err := json.Marshal(map[string]any{"assets": map[string]any{
		"count": assetPageSize, "items": items, "nextPage": "2", "total": assetPageSize,
	}})
	require.NoError(t, err)
	lastID := uuid.New()
	secondPayload := []byte(`{"assets":{"count":1,"items":[{"id":"` + lastID.String() + `","type":"IMAGE","width":null,"height":null,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`)
	var requested []int
	server := contractServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Page int `json:"page"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requested = append(requested, request.Page)
		if request.Page == 1 {
			_, _ = w.Write(firstPayload)
			return
		}
		_, _ = w.Write(secondPayload)
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	first, err := client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.NoError(t, err)
	require.NotNil(t, first.NextPage)
	second, err := client.AlbumAssetsPage(context.Background(), albumID, *first.NextPage)
	require.NoError(t, err)
	assert.Len(t, first.Items, 1000)
	assert.Equal(t, lastID, second.Items[0].SourceID)
	assert.Equal(t, []int{1, 2}, requested)
}

func TestAlbumAssetsPageAcceptsOnlyFullNonterminalPages(t *testing.T) {
	albumID := uuid.New()
	items := make([]map[string]any, assetPageSize)
	for index := range items {
		items[index] = map[string]any{
			"id": uuid.NewString(), "type": "IMAGE", "width": nil, "height": nil,
			"localDateTime": "2026-01-01T10:00:00Z",
		}
	}
	payload, err := json.Marshal(map[string]any{
		"assets": map[string]any{
			"count": assetPageSize, "items": items, "nextPage": "2", "total": assetPageSize,
		},
	})
	require.NoError(t, err)
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	page, err := client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.NoError(t, err)
	assert.Len(t, page.Items, assetPageSize)
	require.NotNil(t, page.NextPage)
	assert.Equal(t, 2, *page.NextPage)
}

func TestAlbumAssetsPagePreservesDuplicateIdentifiersForSnapshotDeduplication(t *testing.T) {
	albumID := uuid.New()
	assetID := uuid.NewString()
	asset := `{"id":"` + assetID + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00Z"}`
	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"assets":{"count":2,"items":[` + asset + `,` + asset + `],"nextPage":null,"total":2}}`))
	})
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	page, err := client.AlbumAssetsPage(context.Background(), albumID, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, page.Items[0], page.Items[1])
}

func TestAlbumAssetsPageRejectsInvalidEntryPointsAndResponses(t *testing.T) {
	client, err := New(clientConfig("https://immich.internal"), nil)
	require.NoError(t, err)
	_, err = client.AlbumAssetsPage(context.Background(), uuid.Nil, 1)
	require.EqualError(t, err, "Immich returned an invalid response")
	_, err = client.AlbumAssetsPage(context.Background(), uuid.New(), 0)
	require.EqualError(t, err, "Immich returned an invalid response")

	albumID := uuid.New()
	duplicateID := uuid.NewString()
	validAssetFields := `"type":"IMAGE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00Z"`
	validAsset := `{"id":"` + duplicateID + `",` + validAssetFields + `}`
	for name, body := range map[string]string{
		"missing assets":              `{}`,
		"null assets":                 `{"assets":null}`,
		"missing count":               `{"assets":{"items":[],"nextPage":null,"total":0}}`,
		"count mismatch":              `{"assets":{"count":1,"items":[],"nextPage":null,"total":0}}`,
		"duplicate count member":      `{"assets":{"count":1,"count":0,"items":[],"nextPage":null,"total":0}}`,
		"case-colliding count member": `{"assets":{"count":1,"Count":0,"items":[],"nextPage":null,"total":0}}`,
		"missing items":               `{"assets":{"count":0,"nextPage":null,"total":0}}`,
		"null items":                  `{"assets":{"count":0,"items":null,"nextPage":null,"total":0}}`,
		"missing next page":           `{"assets":{"count":0,"items":[],"total":0}}`,
		"missing total":               `{"assets":{"count":0,"items":[],"nextPage":null}}`,
		"null total":                  `{"assets":{"count":0,"items":[],"nextPage":null,"total":null}}`,
		"mismatched total":            `{"assets":{"count":0,"items":[],"nextPage":null,"total":1}}`,
		"underfilled continuation":    `{"assets":{"count":1,"items":[` + validAsset + `],"nextPage":"2","total":1}}`,
		"bad asset":                   `{"assets":{"count":1,"items":[{"id":"private",` + validAssetFields + `}],"nextPage":null,"total":1}}`,
		"bad type":                    `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"PRIVATE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"unsupported audio":           `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"AUDIO","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing width":               `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing height":              `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"unstorable width":            `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":2147483648,"height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing local date time":     `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800}],"nextPage":null,"total":1}}`,
		"bad local date time":         `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"yesterday"}],"nextPage":null,"total":1}}`,
		"zoned year zero":             `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"0000-01-01T00:00:00Z"}],"nextPage":null,"total":1}}`,
		"unzoned year zero":           `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"0000-01-01T00:00:00"}],"nextPage":null,"total":1}}`,
		"skipped next page":           `{"assets":{"count":0,"items":[],"nextPage":"3","total":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			defer server.Close()
			client, newErr := New(clientConfig(server.URL), server.Client())
			require.NoError(t, newErr)
			_, pageErr := client.AlbumAssetsPage(context.Background(), albumID, 1)
			require.EqualError(t, pageErr, "Immich returned an invalid response")
		})
	}
}

func TestClientReturnsSafeFailClosedDependencyErrors(t *testing.T) {
	operations := []struct {
		name         string
		run          func(*Client) error
		want         string
		notFoundWant string
	}{
		{"validation", func(client *Client) error { return client.Check(context.Background()) }, "Immich validation failed", ""},
		{"discovery", func(client *Client) error { _, err := client.OwnedAlbums(context.Background()); return err }, "Immich album discovery failed", ""},
		{"album summary", func(client *Client) error { _, err := client.Album(context.Background(), uuid.New()); return err }, "Immich album discovery failed", ErrNotFound.Error()},
		{"membership", func(client *Client) error {
			_, err := client.AlbumAssetsPage(context.Background(), uuid.New(), 1)
			return err
		}, "Immich album membership lookup failed", ErrNotFound.Error()},
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, operation := range operations {
			t.Run(operation.name+"/"+http.StatusText(status), func(t *testing.T) {
				server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte("private dependency URL / source path / raw DTO"))
				})
				defer server.Close()
				client, err := New(clientConfig(server.URL), server.Client())
				require.NoError(t, err)
				err = operation.run(client)
				if status == http.StatusUnauthorized || status == http.StatusForbidden {
					require.EqualError(t, err, "Immich API key is invalid")
					assert.True(t, IsConfigurationError(err))
				} else {
					want := operation.want
					if status == http.StatusNotFound && operation.notFoundWant != "" {
						want = operation.notFoundWant
					}
					require.EqualError(t, err, want)
					assert.False(t, IsConfigurationError(err))
				}
				assert.NotContains(t, err.Error(), "private")
			})
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial http://private.internal?key=secret")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read https://immich.internal/private?key=secret")
}

func (failingReadCloser) Close() error { return nil }

func TestClientRejectsRedirectsWithoutForwardingAPIKey(t *testing.T) {
	targetRequests := make(chan *http.Request, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		targetRequests <- r.Clone(r.Context())
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, err := New(clientConfig(source.URL), source.Client())
	require.NoError(t, err)
	require.EqualError(t, client.Check(context.Background()), "Immich validation failed")
	select {
	case request := <-targetRequests:
		t.Fatalf("redirect target received request with API key %q", request.Header.Get("x-api-key"))
	default:
	}
}

func TestClientRedactsTransportErrorsAndTimeouts(t *testing.T) {
	client, err := New(clientConfig("https://immich.internal"), &http.Client{Transport: failingTransport{}})
	require.NoError(t, err)
	require.EqualError(t, client.Check(context.Background()), "Immich is unreachable")

	server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"major":3,"minor":0,"patch":3,"prerelease":null}`))
	})
	defer server.Close()
	cfg := clientConfig(server.URL)
	cfg.HealthTimeout = time.Millisecond
	client, err = New(cfg, server.Client())
	require.NoError(t, err)
	require.EqualError(t, client.Check(context.Background()), "Immich is unreachable")
}

func TestClientRejectsInvalidOrOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":        `{`,
		"missing field":    `{"major":3,"minor":0,"patch":3}`,
		"trailing JSON":    `{"major":3,"minor":0,"patch":3,"prerelease":null}{}`,
		"trailing garbage": `{"major":3,"minor":0,"patch":3,"prerelease":null} private`,
		"oversized":        `{"major":3,"minor":0,"patch":3,"prerelease":null}` + strings.Repeat(" ", maxJSONResponse),
	} {
		t.Run(name, func(t *testing.T) {
			server := contractServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			require.EqualError(t, client.Check(context.Background()), "Immich returned an invalid response")
		})
	}
}

func TestNewRejectsInvalidURLWithoutEchoingIt(t *testing.T) {
	cfg := clientConfig("https://%zz-secret")
	_, err := New(cfg, nil)
	require.EqualError(t, err, "parse Immich URL")
	assert.NotContains(t, err.Error(), "secret")
}

func TestThumbnailMapsUpstreamStatusesTimeoutsAndReadFailuresToSafeErrors(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		status int
		want   string
	}{
		{status: http.StatusUnauthorized, want: "Immich API key is invalid"},
		{status: http.StatusForbidden, want: "Immich API key is invalid"},
		{status: http.StatusBadRequest, want: "Immich resource not found"},
		{status: http.StatusNotFound, want: "Immich resource not found"},
		{status: http.StatusInternalServerError, want: "Immich validation failed"},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"private":"response"}`))
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)

			_, err = client.Thumbnail(context.Background(), assetID, MediaRequest{})
			require.EqualError(t, err, test.want)
		})
	}

	t.Run("timeout", func(t *testing.T) {
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		cfg := clientConfig("https://immich.internal")
		cfg.HealthTimeout = time.Millisecond
		client, err := New(cfg, &http.Client{Transport: transport})
		require.NoError(t, err)

		_, err = client.Thumbnail(context.Background(), assetID, MediaRequest{})
		require.EqualError(t, err, "Immich is unreachable")
	})

	t.Run("streaming read failure", func(t *testing.T) {
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
				Body:       failingReadCloser{},
				Request:    request,
			}, nil
		})
		client, err := New(clientConfig("https://immich.internal"), &http.Client{Transport: transport})
		require.NoError(t, err)
		response, err := client.Thumbnail(context.Background(), assetID, MediaRequest{})
		require.NoError(t, err)

		_, err = io.ReadAll(response.Body)
		require.EqualError(t, err, "Immich returned an invalid response")
		require.NoError(t, response.Body.Close())
	})
}

func TestMediaRepresentationsFollowOnlySameOriginRedirectsWithCredentials(t *testing.T) {
	assetID := uuid.New()
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		if r.URL.Path == "/api/assets/"+assetID.String()+"/thumbnail" {
			assert.Equal(t, "preview", r.URL.Query().Get("size"))
			http.Redirect(w, r, "/generated/preview", http.StatusTemporaryRedirect)
			return
		}
		assert.Equal(t, "/generated/preview", r.URL.Path)
		redirected = true
		assert.Equal(t, `"preview"`, r.Header.Get("If-None-Match"))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("preview"))
	}))
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	response, err := client.Preview(context.Background(), assetID, MediaRequest{IfNoneMatch: `"preview"`})
	require.NoError(t, err)
	contents, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.True(t, redirected)
	assert.Equal(t, "preview", string(contents))

	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalled = true }))
	defer target.Close()
	crossOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		http.Redirect(w, r, target.URL+"/private", http.StatusFound)
	}))
	defer crossOrigin.Close()
	client, err = New(clientConfig(crossOrigin.URL), crossOrigin.Client())
	require.NoError(t, err)
	_, err = client.Original(context.Background(), assetID, MediaRequest{})
	require.EqualError(t, err, "Immich validation failed")
	assert.False(t, targetCalled, "an unapproved origin is never contacted")

	credentialTargetCalled := false
	var credentialServer *httptest.Server
	credentialServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/credential-target" {
			credentialTargetCalled = true
			return
		}
		location := strings.Replace(credentialServer.URL, "://", "://user@", 1) + "/credential-target"
		http.Redirect(w, r, location, http.StatusFound)
	}))
	defer credentialServer.Close()
	client, err = New(clientConfig(credentialServer.URL), credentialServer.Client())
	require.NoError(t, err)
	_, err = client.Preview(context.Background(), assetID, MediaRequest{})
	require.EqualError(t, err, "Immich validation failed")
	assert.False(t, credentialTargetCalled, "credential-bearing redirects are rejected before a second request")
}

func TestMediaRepresentationEnforcesTheSameOriginRedirectBudget(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		name              string
		requiredRedirects int
		wantSuccess       bool
	}{
		{name: "five redirects succeed", requiredRedirects: maxMediaRedirects, wantSuccess: true},
		{name: "sixth redirect is rejected", requiredRedirects: maxMediaRedirects + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			terminalReached := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
				assert.Equal(t, `"redirect-budget"`, r.Header.Get("If-None-Match"))
				hop := 0
				if r.URL.Path != "/api/assets/"+assetID.String()+"/thumbnail" {
					var err error
					hop, err = strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
					if err != nil {
						t.Errorf("unexpected redirect path %q: %v", r.URL.Path, err)
						http.Error(w, "unexpected path", http.StatusNotFound)
						return
					}
				}
				if hop == test.requiredRedirects {
					terminalReached = true
					w.Header().Set("Content-Type", "image/jpeg")
					_, _ = w.Write([]byte("preview"))
					return
				}
				http.Redirect(w, r, fmt.Sprintf("/hop/%d", hop+1), http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)

			response, err := client.Preview(context.Background(), assetID, MediaRequest{IfNoneMatch: `"redirect-budget"`})
			if test.wantSuccess {
				require.NoError(t, err)
				contents, readErr := io.ReadAll(response.Body)
				require.NoError(t, readErr)
				require.NoError(t, response.Body.Close())
				assert.Equal(t, "preview", string(contents))
				assert.True(t, terminalReached)
			} else {
				require.EqualError(t, err, "Immich validation failed")
				assert.False(t, terminalReached)
			}
			assert.Equal(t, maxMediaRedirects+1, requestCount)
		})
	}
}

func TestVideoAndOriginalStreamRangesValidatorsAndUnchangedBytes(t *testing.T) {
	assetID := uuid.New()
	original := []byte{0, 1, 2, 0xff, 'E', 'X', 'I', 'F'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		switch r.URL.Path {
		case "/api/assets/" + assetID.String() + "/video/playback":
			assert.Equal(t, "bytes=4-7", r.Header.Get("Range"))
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"video-v1"`)
			w.Header().Set("Last-Modified", "Mon, 27 Jul 2026 12:00:00 GMT")
			w.Header().Set("X-Immich-Path", "/private/video.mp4")
			if r.Header.Get("If-Range") != `"video-v1"` {
				_, _ = w.Write([]byte("0123456789abcdefghij"))
				return
			}
			w.Header().Set("Content-Range", "bytes 4-7/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("5678"))
		case "/api/assets/" + assetID.String() + "/original":
			assert.Equal(t, `"original-v1"`, r.Header.Get("If-None-Match"))
			assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(original)))
			_, _ = w.Write(original)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)

	video, err := client.Video(context.Background(), assetID, MediaRequest{Range: "bytes=4-7", IfRange: `"video-v1"`})
	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, video.StatusCode)
	assert.Equal(t, "bytes 4-7/20", video.ContentRange)
	assert.Equal(t, "bytes", video.AcceptRanges)
	assert.Equal(t, `"video-v1"`, video.ETag)
	assert.Equal(t, "Mon, 27 Jul 2026 12:00:00 GMT", video.LastModified)
	videoBytes, err := io.ReadAll(video.Body)
	require.NoError(t, err)
	require.NoError(t, video.Body.Close())
	assert.Equal(t, []byte("5678"), videoBytes)

	fullVideo, err := client.Video(context.Background(), assetID, MediaRequest{Range: "bytes=4-7", IfRange: `"stale"`})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, fullVideo.StatusCode)
	assert.Empty(t, fullVideo.ContentRange)
	fullVideoBytes, err := io.ReadAll(fullVideo.Body)
	require.NoError(t, err)
	require.NoError(t, fullVideo.Body.Close())
	assert.Equal(t, []byte("0123456789abcdefghij"), fullVideoBytes, "an If-Range mismatch falls back to the complete representation")

	download, err := client.Original(context.Background(), assetID, MediaRequest{IfNoneMatch: `"original-v1"`})
	require.NoError(t, err)
	downloaded, err := io.ReadAll(download.Body)
	require.NoError(t, err)
	require.NoError(t, download.Body.Close())
	assert.Equal(t, original, downloaded, "original bytes are never decoded or transformed")
	assert.Equal(t, int64(len(original)), download.ContentLength)
}

func TestOriginalPostHeaderStreamCancellationReachesUpstream(t *testing.T) {
	assetID := uuid.New()
	upstreamCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("a"))
		w.(http.Flusher).Flush()
		<-request.Context().Done()
		close(upstreamCanceled)
	}))
	defer server.Close()
	client, err := New(clientConfig(server.URL), server.Client())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response, err := client.Original(ctx, assetID, MediaRequest{})
	require.NoError(t, err)

	cancel()
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		require.FailNow(t, "upstream did not observe post-header cancellation")
	}
	_, err = io.ReadAll(response.Body)
	require.EqualError(t, err, "Immich returned an invalid response")
	require.NoError(t, response.Body.Close())
}

func TestChunkedPartialBodiesMustMatchRangeAndDerivativeLimit(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		name     string
		body     string
		wantBody string
		wantErr  string
	}{
		{name: "exact", body: "5678", wantBody: "5678"},
		{name: "short", body: "567", wantBody: "567", wantErr: "Immich returned an invalid response"},
		{name: "long", body: "56789", wantBody: "5678", wantErr: "Immich returned an invalid response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Range", "bytes 4-7/20")
				w.WriteHeader(http.StatusPartialContent)
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			response, err := client.Video(context.Background(), assetID, MediaRequest{Range: "bytes=4-7"})
			require.NoError(t, err)
			contents, readErr := io.ReadAll(response.Body)
			assert.Equal(t, test.wantBody, string(contents))
			if test.wantErr == "" {
				require.NoError(t, readErr)
			} else {
				require.EqualError(t, readErr, test.wantErr)
			}
			require.NoError(t, response.Body.Close())
		})
	}

	t.Run("oversized derivative range", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Range", "bytes 0-20971520/20971521")
			w.WriteHeader(http.StatusPartialContent)
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		_, err = client.Preview(context.Background(), assetID, MediaRequest{Range: "bytes=0-20971520"})
		require.EqualError(t, err, "Immich returned an invalid response")
	})
}

func TestMediaConditionalAndUnsatisfiedRangesReturnSafeEmptyResponses(t *testing.T) {
	assetID := uuid.New()
	for _, test := range []struct {
		name         string
		request      MediaRequest
		status       int
		contentRange string
	}{
		{name: "not modified", request: MediaRequest{IfNoneMatch: `"current"`}, status: http.StatusNotModified},
		{name: "range unsatisfied", request: MediaRequest{Range: "bytes=99-100"}, status: http.StatusRequestedRangeNotSatisfiable, contentRange: "bytes */12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, test.request.Range, r.Header.Get("Range"))
				assert.Equal(t, test.request.IfNoneMatch, r.Header.Get("If-None-Match"))
				w.Header().Set("ETag", `"current"`)
				if test.contentRange != "" {
					w.Header().Set("Content-Range", test.contentRange)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"private":"must not be forwarded"}`))
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			response, err := client.Original(context.Background(), assetID, test.request)
			require.NoError(t, err)
			assert.Equal(t, test.status, response.StatusCode)
			assert.Equal(t, int64(-1), response.ContentLength)
			require.NoError(t, response.Body.Close())
		})
	}
}

func TestMediaRepresentationsRejectInvalidEntryPointsStatusesAndHeaders(t *testing.T) {
	client, err := New(clientConfig("https://immich.internal"), nil)
	require.NoError(t, err)
	_, err = client.Original(context.Background(), uuid.Nil, MediaRequest{})
	require.EqualError(t, err, "Immich returned an invalid response")

	assetID := uuid.New()
	for _, test := range []struct {
		name     string
		request  MediaRequest
		status   int
		headers  http.Header
		original bool
		want     string
	}{
		{name: "recipient-induced bad request", request: MediaRequest{IfNoneMatch: "malformed"}, status: http.StatusBadRequest, want: "Immich validation failed"},
		{name: "missing resource", status: http.StatusNotFound, want: "Immich resource not found"},
		{name: "dependency unavailable", status: http.StatusServiceUnavailable, want: "Immich validation failed"},
		{name: "video with image body", status: http.StatusOK, headers: http.Header{"Content-Type": {"image/jpeg"}}, want: "Immich returned an invalid response"},
		{name: "original SVG", status: http.StatusOK, original: true, headers: http.Header{"Content-Type": {"image/svg+xml"}}, want: "Immich returned an invalid response"},
		{name: "encoded original", status: http.StatusOK, original: true, headers: http.Header{"Content-Type": {"image/jpeg"}, "Content-Encoding": {"gzip"}}, want: "Immich returned an invalid response"},
		{name: "partial without range request", status: http.StatusPartialContent, headers: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 4-7/20"}}, want: "Immich returned an invalid response"},
		{name: "partial outside requested range", request: MediaRequest{Range: "bytes=0-3"}, status: http.StatusPartialContent, headers: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 4-7/20"}}, want: "Immich returned an invalid response"},
		{name: "partial without content range", request: MediaRequest{Range: "bytes=4-7"}, status: http.StatusPartialContent, headers: http.Header{"Content-Type": {"video/mp4"}}, want: "Immich returned an invalid response"},
		{name: "partial with reversed range", request: MediaRequest{Range: "bytes=4-7"}, status: http.StatusPartialContent, headers: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 7-4/20"}}, want: "Immich returned an invalid response"},
		{name: "partial with mismatched length", request: MediaRequest{Range: "bytes=4-7"}, status: http.StatusPartialContent, headers: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 4-7/20"}, "Content-Length": {"3"}}, want: "Immich returned an invalid response"},
		{name: "not modified without validator", status: http.StatusNotModified, want: "Immich returned an invalid response"},
		{name: "unsatisfied without total", request: MediaRequest{Range: "bytes=99-100"}, status: http.StatusRequestedRangeNotSatisfiable, want: "Immich returned an invalid response"},
		{name: "unsatisfied without range request", status: http.StatusRequestedRangeNotSatisfiable, headers: http.Header{"Content-Range": {"bytes */12"}}, want: "Immich returned an invalid response"},
		{name: "unsatisfied for satisfiable range", request: MediaRequest{Range: "bytes=0-1"}, status: http.StatusRequestedRangeNotSatisfiable, headers: http.Header{"Content-Range": {"bytes */12"}}, want: "Immich returned an invalid response"},
		{name: "unsafe range unit", status: http.StatusOK, headers: http.Header{"Content-Type": {"video/mp4"}, "Accept-Ranges": {"items"}}, want: "Immich returned an invalid response"},
		{name: "invalid last modified", status: http.StatusOK, headers: http.Header{"Content-Type": {"video/mp4"}, "Last-Modified": {"private timestamp"}}, want: "Immich returned an invalid response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for name, values := range test.headers {
					for _, value := range values {
						w.Header().Add(name, value)
					}
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("abc"))
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			if test.original {
				_, err = client.Original(context.Background(), assetID, test.request)
			} else {
				_, err = client.Video(context.Background(), assetID, test.request)
			}
			require.EqualError(t, err, test.want)
		})
	}
}

func TestOriginalInterruptedStreamReturnsOnlySafeError(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Header:        http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:          failingReadCloser{},
			Request:       request,
		}, nil
	})
	client, err := New(clientConfig("https://immich.internal"), &http.Client{Transport: transport})
	require.NoError(t, err)
	response, err := client.Original(context.Background(), uuid.New(), MediaRequest{})
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.EqualError(t, err, "Immich returned an invalid response")
	require.NoError(t, response.Body.Close())
}

func TestThumbnailStreamsOnlyImageResponsesWithoutFollowingRedirects(t *testing.T) {
	assetID := uuid.New()
	t.Run("image", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/assets/"+assetID.String()+"/thumbnail", r.URL.Path)
			assert.Equal(t, "secret-key", r.Header.Get("x-api-key"))
			w.Header().Set("Content-Type", "image/webp")
			w.Header().Set("ETag", `"safe"`)
			_, _ = w.Write([]byte("thumbnail"))
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		response, err := client.Thumbnail(context.Background(), assetID, MediaRequest{})
		require.NoError(t, err)
		contents, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.Equal(t, "thumbnail", string(contents))
		assert.Equal(t, "image/webp", response.ContentType)
		assert.Equal(t, `"safe"`, response.ETag)
	})

	t.Run("not modified", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, `"thumbnail-v1"`, r.Header.Get("If-None-Match"))
			w.Header().Set("ETag", `"thumbnail-v1"`)
			w.WriteHeader(http.StatusNotModified)
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		response, err := client.Thumbnail(context.Background(), assetID, MediaRequest{IfNoneMatch: `"thumbnail-v1"`})
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotModified, response.StatusCode)
		assert.Equal(t, `"thumbnail-v1"`, response.ETag)
		require.NoError(t, response.Body.Close())
	})

	t.Run("redirect", func(t *testing.T) {
		redirected := false
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		_, err = client.Thumbnail(context.Background(), assetID, MediaRequest{})
		require.EqualError(t, err, "Immich validation failed")
		assert.False(t, redirected)
	})

	t.Run("oversized declared body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.FormatInt(maxThumbnailResponse+1, 10))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		_, err = client.Thumbnail(context.Background(), assetID, MediaRequest{})
		assert.EqualError(t, err, "Immich returned an invalid response")
	})

	t.Run("oversized chunked body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", maxThumbnailResponse+1)))
		}))
		defer server.Close()
		client, err := New(clientConfig(server.URL), server.Client())
		require.NoError(t, err)
		response, err := client.Thumbnail(context.Background(), assetID, MediaRequest{})
		require.NoError(t, err)
		contents, err := io.ReadAll(response.Body)
		require.EqualError(t, err, "Immich returned an invalid response")
		assert.Len(t, contents, maxThumbnailResponse)
		require.NoError(t, response.Body.Close())
	})

	for _, contentType := range []string{"application/json", "image/svg+xml"} {
		t.Run("unsafe "+contentType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte(`{"private":"metadata"}`))
			}))
			defer server.Close()
			client, err := New(clientConfig(server.URL), server.Client())
			require.NoError(t, err)
			_, err = client.Thumbnail(context.Background(), assetID, MediaRequest{})
			assert.EqualError(t, err, "Immich returned an invalid response")
		})
	}
}
