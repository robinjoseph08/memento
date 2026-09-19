package media_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type source struct {
	requested      string
	asset          immich.Asset
	assetError     error
	originalError  error
	originalLength int64
	originalType   string
	playback       func(context.Context, immich.PlaybackRequest) (immich.Playback, error)
	chapters       func(context.Context, string) ([]ffprobe.Chapter, error)
}

func (s *source) CheckImport(context.Context) error                  { return nil }
func (s *source) ListAlbums(context.Context) ([]immich.Album, error) { return nil, nil }
func (s *source) ListMembers(context.Context, string, int) ([]immich.Asset, int, error) {
	return []immich.Asset{s.asset}, 0, nil
}
func (s *source) GetAsset(context.Context, string) (immich.Asset, error) {
	return s.asset, s.assetError
}
func (*source) ListFaces(context.Context, string) ([]immich.Face, error) { return nil, nil }
func (s *source) GetAlbum(_ context.Context, id string) (immich.Album, error) {
	cover := "configured-cover"
	return immich.Album{ID: id, Name: "Fixture", Count: 1, ThumbnailID: &cover}, nil
}
func (s *source) Thumbnail(_ context.Context, id string) (immich.Thumbnail, error) {
	s.requested = id
	return immich.Thumbnail{Body: io.NopCloser(strings.NewReader("generated image")), ContentType: "image/webp"}, nil
}
func (s *source) Preview(_ context.Context, id string) (immich.Thumbnail, error) {
	s.requested = "preview:" + id
	return immich.Thumbnail{Body: io.NopCloser(strings.NewReader("large image")), ContentType: "image/jpeg"}, nil
}
func (s *source) Original(_ context.Context, id string) (immich.Original, error) {
	s.requested = "original:" + id
	if s.originalError != nil {
		return immich.Original{}, s.originalError
	}
	length := s.originalLength
	if length == 0 {
		length = 14
	}
	contentType := s.originalType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return immich.Original{Body: io.NopCloser(strings.NewReader("original bytes")), ContentType: contentType, Length: length}, nil
}
func (s *source) Playback(ctx context.Context, id string, request immich.PlaybackRequest) (immich.Playback, error) {
	s.requested = "playback:" + id
	if s.playback == nil {
		return immich.Playback{}, errors.New("playback fake not configured")
	}
	return s.playback(ctx, request)
}
func (s *source) Chapters(ctx context.Context, id string) ([]ffprobe.Chapter, error) {
	s.requested = "chapters:" + id
	if s.chapters == nil {
		return nil, errors.New("chapter fake not configured")
	}
	return s.chapters(ctx, id)
}
func (s *source) PersonThumbnail(_ context.Context, id string) (immich.Thumbnail, error) {
	s.requested = id
	return immich.Thumbnail{Body: io.NopCloser(strings.NewReader("person image")), ContentType: "image/jpeg"}, nil
}

func TestViewerThumbnailChecksIdentityAndPolicyBeforeConditionalResponse(t *testing.T) {
	t.Parallel()
	upstream := &source{asset: immich.Asset{ID: "asset", Checksum: "YQ==", Filename: "photo.jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}}
	db := testdb.New(t)
	imports := publishing.New(db, upstream, func(context.Context, bun.Tx, string) error { return nil })
	album, err := imports.StartImport(t.Context(), "source")
	require.NoError(t, err)
	require.NoError(t, imports.ExecuteImport(t.Context(), album.ID))
	album, err = imports.GetAlbum(t.Context(), album.ID)
	require.NoError(t, err)
	entry := album.Moments[0].Entries[0]
	version := strings.Split(entry.ThumbnailURL, "?v=")[1]
	actor := "alex"
	allowed := true
	var selected string
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("identity.person_id", actor); return next(c) }
	}
	curator := func(next echo.HandlerFunc) echo.HandlerFunc {
		return person(func(c *echo.Context) error {
			if actor != "curator" {
				return echo.ErrForbidden
			}
			return next(c)
		})
	}
	media.RegisterViewerRoutes(e, media.New(db, upstream), func(_ context.Context, actorID, previewID, entryID string) error {
		require.Equal(t, actor, actorID)
		require.Equal(t, entry.ID, entryID)
		selected = previewID
		if !allowed {
			return errcodes.NotFound("Thumbnail")
		}
		return nil
	}, person, curator)
	get := func(mode, id, etag string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/media/"+mode+"/"+id+"/entries/"+entry.ID+"/thumbnail?v="+version, nil)
		req.Header.Set("If-None-Match", etag)
		e.ServeHTTP(r, req)
		return r
	}
	first := get("viewer", "alex", "")
	require.Equal(t, 200, first.Code)
	require.Equal(t, "private, max-age=31536000, immutable", first.Header().Get("Cache-Control"))
	allowed = false
	require.Equal(t, 404, get("viewer", "alex", first.Header().Get("ETag")).Code)
	allowed = true
	require.Equal(t, 404, get("viewer", "sam", "").Code, "cannot borrow another Person's cache URL")
	require.Equal(t, 403, get("preview", "sam", "").Code)
	actor = "curator"
	preview := get("preview", "sam", "")
	require.Equal(t, 200, preview.Code)
	require.Equal(t, "sam", selected)
	require.Equal(t, "private, max-age=31536000, immutable", preview.Header().Get("Cache-Control"), "preview URLs name the selected Person, so caching cannot mix identities")
	large := httptest.NewRecorder()
	e.ServeHTTP(large, httptest.NewRequest(http.MethodGet, "/api/media/preview/sam/entries/"+entry.ID+"/preview?v="+version, nil))
	require.Equal(t, 200, large.Code)
	require.Equal(t, "large image", large.Body.String(), "the preview route serves Immich's larger variant")
	require.Equal(t, "preview:asset", upstream.requested)
	upstream.assetError = &errcodes.Error{HTTPCode: 403, Code: "immich_permission_denied", Message: "Enable asset.read on the Immich API key."}
	failed := get("viewer", "curator", "")
	require.Equal(t, http.StatusBadGateway, failed.Code)
	require.Contains(t, failed.Body.String(), "Media is unavailable. Try again later.")
	require.NotContains(t, failed.Body.String(), "Immich")
	require.NotContains(t, failed.Body.String(), "asset.read")
	preview = get("preview", "sam", "")
	require.Equal(t, http.StatusBadGateway, preview.Code)
	require.NotContains(t, preview.Body.String(), "Immich")
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
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !allowed {
				return echo.ErrForbidden
			}
			return next(c)
		}
	}
	media.RegisterRoutes(e, media.New(db, upstream), guard, guard)
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

func TestFaceAndAvatarThumbnailsRequireKnownAuthorizedRecords(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Now().UTC()
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "asset", Checksum: "face", Filename: "face.jpg", Kind: "IMAGE",
		CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "face"}
	_, err := db.NewInsert().Model(&item).Exec(t.Context())
	require.NoError(t, err)
	face := models.MediaFaceAssociation{MediaItemID: item.ID, SourceFaceID: "immich-person", SourceName: "Alex", SourceVersion: "2024-07-02T07:00:00Z"}
	_, err = db.NewInsert().Model(&face).Exec(t.Context())
	require.NoError(t, err)
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", CreatedAt: now}
	_, err = db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	personID := person.ID
	link := models.ImmichFaceLink{SourceID: "immich-person", PersonID: &personID, UpdatedAt: now}
	_, err = db.NewInsert().Model(&link).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model(&person).Set("avatar_face_id = ?", "immich-person").WherePK().Exec(t.Context())
	require.NoError(t, err)

	upstream := &source{}
	authorized := true
	actor, curator := "", false
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	requirePerson := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !authorized {
				return echo.ErrForbidden
			}
			c.Set("identity.person_id", actor)
			c.Set("identity.is_curator", curator)
			return next(c)
		}
	}
	requireCurator := func(next echo.HandlerFunc) echo.HandlerFunc {
		return requirePerson(func(c *echo.Context) error {
			if !curator {
				return echo.ErrForbidden
			}
			return next(c)
		})
	}
	media.RegisterRoutes(e, media.New(db, upstream), requirePerson, requireCurator)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	faceURL := media.FaceThumbnailURL("immich-person", "2024-07-02T07:00:00Z")
	avatarURL := media.AvatarURL(person.ID.String(), "immich-person", "2024-07-02T07:00:00Z")
	// An ordinary viewer reads only their own avatar; face thumbnails stay Curator-only.
	actor = person.ID.String()
	require.Equal(t, http.StatusForbidden, get(faceURL).Code)
	require.Equal(t, http.StatusOK, get(avatarURL).Code)
	require.Equal(t, "immich-person", upstream.requested)
	actor = models.NewUUIDv7().String()
	upstream.requested = ""
	require.Equal(t, http.StatusForbidden, get(avatarURL).Code, "another Person's avatar needs a Curator")
	require.Empty(t, upstream.requested)
	curator = true
	first := get(faceURL)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "private, max-age=31536000, immutable", first.Header().Get("Cache-Control"))
	require.Equal(t, "immich-person", upstream.requested)
	require.Equal(t, http.StatusOK, get(avatarURL).Code)
	require.Equal(t, "immich-person", upstream.requested)
	upstream.requested = ""
	require.Equal(t, http.StatusNotFound, get("/api/media/faces/arbitrary/thumbnail?v=2024-07-02T07%3A00%3A00Z").Code)
	require.Equal(t, http.StatusNotFound, get(media.FaceThumbnailURL("immich-person", "stale")).Code, "a superseded version never serves new bytes")
	require.Equal(t, http.StatusNotFound, get("/api/media/people/"+person.ID.String()+"/avatar?v=ignored").Code)
	require.Empty(t, upstream.requested)
	authorized = false
	require.Equal(t, http.StatusForbidden, get(faceURL).Code)
	require.Equal(t, http.StatusForbidden, get(avatarURL).Code)
}

func TestSourceCoverUsesAlbumWithoutEntryAndRequiresCurator(t *testing.T) {
	t.Parallel()
	upstream := &source{}
	e := echo.New()
	authorized := false
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !authorized {
				return echo.ErrForbidden
			}
			return next(c)
		}
	}
	media.RegisterRoutes(e, media.New(testdb.New(t), upstream), guard, guard)
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

func TestViewerOriginalDownloadsAuthorizedPhotosAndVideosWithoutRanges(t *testing.T) {
	t.Parallel()
	upstream := &source{asset: immich.Asset{ID: "asset", Checksum: "YQ==", Filename: "Lake sunset (edited).jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}}
	db := testdb.New(t)
	imports := publishing.New(db, upstream, func(context.Context, bun.Tx, string) error { return nil })
	importEntry := func(sourceAlbum string) publishing.Entry {
		album, err := imports.StartImport(t.Context(), sourceAlbum)
		require.NoError(t, err)
		require.NoError(t, imports.ExecuteImport(t.Context(), album.ID))
		album, err = imports.GetAlbum(t.Context(), album.ID)
		require.NoError(t, err)
		return album.Moments[0].Entries[0]
	}
	// The same Media Item joins two Albums as two Album Entries.
	allowed := importEntry("source-a")
	denied := importEntry("source-b")
	require.Equal(t, allowed.MediaID, denied.MediaID)
	upstream.asset = immich.Asset{ID: "clip", Checksum: "Yg==", Filename: "clip.mp4", Kind: "VIDEO", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	video := importEntry("source-c")
	upstream.asset = immich.Asset{ID: "asset", Checksum: "YQ==", Filename: "Lake sunset (edited).jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	version := strings.Split(allowed.ThumbnailURL, "?v=")[1]
	actor := "alex"
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	person := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("identity.person_id", actor); return next(c) }
	}
	curator := func(next echo.HandlerFunc) echo.HandlerFunc {
		return person(func(c *echo.Context) error {
			if actor != "curator" {
				return echo.ErrForbidden
			}
			return next(c)
		})
	}
	media.RegisterViewerRoutes(e, media.New(db, upstream), func(_ context.Context, actorID, previewID, entryID string) error {
		require.Empty(t, previewID, "downloads never run in a preview context")
		if actorID == "alex" && (entryID == allowed.ID || entryID == video.ID) {
			return nil
		}
		return errcodes.NotFound("Thumbnail")
	}, person, curator)
	do := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		e.ServeHTTP(r, req)
		return r
	}
	url := "/api/media/viewer/alex/entries/" + allowed.ID + "/original?v=" + version
	response := do(http.MethodGet, url, map[string]string{"Range": "bytes=0-3", "If-None-Match": "*"})
	require.Equal(t, http.StatusOK, response.Code, "photo delivery ignores ranges and validators")
	require.Equal(t, "original bytes", response.Body.String())
	require.Equal(t, "original:asset", upstream.requested)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "14", response.Header().Get("Content-Length"))
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Empty(t, response.Header().Get("Content-Range"))
	require.Empty(t, response.Header().Get("Accept-Ranges"))
	require.Empty(t, response.Header().Get("ETag"))
	disposition, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "attachment", disposition)
	require.Equal(t, "photo-2026-07-04-235900.jpg", params["filename"])
	_, err = db.NewUpdate().Model((*models.MediaItem)(nil)).Set("filename = ?", ".DSC_0123").Where("id = ?", allowed.MediaID).Exec(t.Context())
	require.NoError(t, err)
	unusual := do(http.MethodGet, url, nil)
	_, params, err = mime.ParseMediaType(unusual.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "photo-2026-07-04-235900", params["filename"], "an unusual source suffix is not exposed as an extension")
	upstream.requested = ""
	head := do(http.MethodHead, url, nil)
	require.Equal(t, http.StatusOK, head.Code)
	require.Empty(t, head.Body.String())
	require.Equal(t, "attachment", strings.Split(head.Header().Get("Content-Disposition"), ";")[0])
	require.Equal(t, "private, no-store", head.Header().Get("Cache-Control"))
	require.Empty(t, upstream.requested, "HEAD never opens the upstream stream")
	upstream.originalLength = -1
	chunked := do(http.MethodGet, url, nil)
	require.Equal(t, http.StatusOK, chunked.Code)
	require.Equal(t, "original bytes", chunked.Body.String())
	require.Empty(t, chunked.Header().Get("Content-Length"), "an unknown upstream length is not invented")
	upstream.originalLength = 14

	upstream.requested = ""
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/alex/entries/"+denied.ID+"/original?v="+version, nil).Code, "access to the Media Item through another Album does not authorize this Album Entry")
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/sam/entries/"+allowed.ID+"/original?v="+version, nil).Code, "another Person's URL is never served")
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/viewer/alex/entries/"+allowed.ID+"/original?v=wrong", nil).Code)
	require.Empty(t, upstream.requested)
	// A video downloads through the same route, named after its file.
	upstream.asset = immich.Asset{ID: "clip", Checksum: "Yg==", Filename: "clip.mp4", Kind: "VIDEO", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	upstream.originalType = "video/mp4"
	videoDownload := do(http.MethodGet, "/api/media/viewer/alex/entries/"+video.ID+"/original?v="+strings.Split(video.ThumbnailURL, "?v=")[1], map[string]string{"Range": "bytes=0-3"})
	require.Equal(t, http.StatusOK, videoDownload.Code)
	require.Equal(t, "original:clip", upstream.requested)
	require.Equal(t, "video/mp4", videoDownload.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", videoDownload.Header().Get("Cache-Control"))
	require.Empty(t, videoDownload.Header().Get("Content-Range"), "downloads stay range-independent")
	_, params, err = mime.ParseMediaType(videoDownload.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "clip.mp4", params["filename"])
	upstream.asset = immich.Asset{ID: "asset", Checksum: "YQ==", Filename: "Lake sunset (edited).jpg", Kind: "IMAGE", LocalDateTime: "2026-07-04T23:59:00Z", FileCreatedAt: "2026-07-04T23:59:00Z", UpdatedAt: "2026-07-05T00:00:00Z"}
	upstream.originalType = ""
	upstream.requested = ""
	actor = "curator"
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/media/preview/alex/entries/"+allowed.ID+"/original?v="+version, nil).Code, "Curator preview cannot download")
	actor = "alex"
	upstream.originalError = &errcodes.Error{HTTPCode: 403, Code: "immich_permission_denied", Message: "Enable asset.download on the Immich API key."}
	failed := do(http.MethodGet, url, nil)
	require.Equal(t, http.StatusBadGateway, failed.Code)
	require.Contains(t, failed.Body.String(), "Media is unavailable. Try again later.")
	require.NotContains(t, failed.Body.String(), "Immich")
	require.NotContains(t, failed.Body.String(), "asset.download")
	upstream.originalError = nil
	upstream.asset.UpdatedAt = "2026-07-06T00:00:00Z"
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, url, nil).Code, "a changed source never serves new bytes under an old URL")
}
