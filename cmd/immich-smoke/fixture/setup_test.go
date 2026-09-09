package fixture

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJPEG(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i, photo := range Photos() {
		data, err := JPEG(photo, i)
		require.NoError(t, err)
		_, err = jpeg.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.False(t, seen[string(data)])
		seen[string(data)] = true
		require.Equal(t, "Exif\x00\x00", string(data[6:12]))
		tiff := data[12:]
		require.Equal(t, uint32(26), binary.LittleEndian.Uint32(tiff[18:]))
		require.Equal(t, uint16(2), binary.LittleEndian.Uint16(tiff[26:]))
		values := map[uint16]string{}
		for j := range 2 {
			entry := 28 + j*12
			tag := binary.LittleEndian.Uint16(tiff[entry:])
			length := binary.LittleEndian.Uint32(tiff[entry+4:])
			offset := binary.LittleEndian.Uint32(tiff[entry+8:])
			values[tag] = string(tiff[offset : offset+length-1])
		}
		require.Equal(t, strings.ReplaceAll(photo.CapturedAt[:10], "-", ":")+" "+photo.CapturedAt[11:19], values[0x9003])
		require.Equal(t, "-07:00", values[0x9011])
	}
}

func TestSetupUsesAdminOnlyToCreateNonAdminSource(t *testing.T) {
	t.Parallel()
	for _, release := range []string{"v3.1.0", "v3.0.3"} {
		t.Run(release, func(t *testing.T) {
			t.Parallel()
			for _, ids := range [][]string{{"asset-1", "asset-2", "asset-3", "asset-4"}, {"asset-1", "asset-3", "asset-2", "asset-4"}} {
				t.Run(strings.Join(ids, ","), func(t *testing.T) {
					t.Parallel()
					testSetup(t, release, ids)
				})
			}
		})
	}
}

func testSetup(t *testing.T, release string, assetIDs []string) {
	t.Helper()
	calls := []string{}
	uploads := 0
	albumCount := 0
	covers := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/server/version":
			version := `{"major":3,"minor":1,"patch":0,"prerelease":null}`
			if release == "v3.0.3" {
				version = `{"major":3,"minor":0,"patch":3,"prerelease":null}`
			}
			_, _ = io.WriteString(w, version)
		case "/api/auth/admin-sign-up":
			_, _ = io.WriteString(w, `{}`)
		case "/api/auth/login":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid login body")
			}
			token := "source-token"
			if body["email"] == "admin@memento.invalid" {
				token = "admin-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": token})
		case "/api/admin/users":
			if r.Header.Get("Authorization") != "Bearer admin-token" {
				t.Error("user creation must use admin session")
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid user body")
			}
			if body["isAdmin"] != false {
				t.Error("source owner must be nonadmin")
			}
			_, _ = io.WriteString(w, `{"isAdmin":false}`)
		case "/api/assets":
			if r.Header.Get("Authorization") != "Bearer source-token" {
				t.Error("uploads must use source session")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			file, _, err := r.FormFile("assetData")
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = file.Close() }()
			if _, err := jpeg.Decode(file); err != nil {
				t.Error(err)
			}
			if r.FormValue("fileCreatedAt") != "2020-01-01T00:00:00Z" {
				t.Error("upload timestamp must differ from EXIF")
			}
			uploads++
			_ = json.NewEncoder(w).Encode(map[string]string{"id": assetIDs[uploads-1], "status": "created"})
		case "/api/albums":
			if r.Header.Get("Authorization") != "Bearer source-token" {
				t.Error("album creation must use source session")
			}
			var body struct {
				AssetIDs []string `json:"assetIds"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid album body")
			}
			expected := assetIDs
			if albumCount == 1 {
				expected = assetIDs[1:3]
			}
			if !reflect.DeepEqual(expected, body.AssetIDs) {
				t.Error("wrong overlapping membership")
			}
			albumCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("album-%d", albumCount)})
		case "/api/albums/album-1", "/api/albums/album-2":
			if r.Method != http.MethodPatch || r.Header.Get("Authorization") != "Bearer source-token" {
				t.Error("album cover must be updated by PATCH using source session")
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid cover body")
			}
			if !reflect.DeepEqual(map[string]string{"albumThumbnailAssetId": "asset-3"}, body) {
				t.Error("cover update must select only the later source ID from the equal-time pair")
			}
			covers[strings.TrimPrefix(r.URL.Path, "/api/albums/")] = body["albumThumbnailAssetId"]
			_, _ = io.WriteString(w, `{}`)
		case "/api/api-keys":
			if r.Header.Get("Authorization") != "Bearer source-token" {
				t.Error("read key must belong to source owner")
			}
			var body struct {
				Permissions []string `json:"permissions"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid key body")
			}
			if !reflect.DeepEqual(readPermissions, body.Permissions) {
				t.Error("key permissions must be exact")
			}
			_, _ = io.WriteString(w, `{"secret":"private-read-key","apiKey":{"permissions":["asset.view","album.read","asset.read"]}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	library, err := Setup(context.Background(), server.URL, release)
	require.NoError(t, err)
	require.Len(t, library.Assets, 4)
	require.Len(t, library.Albums, 2)
	require.Equal(t, map[string]string{"album-1": "asset-3", "album-2": "asset-3"}, covers)
	for _, album := range library.Albums {
		require.Equal(t, "asset-3", album.CoverAssetID)
	}
	require.Equal(t, []string{"GET /api/server/version", "GET /api/server/version", "POST /api/auth/admin-sign-up", "POST /api/auth/login", "POST /api/admin/users", "POST /api/auth/login"}, calls[:6])
	require.Equal(t, "POST /api/api-keys", calls[len(calls)-1])
	require.NotNil(t, library.Source())
}

func TestSetupRejectsWrongReleaseBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, expected, response, failure string
	}{
		{"wrong default", "v3.1.0", `{"major":3,"minor":0,"patch":3,"prerelease":null}`, "expected stable v3.1.0"},
		{"wrong explicit", "v3.0.3", `{"major":3,"minor":1,"patch":0,"prerelease":null}`, "expected stable v3.0.3"},
		{"prerelease", "v3.0.3", `{"major":3,"minor":0,"patch":3,"prerelease":1}`, "expected stable v3.0.3"},
		{"unsupported despite exact match", "v3.2.0", `{"major":3,"minor":2,"patch":0,"prerelease":null}`, "Import requires stable Immich"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/server/version" {
					t.Error("wrote before release gate")
				}
				_, _ = io.WriteString(w, test.response)
			}))
			t.Cleanup(server.Close)
			_, err := Setup(t.Context(), server.URL, test.expected)
			require.ErrorContains(t, err, test.failure)
		})
	}
}

func TestSetupRejectsProductionOrigin(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"https://photos.example.com", "http://127.0.0.1/api", "http://user:secret@127.0.0.1", "http://127.0.0.1?key=secret"} {
		_, err := Setup(context.Background(), origin, Release)
		require.ErrorContains(t, err, "disposable loopback")
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestFixtureErrorsDoNotExposeUpstreamSecrets(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"secret":"never-print-this","accessToken":"or-this"}`)
	}))
	t.Cleanup(server.Close)
	_, err := Setup(t.Context(), server.URL, Release)
	require.ErrorContains(t, err, "HTTP 401")
	require.NotContains(t, err.Error(), "never-print-this")
	require.NotContains(t, err.Error(), "or-this")
}
