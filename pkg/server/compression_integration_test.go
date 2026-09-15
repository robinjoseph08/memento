package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestLargeAlbumAndGalleryCompression(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	actor := models.Person{ID: models.NewUUIDv7(), DisplayName: "Curator", IsCurator: true}
	album := models.Album{ID: models.NewUUIDv7(), SourceID: "large-album", Title: "Family trip", ImportStatus: "complete", ImportTotal: 6105, ImportProcessed: 6105}
	moments := make([]models.Moment, 5)
	for day := range moments {
		moments[day] = models.Moment{ID: models.NewUUIDv7(), AlbumID: album.ID, CaptureDate: fmt.Sprintf("2026-07-%02d", day+1), SortOrder: int64(day)}
	}
	items := make([]models.MediaItem, 6105)
	entries := make([]models.AlbumEntry, len(items))
	for i := range items {
		day := i / 1221
		sourceID := fmt.Sprintf("photo-%04d", i)
		width, height := 6000, 4000
		if i%3 == 0 {
			width, height = height, width
		}
		items[i] = models.MediaItem{
			ID: models.NewUUIDv7(), SourceID: sourceID, Checksum: sourceID, Filename: sourceID + ".jpg", Kind: "IMAGE",
			CapturedAt: time.Date(2026, 7, day+1, 12, 0, i%1221, 0, time.UTC), Width: &width, Height: &height,
			ContentVersion: media.ContentVersion(immich.Asset{ID: sourceID, Checksum: sourceID}),
		}
		entries[i] = models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: items[i].ID, MomentID: &moments[day].ID}
		if i%1221 == 0 {
			moments[day].CoverEntryID = entries[i].ID
		}
	}
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		// Deferred cover constraints allow the circular Moment/Entry fixture.
		for _, model := range []any{&actor, &album, &items, &moments, &entries} {
			if _, err := tx.NewInsert().Model(model).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}))
	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("identity.person_id", actor.ID.String())
			return next(c)
		}
	}
	publishing.RegisterViewerRoutes(srv.Handler.(*echo.Echo), publishing.New(db, nil, nil), person, person)
	measure := func(path string) ([]byte, int, int) {
		t.Helper()
		plain := compressionRequest(srv.Handler, path, "")
		compressed := compressionRequest(srv.Handler, path, "gzip")
		require.Equal(t, http.StatusOK, plain.Code)
		require.Equal(t, plain.Code, compressed.Code)
		require.Equal(t, "gzip", compressed.Header().Get("Content-Encoding"))
		require.Contains(t, compressed.Header().Values("Vary"), "Accept-Encoding")
		require.Equal(t, "no-store", compressed.Header().Get("Cache-Control"))
		compressedSize := compressed.Body.Len()
		require.Equal(t, plain.Body.Bytes(), decodedBody(t, compressed))
		return plain.Body.Bytes(), plain.Body.Len(), compressedSize
	}
	path := "/api/albums/" + album.ID.String()
	body, plainSize, gzipSize := measure(path)
	var detail publishing.ViewerAlbum
	require.NoError(t, json.Unmarshal(body, &detail))
	require.Equal(t, 6105, detail.PhotoCount)
	t.Logf("Album, 6,105 photos: %d bytes identity, %d bytes gzip", plainSize, gzipSize)
	plainTotal, gzipTotal, count := 0, 0, 0
	cursor := ""
	for {
		body, plainSize, gzipSize := measure(path + "/photos?from=2026-07-01&to=2026-07-02&cursor=" + url.QueryEscape(cursor))
		plainTotal += plainSize
		gzipTotal += gzipSize
		var page publishing.ViewerPage
		require.NoError(t, json.Unmarshal(body, &page))
		count += len(page.Entries)
		if page.NextCursor == "" {
			break
		}
		require.NotEqual(t, cursor, page.NextCursor)
		cursor = page.NextCursor
	}
	require.Equal(t, 1221, count)
	require.Less(t, gzipTotal, plainTotal)
	t.Logf("Gallery day, 1,221 photos across three pages: %d bytes identity, %d bytes gzip", plainTotal, gzipTotal)
}
