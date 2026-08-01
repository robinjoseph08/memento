//go:build immichcontract

package immich

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun/driver/pgdriver"
)

const (
	contractEmail          = "memento-contract@example.com"
	contractPassword       = "test-password-123"
	contractExternalPath   = "/external"
	contractPaginationSize = assetPageSize + 1
)

type contractSignUpRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type contractLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type contractEmptyResponse struct{}

type contractLoginResponse struct {
	AccessToken string `json:"accessToken"`
	UserID      string `json:"userId"`
}

type contractAPIKeyCreateRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type contractAPIKeyResponse struct {
	ID string `json:"id"`
}

type contractAPIKeyCreateResponse struct {
	Secret string                 `json:"secret"`
	APIKey contractAPIKeyResponse `json:"apiKey"`
}

type contractAlbumCreateRequest struct {
	AlbumName   string   `json:"albumName"`
	Description string   `json:"description"`
	AssetIDs    []string `json:"assetIds"`
}

type contractAlbumCreateResponse struct {
	ID string `json:"id"`
}

type contractLibraryCreateRequest struct {
	OwnerID           string   `json:"ownerId"`
	Name              string   `json:"name"`
	ImportPaths       []string `json:"importPaths"`
	ExclusionPatterns []string `json:"exclusionPatterns"`
}

type contractLibraryCreateResponse struct {
	ID string `json:"id"`
}

type contractAssetDeleteRequest struct {
	IDs   []string `json:"ids"`
	Force bool     `json:"force"`
}

type contractUploadResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type contractUploadFixture struct {
	Path             string
	CreatedAt        string
	LivePhotoVideoID *uuid.UUID
}

type contractHarness struct {
	baseURL   string
	admin     string
	http      *http.Client
	database  *sql.DB
	ownerID   uuid.UUID
	cleanupDB context.Context
}

func TestImmichV303LiveContract(t *testing.T) {
	baseURL := os.Getenv("MEMENTO_TEST_IMMICH_URL")
	if baseURL == "" {
		t.Fatal("MEMENTO_TEST_IMMICH_URL is required")
	}
	databaseURL := os.Getenv("MEMENTO_TEST_IMMICH_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("MEMENTO_TEST_IMMICH_DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	httpClient := &http.Client{Timeout: 15 * time.Second}

	contractPOSTJSON[contractSignUpRequest, contractEmptyResponse](t, ctx, httpClient, baseURL+"/api/auth/admin-sign-up", "", contractSignUpRequest{
		Email: contractEmail, Password: contractPassword, Name: "Memento Contract",
	}, http.StatusCreated, nil)
	var login contractLoginResponse
	contractPOSTJSON(t, ctx, httpClient, baseURL+"/api/auth/login", "", contractLoginRequest{
		Email: contractEmail, Password: contractPassword,
	}, http.StatusCreated, &login)
	if login.AccessToken == "" {
		t.Fatal("login response omitted the access token")
	}
	ownerID, err := uuid.Parse(login.UserID)
	if err != nil || ownerID == uuid.Nil {
		t.Fatal("login response omitted a valid owner identity")
	}

	database := contractOpenDatabase(t, ctx, databaseURL)
	cleanupCtx := context.Background()
	harness := &contractHarness{
		baseURL: baseURL, admin: login.AccessToken, http: httpClient,
		database: database, ownerID: ownerID, cleanupDB: cleanupCtx,
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error("contract database close failed")
		}
	})

	exactKey := harness.createAPIKey(t, ctx, append([]string(nil), requiredPermissions...))
	overPermissions := append(append([]string(nil), requiredPermissions...), "album.create")
	overKey := harness.createAPIKey(t, ctx, overPermissions)
	underPermissions := append([]string(nil), requiredPermissions[:len(requiredPermissions)-1]...)
	underKey := harness.createAPIKey(t, ctx, underPermissions)

	client := contractNewClient(t, baseURL, exactKey, httpClient)
	require.NoError(t, client.Check(ctx))

	validationCases := []struct {
		name    string
		key     string
		wantErr error
	}{
		{name: "overprivileged", key: overKey, wantErr: errInvalidPermissions},
		{name: "underprivileged", key: underKey, wantErr: errInvalidPermissions},
		{name: "nonexistent", key: "never-issued-contract-key", wantErr: errInvalidCredentials},
	}
	for _, validationCase := range validationCases {
		t.Run(validationCase.name+" key", func(t *testing.T) {
			invalidClient := contractNewClient(t, baseURL, validationCase.key, httpClient)
			err := invalidClient.Check(ctx)
			require.ErrorIs(t, err, validationCase.wantErr)
			assert.True(t, IsConfigurationError(err))
		})
	}

	ownedImagePath := filepath.Join("testdata", "contract.jpg")
	ownedVideoPath := filepath.Join("testdata", "contract.mp4")
	liveMotionPath := filepath.Join("testdata", "live-motion.mp4")
	liveStillPath := filepath.Join("testdata", "live-still.jpg")
	externalPath := filepath.Join("testdata", "external", "external.jpg")
	ownedImageBytes := contractReadFixture(t, ownedImagePath)
	ownedVideoBytes := contractReadFixture(t, ownedVideoPath)
	liveMotionBytes := contractReadFixture(t, liveMotionPath)
	liveStillBytes := contractReadFixture(t, liveStillPath)
	externalBytes := contractReadFixture(t, externalPath)

	ownedImageID := harness.upload(t, ctx, contractUploadFixture{
		Path: ownedImagePath, CreatedAt: "2026-07-27T12:00:00.000Z",
	})
	ownedVideoID := harness.upload(t, ctx, contractUploadFixture{
		Path: ownedVideoPath, CreatedAt: "2026-07-27T12:00:01.000Z",
	})
	liveMotionID := harness.upload(t, ctx, contractUploadFixture{
		Path: liveMotionPath, CreatedAt: "2026-07-27T12:00:02.000Z",
	})
	liveStillID := harness.upload(t, ctx, contractUploadFixture{
		Path: liveStillPath, CreatedAt: "2026-07-27T12:00:03.000Z", LivePhotoVideoID: &liveMotionID,
	})

	externalLibraryID := harness.createExternalLibrary(t, ctx)
	externalID := harness.awaitExternalAsset(t, ctx, externalLibraryID)

	archiveExpectedIDs := []uuid.UUID{ownedImageID, ownedVideoID, liveStillID, liveMotionID, externalID}
	// Immich represents the hidden Live Photo motion asset through its still in
	// album membership. ArchiveInfo must restore that companion explicitly.
	representativeMediaIDs := []uuid.UUID{ownedImageID, ownedVideoID, liveStillID, externalID}
	albumID := harness.createAlbum(t, ctx, representativeMediaIDs)

	personID, assignedFaceID, unassignedFaceID := harness.seedFaces(t, ctx, ownedImageID)
	t.Cleanup(func() { harness.deletePerson(t, personID) })

	directIDs := harness.seedPaginationAssets(t, ctx, albumID, ownedImageID, contractPaginationSize-len(representativeMediaIDs))
	t.Cleanup(func() { harness.deletePaginationAssets(t) })

	albums, err := client.OwnedAlbums(ctx)
	require.NoError(t, err)
	if len(albums) != 1 {
		t.Fatalf("album discovery returned %d albums, expected 1", len(albums))
	}
	contractRequireSameID(t, albums[0].SourceID, albumID, "owned album identity mismatch")
	if albums[0].Name != "Contract album" || albums[0].Description != "Normalized description" {
		t.Fatal("owned album text normalization mismatch")
	}
	assert.Equal(t, contractPaginationSize, albums[0].AssetCount)

	album, err := client.Album(ctx, albumID)
	require.NoError(t, err)
	contractRequireSameID(t, album.SourceID, albumID, "album identity mismatch")
	assert.Equal(t, contractPaginationSize, album.AssetCount)

	firstPage, err := client.AlbumAssetsPage(ctx, albumID, 1)
	require.NoError(t, err)
	if len(firstPage.Items) != assetPageSize {
		t.Fatalf("first album page returned %d members, expected %d", len(firstPage.Items), assetPageSize)
	}
	if firstPage.NextPage == nil || *firstPage.NextPage != 2 {
		t.Fatal("first album page omitted the expected continuation")
	}
	secondPage, err := client.AlbumAssetsPage(ctx, albumID, *firstPage.NextPage)
	require.NoError(t, err)
	if len(secondPage.Items) != 1 {
		t.Fatalf("second album page returned %d members, expected 1", len(secondPage.Items))
	}
	if secondPage.NextPage != nil {
		t.Fatal("final album page returned an unexpected continuation")
	}
	contractRequireCompleteMembership(t, firstPage.Items, secondPage.Items, representativeMediaIDs, directIDs)

	people, err := client.People(ctx)
	require.NoError(t, err)
	if len(people) != 1 {
		t.Fatalf("People discovery returned %d records, expected 1", len(people))
	}
	contractRequireSameID(t, people[0].SourceID, personID, "person identity mismatch")
	if people[0].Name != "Contract Person" || people[0].Hidden {
		t.Fatal("person normalization mismatch")
	}

	faces, err := client.Faces(ctx, ownedImageID)
	require.NoError(t, err)
	if len(faces) != 2 {
		t.Fatalf("face discovery returned %d records, expected 2", len(faces))
	}
	contractRequireFace(t, faces[0], assignedFaceID, &personID, 101, 57, 301, 257)
	contractRequireFace(t, faces[1], unassignedFaceID, nil, 350, 80, 550, 300)

	representatives := []struct {
		id        uuid.UUID
		mediaType string
	}{
		{id: ownedImageID, mediaType: "image"},
		{id: ownedVideoID, mediaType: "video"},
		{id: liveStillID, mediaType: "image"},
		{id: liveMotionID, mediaType: "video"},
		{id: externalID, mediaType: "image"},
	}
	for _, representative := range representatives {
		evidence, err := client.Asset(ctx, representative.id)
		require.NoError(t, err)
		contractRequireSameID(t, evidence.SourceID, representative.id, "asset identity mismatch")
		if evidence.MediaType != representative.mediaType {
			t.Fatal("asset media type mismatch")
		}
		if evidence.Checksum == "" || evidence.Filename == "" || evidence.OriginalPath == "" {
			t.Fatal("asset metadata omitted required evidence")
		}
		exists, err := client.AssetExists(ctx, representative.id)
		require.NoError(t, err)
		if !exists {
			t.Fatal("representative asset did not exist")
		}
	}

	imageFixtures := []struct {
		id       uuid.UUID
		contents []byte
	}{
		{id: ownedImageID, contents: ownedImageBytes},
		{id: liveStillID, contents: liveStillBytes},
		{id: externalID, contents: externalBytes},
	}
	imageETags := make(map[uuid.UUID]string, len(imageFixtures))
	for _, fixture := range imageFixtures {
		contractAwaitDelivery(t, func() (bool, error) {
			return client.AssetDeliveryAvailable(ctx, fixture.id, "image")
		})
		imageETags[fixture.id] = contractExerciseImageMedia(t, ctx, client, fixture.id, fixture.contents)
	}

	contractAwaitDelivery(t, func() (bool, error) {
		return client.AssetDeliveryAvailable(ctx, ownedVideoID, "video")
	})
	contractExerciseVideoMedia(t, ctx, client, ownedVideoID, ownedVideoBytes)

	// Immich serves a Live Photo's hidden motion companion through archive and
	// original download instead of the ordinary encoded-video playback route.
	liveMotionOriginal := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, liveMotionID, MediaRequest{})
	})
	contractRequireMediaStatus(t, liveMotionOriginal, http.StatusOK)
	if !bytes.Equal(liveMotionBytes, contractReadMedia(t, liveMotionOriginal)) {
		t.Fatal("Live Photo motion original differed from the deterministic fixture")
	}

	emailThumbnail := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.EmailThumbnail(ctx, ownedImageID, MediaRequest{})
	})
	contractRequireMediaStatus(t, emailThumbnail, http.StatusOK)
	if len(contractReadMedia(t, emailThumbnail)) == 0 {
		t.Fatal("email thumbnail response was empty")
	}

	ownedImageETag := imageETags[ownedImageID]
	if ownedImageETag == "" {
		t.Fatal("original response omitted its validator")
	}
	notModified := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, ownedImageID, MediaRequest{IfNoneMatch: ownedImageETag})
	})
	contractRequireMediaStatus(t, notModified, http.StatusNotModified)
	if len(contractReadMedia(t, notModified)) != 0 {
		t.Fatal("not-modified response returned a body")
	}

	originalRange := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, ownedImageID, MediaRequest{Range: "bytes=0-7"})
	})
	contractRequireMediaStatus(t, originalRange, http.StatusPartialContent)
	if !bytes.Equal(ownedImageBytes[:8], contractReadMedia(t, originalRange)) {
		t.Fatal("live original range bytes differed from the uploaded fixture")
	}

	fullVideo := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Video(ctx, ownedVideoID, MediaRequest{Range: "bytes=0-7", IfRange: `"stale"`})
	})
	contractRequireMediaStatus(t, fullVideo, http.StatusOK)
	if fullVideo.ContentRange != "" {
		t.Fatal("If-Range mismatch retained a partial content range")
	}
	if len(contractReadMedia(t, fullVideo)) <= 8 {
		t.Fatal("If-Range mismatch did not return the complete playback representation")
	}

	archiveRequested := []uuid.UUID{ownedImageID, ownedVideoID, liveStillID, externalID}
	archiveParts, err := client.ArchiveInfo(ctx, archiveRequested)
	require.NoError(t, err)
	contractRequireArchiveExpansion(t, archiveParts, archiveExpectedIDs, liveStillID, liveMotionID)
	contractReadCompleteArchive(t, ctx, client, archiveParts, [][]byte{
		ownedImageBytes, ownedVideoBytes, liveStillBytes, liveMotionBytes, externalBytes,
	})

	contractDELETEStatus(t, ctx, httpClient, baseURL+"/api/albums/"+albumID.String(), login.AccessToken,
		http.StatusNoContent)
	_, err = client.Album(ctx, albumID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = client.AlbumAssetsPage(ctx, albumID, 1)
	require.ErrorIs(t, err, ErrNotFound)
	harness.deletePaginationAssets(t)

	harness.deleteLibrary(t, ctx, externalLibraryID, http.StatusNoContent)
	contractAwaitAssetDeleted(t, func() (bool, error) { return client.AssetExists(ctx, externalID) })

	ownedIDs := []uuid.UUID{ownedImageID, ownedVideoID, liveStillID, liveMotionID}
	harness.deleteAssets(t, ctx, ownedIDs, http.StatusNoContent)
	for _, deletedID := range ownedIDs {
		contractAwaitAssetDeleted(t, func() (bool, error) { return client.AssetExists(ctx, deletedID) })
		_, err = client.Asset(ctx, deletedID)
		require.ErrorIs(t, err, ErrNotFound)
	}

	deletedRepresentations := []struct {
		name string
		load func() (MediaResponse, error)
	}{
		{name: "thumbnail", load: func() (MediaResponse, error) { return client.Thumbnail(ctx, ownedImageID, MediaRequest{}) }},
		{name: "preview", load: func() (MediaResponse, error) { return client.Preview(ctx, ownedImageID, MediaRequest{}) }},
		{name: "original", load: func() (MediaResponse, error) { return client.Original(ctx, ownedImageID, MediaRequest{}) }},
		{name: "video", load: func() (MediaResponse, error) { return client.Video(ctx, ownedVideoID, MediaRequest{}) }},
	}
	for _, representation := range deletedRepresentations {
		t.Run("deleted asset "+representation.name, func(t *testing.T) {
			contractAwaitMediaDeleted(t, representation.load)
		})
	}
}

func contractOpenDatabase(t *testing.T, ctx context.Context, databaseURL string) *sql.DB {
	t.Helper()
	connector, err := pgdriver.NewDriver().OpenConnector(databaseURL)
	if err != nil {
		t.Fatal("contract database connector creation failed")
	}
	database := sql.OpenDB(connector)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := database.PingContext(pingCtx); err != nil {
		_ = database.Close()
		t.Fatal("contract database did not become reachable")
	}
	return database
}

func contractNewClient(t *testing.T, baseURL, key string, httpClient *http.Client) *Client {
	t.Helper()
	client, err := New(config.ImmichConfig{
		URL: baseURL, APIKey: key, HealthTimeout: 10 * time.Second,
	}, httpClient)
	if err != nil {
		t.Fatal("contract adapter creation failed")
	}
	return client
}

func (h *contractHarness) createAPIKey(t *testing.T, ctx context.Context, permissions []string) string {
	t.Helper()
	var response contractAPIKeyCreateResponse
	contractPOSTJSON(t, ctx, h.http, h.baseURL+"/api/api-keys", h.admin, contractAPIKeyCreateRequest{
		Name: "Memento v3.0.3 contract", Permissions: permissions,
	}, http.StatusCreated, &response)
	if response.Secret == "" {
		t.Fatal("API key creation omitted the secret")
	}
	keyID, err := uuid.Parse(response.APIKey.ID)
	if err != nil || keyID == uuid.Nil {
		t.Fatal("API key creation omitted a valid identity")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 15*time.Second)
		defer cancel()
		contractDELETEStatus(t, cleanupCtx, h.http, h.baseURL+"/api/api-keys/"+keyID.String(), h.admin,
			http.StatusNoContent, http.StatusNotFound)
	})
	return response.Secret
}

func (h *contractHarness) upload(t *testing.T, ctx context.Context, fixture contractUploadFixture) uuid.UUID {
	t.Helper()
	contents := contractReadFixture(t, fixture.Path)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	contractWriteField(t, writer, "fileCreatedAt", fixture.CreatedAt)
	contractWriteField(t, writer, "fileModifiedAt", fixture.CreatedAt)
	contractWriteField(t, writer, "filename", filepath.Base(fixture.Path))
	if fixture.LivePhotoVideoID != nil {
		contractWriteField(t, writer, "livePhotoVideoId", fixture.LivePhotoVideoID.String())
	}
	part, err := writer.CreateFormFile("assetData", filepath.Base(fixture.Path))
	if err != nil {
		t.Fatal("fixture upload form creation failed")
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal("fixture upload form write failed")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("fixture upload form close failed")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/api/assets", &body)
	if err != nil {
		t.Fatal("fixture upload request creation failed")
	}
	request.Header.Set("Authorization", "Bearer "+h.admin)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := h.http.Do(request)
	if err != nil {
		t.Fatal("fixture upload request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		t.Fatalf("fixture upload returned status %d", response.StatusCode)
	}
	var uploaded contractUploadResponse
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal("fixture upload response decoding failed")
	}
	if uploaded.Status != "created" {
		t.Fatal("fixture upload did not create a new asset")
	}
	id, err := uuid.Parse(uploaded.ID)
	if err != nil || id == uuid.Nil {
		t.Fatal("fixture upload omitted a valid asset identity")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 15*time.Second)
		defer cancel()
		h.deleteAssets(t, cleanupCtx, []uuid.UUID{id}, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest)
	})
	return id
}

func contractWriteField(t *testing.T, writer *multipart.Writer, name, value string) {
	t.Helper()
	if err := writer.WriteField(name, value); err != nil {
		t.Fatal("fixture upload field write failed")
	}
}

func contractReadFixture(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("contract fixture read failed")
	}
	return contents
}

func (h *contractHarness) createExternalLibrary(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	var response contractLibraryCreateResponse
	contractPOSTJSON(t, ctx, h.http, h.baseURL+"/api/libraries", h.admin, contractLibraryCreateRequest{
		OwnerID: h.ownerID.String(), Name: "Memento contract external library",
		ImportPaths: []string{contractExternalPath}, ExclusionPatterns: []string{},
	}, http.StatusCreated, &response)
	libraryID, err := uuid.Parse(response.ID)
	if err != nil || libraryID == uuid.Nil {
		t.Fatal("external library creation omitted a valid identity")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 30*time.Second)
		defer cancel()
		h.deleteLibrary(t, cleanupCtx, libraryID, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest)
	})
	contractPOSTStatus(t, ctx, h.http, h.baseURL+"/api/libraries/"+libraryID.String()+"/scan", h.admin,
		http.StatusNoContent)
	return libraryID
}

func (h *contractHarness) awaitExternalAsset(t *testing.T, ctx context.Context, libraryID uuid.UUID) uuid.UUID {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastCount := 0
	for {
		var rawID string
		var count int
		err := h.database.QueryRowContext(pollCtx, `
			SELECT
				count(*),
				COALESCE((
					SELECT id::text
					FROM asset
					WHERE "libraryId" = $1 AND status = 'active' AND "deletedAt" IS NULL
					ORDER BY id
					LIMIT 1
				), '')
			FROM asset
			WHERE "libraryId" = $1 AND status = 'active' AND "deletedAt" IS NULL
		`, libraryID).Scan(&count, &rawID)
		if err == nil {
			lastCount = count
			if count == 1 {
				id, parseErr := uuid.Parse(rawID)
				if parseErr == nil && id != uuid.Nil {
					return id
				}
				t.Fatal("external scan produced an invalid asset identity")
			}
			if count > 1 {
				t.Fatalf("external scan discovered an unexpected asset count: %d", count)
			}
		} else if pollCtx.Err() == nil {
			t.Fatal("external asset discovery query failed")
		}
		select {
		case <-pollCtx.Done():
			t.Fatalf("external asset discovery timed out with count %d", lastCount)
		case <-ticker.C:
		}
	}
}

func (h *contractHarness) createAlbum(t *testing.T, ctx context.Context, assetIDs []uuid.UUID) uuid.UUID {
	t.Helper()
	rawIDs := make([]string, len(assetIDs))
	for index, assetID := range assetIDs {
		rawIDs[index] = assetID.String()
	}
	var response contractAlbumCreateResponse
	contractPOSTJSON(t, ctx, h.http, h.baseURL+"/api/albums", h.admin, contractAlbumCreateRequest{
		AlbumName: "  Contract album  ", Description: " Normalized description ", AssetIDs: rawIDs,
	}, http.StatusCreated, &response)
	albumID, err := uuid.Parse(response.ID)
	if err != nil || albumID == uuid.Nil {
		t.Fatal("album creation omitted a valid identity")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 15*time.Second)
		defer cancel()
		contractDELETEStatus(t, cleanupCtx, h.http, h.baseURL+"/api/albums/"+albumID.String(), h.admin,
			http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest)
	})
	return albumID
}

func (h *contractHarness) seedFaces(t *testing.T, ctx context.Context, assetID uuid.UUID) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transaction, err := h.database.BeginTx(seedCtx, nil)
	if err != nil {
		t.Fatal("face seed transaction creation failed")
	}
	defer transaction.Rollback()

	personID := uuid.MustParse("a11ce000-0000-4000-8000-000000000001")
	assignedFaceID := uuid.MustParse("face0000-0000-4000-8000-000000000001")
	unassignedFaceID := uuid.MustParse("face0000-0000-4000-8000-000000000002")
	result, err := transaction.ExecContext(seedCtx, `
		INSERT INTO person (id, "ownerId", name, "isHidden")
		SELECT $1, "ownerId", $2, false
		FROM asset
		WHERE id = $3
	`, personID, "  Contract Person  ", assetID)
	if err != nil {
		t.Fatal("pinned face schema rejected the person seed")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		t.Fatal("API-created asset parent was unavailable for face seeding")
	}
	_, err = transaction.ExecContext(seedCtx, `
		INSERT INTO asset_face (
			id, "assetId", "personId", "imageWidth", "imageHeight",
			"boundingBoxX1", "boundingBoxY1", "boundingBoxX2", "boundingBoxY2", "sourceType"
		) VALUES
			($1, $2, $3, 640, 480, 101, 57, 301, 257, 'manual'),
			($4, $2, NULL, 640, 480, 350, 80, 550, 300, 'manual')
	`, assignedFaceID, assetID, personID, unassignedFaceID)
	if err != nil {
		t.Fatal("pinned face schema rejected the face seed")
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal("face seed commit failed")
	}
	return personID, assignedFaceID, unassignedFaceID
}

func (h *contractHarness) deletePerson(t *testing.T, personID uuid.UUID) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 10*time.Second)
	defer cancel()
	if _, err := h.database.ExecContext(cleanupCtx, `DELETE FROM person WHERE id = $1`, personID); err != nil {
		t.Error("person cleanup failed")
	}
}

func (h *contractHarness) seedPaginationAssets(
	t *testing.T,
	ctx context.Context,
	albumID, parentAssetID uuid.UUID,
	count int,
) []uuid.UUID {
	t.Helper()
	seedCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	transaction, err := h.database.BeginTx(seedCtx, nil)
	if err != nil {
		t.Fatal("pagination seed transaction creation failed")
	}
	defer transaction.Rollback()

	result, err := transaction.ExecContext(seedCtx, `
		INSERT INTO asset (
			id, "ownerId", type, "originalPath", "fileCreatedAt", "fileModifiedAt",
			checksum, "originalFileName", "localDateTime", "createdAt", "updatedAt", "checksumAlgorithm"
		)
		SELECT
			('90000000-0000-4000-8000-' || lpad(series::text, 12, '0'))::uuid,
			parent."ownerId", 'IMAGE',
			'/contract-pagination/' || lpad(series::text, 4, '0') || '.jpg',
			timestamptz '2025-01-01 00:00:00+00' + series * interval '1 microsecond',
			timestamptz '2025-02-01 00:00:00+00' + series * interval '1 microsecond',
			decode(lpad(to_hex(series), 40, '0'), 'hex'),
			'pagination-' || lpad(series::text, 4, '0') || '.jpg',
			timestamptz '2025-03-01 00:00:00+00' + series * interval '1 microsecond',
			timestamptz '2025-04-01 00:00:00+00' + series * interval '1 microsecond',
			timestamptz '2025-05-01 00:00:00+00' + series * interval '1 microsecond',
			parent."checksumAlgorithm"
		FROM asset AS parent
		CROSS JOIN generate_series(1, $1) AS series
		WHERE parent.id = $2
	`, count, parentAssetID)
	if err != nil {
		t.Fatal("pinned asset schema rejected the metadata-only pagination seed")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || int(rowsAffected) != count {
		t.Fatal("pagination seed did not find its API-created asset parent")
	}

	result, err = transaction.ExecContext(seedCtx, `
		INSERT INTO album_asset ("albumId", "assetId", "createdAt", "updatedAt")
		SELECT $1, id, "createdAt", "updatedAt"
		FROM asset
		WHERE "ownerId" = $2 AND "originalPath" LIKE '/contract-pagination/%'
	`, albumID, h.ownerID)
	if err != nil {
		t.Fatal("pinned album membership schema rejected the pagination seed")
	}
	rowsAffected, err = result.RowsAffected()
	if err != nil || int(rowsAffected) != count {
		t.Fatal("pagination membership seed did not find its API-created parents")
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal("pagination seed commit failed")
	}

	ids := make([]uuid.UUID, count)
	for index := range ids {
		ids[index] = uuid.MustParse("90000000-0000-4000-8000-" + leftPadDecimal(index+1, 12))
	}
	return ids
}

func leftPadDecimal(value, width int) string {
	raw := strconv.Itoa(value)
	return string(bytes.Repeat([]byte{'0'}, width-len(raw))) + raw
}

func (h *contractHarness) deletePaginationAssets(t *testing.T) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(h.cleanupDB, 15*time.Second)
	defer cancel()
	if _, err := h.database.ExecContext(cleanupCtx, `
		DELETE FROM asset
		WHERE "ownerId" = $1 AND "originalPath" LIKE '/contract-pagination/%'
	`, h.ownerID); err != nil {
		t.Error("pagination asset cleanup failed")
	}
}

func (h *contractHarness) deleteAssets(t *testing.T, ctx context.Context, assetIDs []uuid.UUID, wantStatuses ...int) {
	t.Helper()
	if len(assetIDs) == 0 {
		return
	}
	ids := make([]string, len(assetIDs))
	for index, assetID := range assetIDs {
		ids[index] = assetID.String()
	}
	contractDELETEJSONStatus(t, ctx, h.http, h.baseURL+"/api/assets", h.admin,
		contractAssetDeleteRequest{IDs: ids, Force: true}, wantStatuses...)
}

func (h *contractHarness) deleteLibrary(t *testing.T, ctx context.Context, libraryID uuid.UUID, wantStatuses ...int) {
	t.Helper()
	contractDELETEStatus(t, ctx, h.http, h.baseURL+"/api/libraries/"+libraryID.String(), h.admin, wantStatuses...)
}

func contractRequireCompleteMembership(
	t *testing.T,
	first, second []AssetSummary,
	representativeIDs, directIDs []uuid.UUID,
) {
	t.Helper()
	seen := make(map[uuid.UUID]struct{}, len(first)+len(second))
	for _, asset := range append(append([]AssetSummary(nil), first...), second...) {
		if _, duplicate := seen[asset.SourceID]; duplicate {
			t.Fatal("pagination returned a duplicate asset identity")
		}
		seen[asset.SourceID] = struct{}{}
	}
	if len(seen) != contractPaginationSize {
		t.Fatalf("pagination returned %d unique members, expected %d", len(seen), contractPaginationSize)
	}
	for _, id := range append(append([]uuid.UUID(nil), representativeIDs...), directIDs...) {
		if _, found := seen[id]; !found {
			t.Fatal("pagination omitted an expected member")
		}
	}
}

func contractRequireFace(
	t *testing.T,
	face FaceSummary,
	faceID uuid.UUID,
	personID *uuid.UUID,
	x1, y1, x2, y2 int,
) {
	t.Helper()
	contractRequireSameID(t, face.SourceID, faceID, "face identity mismatch")
	if (face.PersonID == nil) != (personID == nil) {
		t.Fatal("face person assignment mismatch")
	}
	if personID != nil {
		contractRequireSameID(t, *face.PersonID, *personID, "face person identity mismatch")
	}
	if face.ImageWidth != 640 || face.ImageHeight != 480 || face.X1 != x1 || face.Y1 != y1 || face.X2 != x2 || face.Y2 != y2 {
		t.Fatal("face bounds mismatch")
	}
}

func contractRequireArchiveExpansion(
	t *testing.T,
	parts []ArchivePart,
	expected []uuid.UUID,
	liveStillID, liveMotionID uuid.UUID,
) {
	t.Helper()
	seen := make(map[uuid.UUID]struct{}, len(expected))
	companionFound := false
	for _, part := range parts {
		for _, assetID := range part.AssetIDs {
			if _, duplicate := seen[assetID]; duplicate {
				t.Fatal("archive plan returned a duplicate asset identity")
			}
			seen[assetID] = struct{}{}
		}
		if primary, found := part.CompanionOf[liveMotionID]; found {
			contractRequireSameID(t, primary, liveStillID, "Live Photo companion parent mismatch")
			companionFound = true
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("archive plan returned %d members, expected %d", len(seen), len(expected))
	}
	for _, expectedID := range expected {
		if _, found := seen[expectedID]; !found {
			t.Fatal("archive plan omitted an expected representative")
		}
	}
	if !companionFound {
		t.Fatal("archive plan omitted the Live Photo companion relationship")
	}
}

func contractExerciseImageMedia(
	t *testing.T,
	ctx context.Context,
	client *Client,
	assetID uuid.UUID,
	expectedOriginal []byte,
) string {
	t.Helper()
	thumbnail := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Thumbnail(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, thumbnail, http.StatusOK)
	if len(contractReadMedia(t, thumbnail)) == 0 {
		t.Fatal("image thumbnail response was empty")
	}
	preview := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Preview(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, preview, http.StatusOK)
	if len(contractReadMedia(t, preview)) == 0 {
		t.Fatal("image preview response was empty")
	}
	original := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, original, http.StatusOK)
	if !bytes.Equal(expectedOriginal, contractReadMedia(t, original)) {
		t.Fatal("image original bytes differed from the deterministic fixture")
	}
	return original.ETag
}

func contractExerciseVideoMedia(
	t *testing.T,
	ctx context.Context,
	client *Client,
	assetID uuid.UUID,
	expectedOriginal []byte,
) {
	t.Helper()
	thumbnail := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Thumbnail(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, thumbnail, http.StatusOK)
	if len(contractReadMedia(t, thumbnail)) == 0 {
		t.Fatal("video thumbnail response was empty")
	}
	preview := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Preview(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, preview, http.StatusOK)
	if len(contractReadMedia(t, preview)) == 0 {
		t.Fatal("video preview response was empty")
	}
	original := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Original(ctx, assetID, MediaRequest{})
	})
	contractRequireMediaStatus(t, original, http.StatusOK)
	if !bytes.Equal(expectedOriginal, contractReadMedia(t, original)) {
		t.Fatal("video original bytes differed from the deterministic fixture")
	}
	playback := contractAwaitMedia(t, func() (MediaResponse, error) {
		return client.Video(ctx, assetID, MediaRequest{Range: "bytes=0-7"})
	})
	contractRequireMediaStatus(t, playback, http.StatusPartialContent)
	if playback.ContentLength != 8 || len(contractReadMedia(t, playback)) != 8 {
		t.Fatal("video playback range did not return eight bytes")
	}
}

func contractReadCompleteArchive(
	t *testing.T,
	ctx context.Context,
	client *Client,
	parts []ArchivePart,
	expectedContents [][]byte,
) {
	t.Helper()
	expected := make(map[[sha256.Size]byte]int, len(expectedContents))
	for _, contents := range expectedContents {
		expected[sha256.Sum256(contents)]++
	}
	actual := make(map[[sha256.Size]byte]int, len(expectedContents))
	files := 0
	for _, part := range parts {
		response, err := client.Archive(ctx, part.AssetIDs)
		require.NoError(t, err)
		archiveBytes, err := io.ReadAll(response.Body)
		if err != nil {
			_ = response.Body.Close()
			t.Fatal("archive stream read failed")
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal("archive stream close failed")
		}
		zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
		if err != nil {
			t.Fatal("archive stream was not a valid ZIP")
		}
		for _, file := range zipReader.File {
			if filepath.Base(file.Name) != file.Name {
				t.Fatal("archive entry exposed a source path")
			}
			entry, err := file.Open()
			if err != nil {
				t.Fatal("archive entry open failed")
			}
			contents, err := io.ReadAll(entry)
			closeErr := entry.Close()
			if err != nil || closeErr != nil {
				t.Fatal("archive entry read failed")
			}
			actual[sha256.Sum256(contents)]++
			files++
		}
	}
	if files != len(expectedContents) {
		t.Fatalf("complete archive returned %d files, expected %d", files, len(expectedContents))
	}
	if len(actual) != len(expected) {
		t.Fatal("complete archive returned unexpected fixture contents")
	}
	for checksum, count := range expected {
		if actual[checksum] != count {
			t.Fatal("complete archive omitted deterministic fixture contents")
		}
	}
}

func contractRequireMediaStatus(t *testing.T, response MediaResponse, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("media response returned status %d, expected %d", response.StatusCode, expected)
	}
}

func contractRequireSameID(t *testing.T, actual, expected uuid.UUID, message string) {
	t.Helper()
	if actual != expected {
		t.Fatal(message)
	}
}

func contractAwaitMedia(t *testing.T, load func() (MediaResponse, error)) MediaResponse {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastStatus := 0
	for {
		response, err := load()
		lastStatus = response.StatusCode
		if err == nil {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("media did not become available before the contract deadline, last status %d", lastStatus)
		}
		<-ticker.C
	}
}

func contractAwaitDelivery(t *testing.T, load func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastAvailable := false
	lastFailed := false
	for {
		available, err := load()
		lastAvailable = available
		lastFailed = err != nil
		if err == nil && available {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("asset delivery timed out, last available %t, last request failed %t", lastAvailable, lastFailed)
		}
		<-ticker.C
	}
}

func contractAwaitAssetDeleted(t *testing.T, load func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastExists := true
	lastFailed := false
	for {
		exists, err := load()
		lastExists = exists
		lastFailed = err != nil
		if err == nil && !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("asset deletion timed out, last exists %t, last request failed %t", lastExists, lastFailed)
		}
		<-ticker.C
	}
}

func contractAwaitMediaDeleted(t *testing.T, load func() (MediaResponse, error)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	attempts := 0
	lastStatus := 0
	for {
		attempts++
		response, err := load()
		lastStatus = response.StatusCode
		if response.Body != nil {
			_ = response.Body.Close()
		}
		if errors.Is(err, ErrNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("representation deletion timed out after %d attempts, last status %d", attempts, lastStatus)
		}
		<-ticker.C
	}
}

func contractReadMedia(t *testing.T, response MediaResponse) []byte {
	t.Helper()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		_ = response.Body.Close()
		t.Fatal("media response read failed")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal("media response close failed")
	}
	return contents
}

func contractPOSTJSON[Request, Response any](
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint, bearer string,
	body Request,
	wantStatus int,
	target *Response,
) {
	t.Helper()
	contractJSONRequest(t, ctx, client, http.MethodPost, endpoint, bearer, body, []int{wantStatus}, target)
}

func contractDELETEJSONStatus[Request any](
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint, bearer string,
	body Request,
	wantStatuses ...int,
) {
	t.Helper()
	contractJSONRequest[Request, contractUploadResponse](t, ctx, client, http.MethodDelete, endpoint, bearer, body, wantStatuses, nil)
}

func contractJSONRequest[Request, Response any](
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	method, endpoint, bearer string,
	body Request,
	wantStatuses []int,
	target *Response,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal("contract request encoding failed")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal("contract request creation failed")
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("contract request failed")
	}
	defer response.Body.Close()
	if !containsStatus(wantStatuses, response.StatusCode) {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		t.Fatalf("contract request returned status %d", response.StatusCode)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal("contract response decoding failed")
		}
	}
}

func contractPOSTStatus(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint, bearer string,
	wantStatuses ...int,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		t.Fatal("contract request creation failed")
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	contractDoStatusRequest(t, client, request, wantStatuses)
}

func contractDELETEStatus(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint, bearer string,
	wantStatuses ...int,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		t.Fatal("contract request creation failed")
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	contractDoStatusRequest(t, client, request, wantStatuses)
}

func contractDoStatusRequest(t *testing.T, client *http.Client, request *http.Request, wantStatuses []int) {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("contract request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if !containsStatus(wantStatuses, response.StatusCode) {
		t.Fatalf("contract request returned status %d", response.StatusCode)
	}
}

func containsStatus(statuses []int, status int) bool {
	for _, allowed := range statuses {
		if status == allowed {
			return true
		}
	}
	return false
}
