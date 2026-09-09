package media_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type source struct {
	requested string
	asset     immich.Asset
}

func (s *source) CheckImport(context.Context) error                  { return nil }
func (s *source) ListAlbums(context.Context) ([]immich.Album, error) { return nil, nil }
func (s *source) ListMembers(context.Context, string, int) ([]immich.Asset, int, error) {
	return []immich.Asset{s.asset}, 0, nil
}
func (s *source) GetAsset(context.Context, string) (immich.Asset, error) { return s.asset, nil }
func (*source) ListFaces(context.Context, string) ([]immich.Face, error) { return nil, nil }
func (s *source) GetAlbum(_ context.Context, id string) (immich.Album, error) {
	cover := "configured-cover"
	return immich.Album{ID: id, Name: "Fixture", Count: 1, ThumbnailID: &cover}, nil
}
func (s *source) Thumbnail(_ context.Context, id string) (immich.Thumbnail, error) {
	s.requested = id
	return immich.Thumbnail{Body: io.NopCloser(strings.NewReader("generated image")), ContentType: "image/webp"}, nil
}

func TestImportedThumbnailUsesPrivateVersionAndMementoValidator(t *testing.T) {
	t.Parallel()
	upstream := &source{asset: immich.Asset{ID: "asset", Checksum: "YQ==", Filename: "photo.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}}
	db := testdb.New(t)
	imports := publishing.New(db, upstream, func(context.Context, bun.Tx, string) error { return nil })
	album, err := imports.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, imports.ExecuteImport(t.Context(), album.ID))
	album, err = imports.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	imageURL := album.Moments[0].Entries[0].ThumbnailURL
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	allowed := true
	media.RegisterRoutes(e, media.New(db, upstream), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !allowed {
				return echo.ErrForbidden
			}
			return next(c)
		}
	})
	request := func(path, etag string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-None-Match", etag)
		e.ServeHTTP(r, req)
		return r
	}
	first := request(imageURL, "")
	require.Equal(t, 200, first.Code)
	require.Equal(t, "private, max-age=31536000, immutable", first.Header().Get("Cache-Control"))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	upstream.requested = ""
	require.Equal(t, 304, request(imageURL, "W/"+etag).Code)
	require.Empty(t, upstream.requested)
	require.Equal(t, 404, request(strings.Split(imageURL, "?")[0]+"?v=wrong", "").Code)
	allowed = false
	require.Equal(t, 403, request(imageURL, etag).Code)
	allowed = true
	upstream.asset.UpdatedAt = "2026-07-06T00:00:00Z"
	require.Equal(t, 404, request(imageURL, "").Code, "a changed source must not serve new bytes under an old URL")
}

func TestSourceCoverUsesAlbumWithoutEntryAndRequiresCurator(t *testing.T) {
	t.Parallel()
	upstream := &source{}
	e := echo.New()
	authorized := false
	media.RegisterRoutes(e, media.New(testdb.New(t), upstream), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !authorized {
				return echo.ErrForbidden
			}
			return next(c)
		}
	})
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		e.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/media/sources/source-album/cover?url=https://attacker.test", nil))
		return r
	}
	require.Equal(t, 403, request().Code)
	require.Empty(t, upstream.requested)
	authorized = true
	response := request()
	require.Equal(t, 200, response.Code)
	require.Equal(t, "configured-cover", upstream.requested)
	require.Equal(t, "image/webp", response.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
}
