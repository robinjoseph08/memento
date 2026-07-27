//go:build immichcontract

package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

	people, err := client.People(ctx)
	require.NoError(t, err)
	assert.Empty(t, people)
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
