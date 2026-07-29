//go:build immichcontract

package immich

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImmichV303LiveContract(t *testing.T) {
	baseURL := os.Getenv("MEMENTO_TEST_IMMICH_URL")
	if baseURL == "" {
		t.Fatal("MEMENTO_TEST_IMMICH_URL is required")
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	ctx := context.Background()

	contractPOST(t, ctx, httpClient, baseURL+"/api/auth/admin-sign-up", "", map[string]any{
		"email": "memento-contract@example.com", "password": "test-password-123", "name": "Memento Contract",
	}, http.StatusCreated, nil)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	contractPOST(t, ctx, httpClient, baseURL+"/api/auth/login", "", map[string]any{
		"email": "memento-contract@example.com", "password": "test-password-123",
	}, http.StatusCreated, &login)
	require.NotEmpty(t, login.AccessToken)

	createKey := func(permissions []string) string {
		var response struct {
			Secret string `json:"secret"`
		}
		contractPOST(t, ctx, httpClient, baseURL+"/api/api-keys", login.AccessToken, map[string]any{
			"name": "Memento v3.0.3 contract", "permissions": permissions,
		}, http.StatusCreated, &response)
		require.NotEmpty(t, response.Secret)
		return response.Secret
	}

	apiKey := createKey(requiredPermissions)
	client, err := New(config.ImmichConfig{
		URL: baseURL, APIKey: apiKey, HealthTimeout: 10 * time.Second,
	}, httpClient)
	require.NoError(t, err)
	require.NoError(t, client.Check(ctx))

	invalidKey := createKey([]string{"all"})
	invalidClient, err := New(config.ImmichConfig{
		URL: baseURL, APIKey: invalidKey, HealthTimeout: 10 * time.Second,
	}, httpClient)
	require.NoError(t, err)
	err = invalidClient.Check(ctx)
	require.ErrorIs(t, err, errInvalidPermissions)

	var createdAlbum struct {
		ID string `json:"id"`
	}
	contractPOST(t, ctx, httpClient, baseURL+"/api/albums", login.AccessToken, map[string]any{
		"albumName": "  Contract album  ", "description": " Normalized description ",
	}, http.StatusCreated, &createdAlbum)
	albumID, err := uuid.Parse(createdAlbum.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		contractDELETE(t, ctx, httpClient, baseURL+"/api/albums/"+albumID.String(), login.AccessToken, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest)
	})

	albums, err := client.OwnedAlbums(ctx)
	require.NoError(t, err)
	require.Len(t, albums, 1)
	assert.Equal(t, albumID, albums[0].SourceID)
	assert.Equal(t, "Contract album", albums[0].Name)
	assert.Equal(t, "Normalized description", albums[0].Description)
	assert.Zero(t, albums[0].AssetCount)

	album, err := client.Album(ctx, albumID)
	require.NoError(t, err)
	assert.Equal(t, albums[0], album)

	page, err := client.AlbumAssetsPage(ctx, albumID, 1)
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	assert.Nil(t, page.NextPage)

	contractDELETE(t, ctx, httpClient, baseURL+"/api/albums/"+albumID.String(), login.AccessToken, http.StatusNoContent)
	_, err = client.Album(ctx, albumID)
	require.ErrorIs(t, err, ErrNotFound, "a real deleted album must reach reconciliation as missing evidence")
	_, err = client.AlbumAssetsPage(ctx, albumID, 1)
	require.ErrorIs(t, err, ErrNotFound, "v3.0.3 rejects metadata search for a deleted album as missing evidence")

	people, err := client.People(ctx)
	require.NoError(t, err)
	assert.Empty(t, people)

	imagePath := filepath.Join("testdata", "contract.jpg")
	videoPath := filepath.Join("testdata", "contract.mp4")
	imageBytes, err := os.ReadFile(imagePath)
	require.NoError(t, err)
	imageID := contractUpload(t, ctx, httpClient, baseURL, login.AccessToken, imagePath)
	videoID := contractUpload(t, ctx, httpClient, baseURL, login.AccessToken, videoPath)
	contractAwaitDelivery(t, func() (bool, error) {
		return client.AssetDeliveryAvailable(ctx, imageID, "image")
	})
	contractAwaitDelivery(t, func() (bool, error) {
		return client.AssetDeliveryAvailable(ctx, videoID, "video")
	})

	thumbnail := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Thumbnail(ctx, imageID, MediaRequest{})
	})
	assert.Equal(t, http.StatusOK, thumbnail.StatusCode)
	assert.NotEmpty(t, contractReadMedia(t, thumbnail))

	preview := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Preview(ctx, imageID, MediaRequest{})
	})
	assert.Equal(t, http.StatusOK, preview.StatusCode)
	assert.NotEmpty(t, contractReadMedia(t, preview))

	original := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, imageID, MediaRequest{})
	})
	assert.Equal(t, http.StatusOK, original.StatusCode)
	assert.Equal(t, imageBytes, contractReadMedia(t, original), "the live original contract preserves every uploaded byte")
	require.NotEmpty(t, original.ETag)

	notModified := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, imageID, MediaRequest{IfNoneMatch: original.ETag})
	})
	assert.Equal(t, http.StatusNotModified, notModified.StatusCode)
	assert.Empty(t, contractReadMedia(t, notModified))

	originalRange := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, imageID, MediaRequest{Range: "bytes=0-7"})
	})
	assert.Equal(t, http.StatusPartialContent, originalRange.StatusCode)
	assert.Equal(t, imageBytes[:8], contractReadMedia(t, originalRange))

	videoRange := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Video(ctx, videoID, MediaRequest{Range: "bytes=0-7"})
	})
	assert.Equal(t, http.StatusPartialContent, videoRange.StatusCode)
	assert.Equal(t, int64(8), videoRange.ContentLength)
	assert.Len(t, contractReadMedia(t, videoRange), 8)

	fullVideo := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Video(ctx, videoID, MediaRequest{Range: "bytes=0-7", IfRange: `"stale"`})
	})
	assert.Equal(t, http.StatusOK, fullVideo.StatusCode)
	assert.Empty(t, fullVideo.ContentRange)
	assert.Greater(t, len(contractReadMedia(t, fullVideo)), 8, "an If-Range mismatch returns the complete playback representation")

	archiveParts, err := client.ArchiveInfo(ctx, []uuid.UUID{imageID, videoID})
	require.NoError(t, err)
	require.NotEmpty(t, archiveParts)
	var archivedIDs []uuid.UUID
	for _, part := range archiveParts {
		archivedIDs = append(archivedIDs, part.AssetIDs...)
	}
	assert.ElementsMatch(t, []uuid.UUID{imageID, videoID}, archivedIDs)
	archiveResponse, err := client.Archive(ctx, archivedIDs)
	require.NoError(t, err)
	archiveBytes, err := io.ReadAll(archiveResponse.Body)
	require.NoError(t, err)
	require.NoError(t, archiveResponse.Body.Close())
	zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	require.NoError(t, err)
	require.Len(t, zipReader.File, 2)
	for _, file := range zipReader.File {
		assert.Equal(t, filepath.Base(file.Name), file.Name, "Immich archive entries never expose a source path")
	}

	contractDeleteAssets(t, ctx, httpClient, baseURL, login.AccessToken, []uuid.UUID{imageID, videoID}, http.StatusNoContent, http.StatusNotFound)
	for _, deletedID := range []uuid.UUID{imageID, videoID} {
		contractAwaitAssetDeleted(t, func() (bool, error) { return client.AssetExists(ctx, deletedID) })
	}
	deletedRepresentations := []struct {
		name string
		load func() (MediaResponse, error)
	}{
		{name: "thumbnail", load: func() (MediaResponse, error) { return client.Thumbnail(ctx, imageID, MediaRequest{}) }},
		{name: "preview", load: func() (MediaResponse, error) { return client.Preview(ctx, imageID, MediaRequest{}) }},
		{name: "original", load: func() (MediaResponse, error) { return client.Original(ctx, imageID, MediaRequest{}) }},
		{name: "video", load: func() (MediaResponse, error) { return client.Video(ctx, videoID, MediaRequest{}) }},
	}
	for _, representation := range deletedRepresentations {
		t.Run("deleted asset "+representation.name, func(t *testing.T) {
			response, loadErr := representation.load()
			if response.Body != nil {
				_ = response.Body.Close()
			}
			require.ErrorIs(t, loadErr, ErrNotFound)
		})
	}
}

func contractUpload(t *testing.T, ctx context.Context, client *http.Client, baseURL, bearer, path string) uuid.UUID {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("fileCreatedAt", "2026-07-27T12:00:00.000Z"))
	require.NoError(t, writer.WriteField("fileModifiedAt", "2026-07-27T12:00:00.000Z"))
	require.NoError(t, writer.WriteField("filename", filepath.Base(path)))
	part, err := writer.CreateFormFile("assetData", filepath.Base(path))
	require.NoError(t, err)
	_, err = part.Write(contents)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/assets", &body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		require.Equal(t, http.StatusCreated, response.StatusCode, "upload %s: %q", path, responseBody)
	}
	var uploaded struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&uploaded))
	assert.Equal(t, "created", uploaded.Status)
	id, err := uuid.Parse(uploaded.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		contractDeleteAssets(t, ctx, client, baseURL, bearer, []uuid.UUID{id}, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest)
	})
	return id
}

func contractAwaitMedia(t *testing.T, load func() (MediaResponse, error)) MediaResponse {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		response, err := load()
		if err == nil {
			return response
		}
		lastErr = err
		if time.Now().After(deadline) {
			require.NoError(t, lastErr, "media did not become available before the contract deadline")
		}
		<-ticker.C
	}
}

func contractAwaitDelivery(t *testing.T, load func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastAvailable bool
	var lastErr error
	for {
		lastAvailable, lastErr = load()
		if lastErr == nil && lastAvailable {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "asset delivery did not become available", "last_available=%t last_error=%v", lastAvailable, lastErr)
		}
		<-ticker.C
	}
}

func contractAwaitAssetDeleted(t *testing.T, load func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastExists bool
	var lastErr error
	for {
		lastExists, lastErr = load()
		if lastErr == nil && !lastExists {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "deleted asset remained available", "last_exists=%t last_error=%v", lastExists, lastErr)
		}
		<-ticker.C
	}
}

func contractReadMedia(t *testing.T, response MediaResponse) []byte {
	t.Helper()
	contents, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return contents
}

func contractDELETE(t *testing.T, ctx context.Context, client *http.Client, endpoint, bearer string, wantStatuses ...int) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	require.NoError(t, err)
	require.Contains(t, wantStatuses, response.StatusCode, "%s: %q", endpoint, contents)
}

func contractDeleteAssets(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL, bearer string,
	assetIDs []uuid.UUID,
	wantStatuses ...int,
) {
	t.Helper()
	ids := make([]string, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		ids = append(ids, assetID.String())
	}
	encoded, err := json.Marshal(map[string]any{"ids": ids, "force": true})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/api/assets", bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	require.NoError(t, err)
	require.Contains(t, wantStatuses, response.StatusCode, "delete assets: %q", contents)
}

func contractPOST(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint, bearer string,
	body any,
	wantStatus int,
	target any,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		require.Equal(t, wantStatus, response.StatusCode, "%s: %s", endpoint, fmt.Sprintf("%q", contents))
	}
	if target != nil {
		require.NoError(t, json.NewDecoder(response.Body).Decode(target))
	}
}
