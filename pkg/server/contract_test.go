package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// The Mobile App is installed from app stores and meets whatever server
// version an operator runs, so the responses it reads are a contract: fields
// may be added, never renamed, removed, or changed in meaning. Each ticket
// that starts calling an endpoint from the app pins it here.
const contractNotice = "the Mobile App depends on this response; fields may be added but never renamed or removed, see docs/adr/0013-keep-the-mobile-app-viewer-api-additive.md"

func requireContract(t *testing.T, body []byte, fields ...string) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded), "%s: %s", contractNotice, body)
	for _, field := range fields {
		require.Contains(t, decoded, field, "%s is missing: %s", field, contractNotice)
	}
	return decoded
}

func TestMobileAppContract(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	cfg := config.NewForTest()
	cfg.AppEnv = "development"
	srv, err := New(cfg, db, Features{Publishing: publishing.New(db, nil, nil)})
	require.NoError(t, err)
	call := func(method, path, body, credential string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "localhost:3579"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		switch {
		case strings.HasPrefix(credential, "Bearer "):
			req.Header.Set("Authorization", credential)
		case credential != "":
			req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: credential})
		}
		// The browser sheet sends its Origin; the app has none to send.
		if credential == "" && path != "/api/identity/mobile/exchange" || credential != "" && !strings.HasPrefix(credential, "Bearer ") {
			req.Header.Set("Origin", cfg.PublicURL)
		}
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, req)
		return recorder
	}

	// Status is read before anyone signs in.
	response := call(http.MethodGet, "/api/identity/status", "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	requireContract(t, response.Body.Bytes(), "claimed", "person", "auth_mode", "version")

	// The web sign-in happens in the browser sheet, then hands the app a code.
	response = call(http.MethodPost, "/api/identity/fake-sign-in", `{"email":"curator@example.test","display_name":"Curator"}`, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, response.Result().Cookies(), 1)
	browser := response.Result().Cookies()[0].Value
	returnPath := "/api/identity/mobile/return?return_to=" + url.QueryEscape("exp://192.168.1.20:8081/--/sign-in")
	// A first-time Person finishes Onboarding on the web before the hand-off.
	response = call(http.MethodGet, returnPath, "", browser)
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())
	require.Equal(t, "/welcome?return_to="+url.QueryEscape("exp://192.168.1.20:8081/--/sign-in"), response.Header().Get("Location"))
	_, err = db.NewUpdate().Table("persons").Set("onboarding_completed_at = now()").Where("TRUE").Exec(t.Context())
	require.NoError(t, err)
	response = call(http.MethodGet, returnPath, "", browser)
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())
	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "exp", location.Scheme)
	code := location.Query().Get("code")
	require.Len(t, code, 43)

	response = call(http.MethodPost, "/api/identity/mobile/exchange", `{"code":"`+code+`","platform":"iPhone"}`, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, response.Result().Cookies(), "the app keeps its token itself")
	exchanged := requireContract(t, response.Body.Bytes(), "token", "person")
	token, _ := exchanged["token"].(string)
	require.Len(t, token, 43)
	bearer := "Bearer " + token
	response = call(http.MethodPost, "/api/identity/mobile/exchange", `{"code":"`+code+`","platform":"iPhone"}`, "")
	require.Equal(t, http.StatusUnauthorized, response.Code, "a code works once")

	personFields := []string{"id", "display_name", "is_curator", "onboarding_completed_at", "deactivated_at", "update_email", "email_updates", "avatar_url"}
	person, ok := exchanged["person"].(map[string]any)
	require.True(t, ok)
	for _, field := range personFields {
		require.Contains(t, person, field, "%s is missing: %s", field, contractNotice)
	}
	response = call(http.MethodGet, "/api/identity/me", "", bearer)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	requireContract(t, response.Body.Bytes(), personFields...)
	response = call(http.MethodGet, "/api/identity/status", "", bearer)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	status := requireContract(t, response.Body.Bytes(), "claimed", "person", "auth_mode", "version")
	require.NotNil(t, status["person"], "status reports the bearer session's Person")

	// The phone shows up in the Person's sessions with a label they recognize.
	response = call(http.MethodGet, "/api/identity/sessions", "", browser)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var sessions []map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &sessions))
	devices := []string{}
	for _, session := range sessions {
		devices = append(devices, session["device"].(string))
	}
	require.Contains(t, devices, "Memento on iPhone")

	// Albums list what the Person can see, with covers on the media routes.
	seedAlbum(t, db)
	response = call(http.MethodGet, "/api/albums", "", bearer)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var albums []json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &albums), contractNotice)
	require.Len(t, albums, 1)
	album := requireContract(t, albums[0], "id", "title", "description", "photo_count", "video_count", "start_date", "end_date", "cover_url", "cover_preview_url")
	cover, _ := album["cover_url"].(string)
	require.True(t, strings.HasPrefix(cover, "/api/media/viewer/"), "covers stay relative to the Installation: %s", cover)

	// Signing out ends the session on the server.
	response = call(http.MethodPost, "/api/identity/sign-out", `{}`, bearer)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.Empty(t, response.Result().Cookies())
	response = call(http.MethodGet, "/api/identity/me", "", bearer)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = call(http.MethodGet, "/api/identity/me", "", browser)
	require.Equal(t, http.StatusOK, response.Code, "the browser sheet's own session is untouched")
	response = call(http.MethodGet, "/api/identity/sessions", "", browser)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &sessions))
	require.False(t, slices.ContainsFunc(sessions, func(session map[string]any) bool { return session["device"] == "Memento on iPhone" }))
}

// seedAlbum is one complete imported Album with a single photo, enough for
// the viewer to list it with a cover.
func seedAlbum(t *testing.T, db *bun.DB) {
	t.Helper()
	album := models.Album{ID: models.NewUUIDv7(), SourceID: "album", Title: "Summer", ImportStatus: "complete", ImportTotal: 1, ImportProcessed: 1}
	moment := models.Moment{ID: models.NewUUIDv7(), AlbumID: album.ID, CaptureDate: "2026-07-04", SortOrder: 0}
	width, height := 4032, 3024
	item := models.MediaItem{
		ID: models.NewUUIDv7(), SourceID: "photo", Checksum: "photo", Filename: "photo.jpg", Kind: "IMAGE",
		CapturedAt: time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC), Width: &width, Height: &height,
		ContentVersion: media.ContentVersion(immich.Asset{ID: "photo", Checksum: "photo"}),
	}
	entry := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: item.ID, MomentID: &moment.ID}
	moment.CoverEntryID = entry.ID
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		for _, model := range []any{&album, &item, &moment, &entry} {
			if _, err := tx.NewInsert().Model(model).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}))
}
