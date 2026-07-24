package immich

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestAlbumAssetsPageUsesPinnedPaginationAndStripsSensitiveDTOFields(t *testing.T) {
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
	assert.Nil(t, page.NextPage)
	serialized, err := json.Marshal(page)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "secret")
	assert.NotContains(t, string(serialized), "face")
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
		"duplicate asset":             `{"assets":{"count":2,"items":[` + validAsset + `,` + validAsset + `],"nextPage":null,"total":2}}`,
		"bad asset":                   `{"assets":{"count":1,"items":[{"id":"private",` + validAssetFields + `}],"nextPage":null,"total":1}}`,
		"bad type":                    `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"PRIVATE","width":1200,"height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing width":               `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","height":800,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing height":              `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"localDateTime":"2026-01-01T10:00:00Z"}],"nextPage":null,"total":1}}`,
		"missing local date time":     `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800}],"nextPage":null,"total":1}}`,
		"null local date time":        `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":null}],"nextPage":null,"total":1}}`,
		"bad local date time":         `{"assets":{"count":1,"items":[{"id":"` + uuid.NewString() + `","type":"IMAGE","width":1200,"height":800,"localDateTime":"yesterday"}],"nextPage":null,"total":1}}`,
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
		name string
		run  func(*Client) error
		want string
	}{
		{"validation", func(client *Client) error { return client.Check(context.Background()) }, "Immich validation failed"},
		{"discovery", func(client *Client) error { _, err := client.OwnedAlbums(context.Background()); return err }, "Immich album discovery failed"},
		{"membership", func(client *Client) error {
			_, err := client.AlbumAssetsPage(context.Background(), uuid.New(), 1)
			return err
		}, "Immich album membership lookup failed"},
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
					require.EqualError(t, err, operation.want)
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
