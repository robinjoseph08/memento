// Package media resolves authorized media requests to fixed Immich-generated variants.
package media

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/ffprobe"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// Source exposes source album lookup, generated image variants, originals,
// ranged playback, and chapter probing.
type Source interface {
	GetAlbum(context.Context, string) (immich.Album, error)
	GetAsset(context.Context, string) (immich.Asset, error)
	Thumbnail(context.Context, string) (immich.Thumbnail, error)
	Preview(context.Context, string) (immich.Thumbnail, error)
	Original(context.Context, string) (immich.Original, error)
	Playback(context.Context, string, immich.PlaybackRequest) (immich.Playback, error)
	Chapters(context.Context, string) ([]ffprobe.Chapter, error)
	PersonThumbnail(context.Context, string) (immich.Thumbnail, error)
}

type Module struct {
	db     *bun.DB
	source Source
	// EnqueueChapters commits extraction tasks with their rows. Leave it nil
	// only where no chapter work is ever requested, such as media-only tests.
	EnqueueChapters EnqueueChapters
}

func New(db *bun.DB, source Source) *Module { return &Module{db: db, source: source} }

func (m *Module) SourceCover(ctx context.Context, albumID string) (immich.Thumbnail, error) {
	album, err := m.source.GetAlbum(ctx, albumID)
	if err != nil {
		return immich.Thumbnail{}, err
	}
	if album.ThumbnailID == nil || *album.ThumbnailID == "" {
		return immich.Thumbnail{}, errcodes.NotFound("Cover")
	}
	return m.source.Thumbnail(ctx, *album.ThumbnailID)
}

// SourceAssetThumbnail reads an asset's generated thumbnail straight from
// Immich for a Curator reviewing source changes.
func (m *Module) SourceAssetThumbnail(ctx context.Context, sourceID string) (immich.Thumbnail, error) {
	if sourceID == "" {
		return immich.Thumbnail{}, errcodes.NotFound("Thumbnail")
	}
	return m.source.Thumbnail(ctx, sourceID)
}

// EntryThumbnail resolves a content-versioned entry only after verifying active
// membership in a completed Album. A stale URL must never serve different bytes.
func (m *Module) EntryThumbnail(ctx context.Context, id, version string) (string, error) {
	item, err := m.entryMedia(ctx, id, version, "Thumbnail")
	return item.SourceID, err
}

// EntryOriginal resolves the source asset and filename behind a photo or
// video download.
func (m *Module) EntryOriginal(ctx context.Context, id, version string) (EntryMedia, error) {
	return m.entryMedia(ctx, id, version, "Download")
}

// EntryPlayback resolves the source asset behind a video stream. Photos have
// no playback, so they are not found here.
func (m *Module) EntryPlayback(ctx context.Context, id, version string) (EntryMedia, error) {
	item, err := m.entryMedia(ctx, id, version, "Video")
	if err == nil && item.Kind != "VIDEO" {
		return EntryMedia{}, errcodes.NotFound("Video")
	}
	return item, err
}

// EntryMedia is the imported source identity behind one Album Entry.
type EntryMedia struct {
	SourceID string
	Filename string
	Kind     string
}

func (m *Module) entryMedia(ctx context.Context, id, version, resource string) (EntryMedia, error) {
	var item EntryMedia
	if _, err := uuid.Parse(id); err != nil {
		return item, errcodes.NotFound(resource)
	}
	err := m.db.NewSelect().TableExpr("album_entries AS entry").ColumnExpr("item.source_id, item.filename, item.kind").
		Join("JOIN albums AS album ON album.id = entry.album_id").Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.id = ? AND entry.removed_at IS NULL AND album.import_status = 'complete'", id).
		Where("item.content_version = ? AND NOT item.offline AND NOT item.trashed", version).Scan(ctx, &item)
	if errors.Is(err, sql.ErrNoRows) {
		return item, errcodes.NotFound(resource)
	}
	return item, errorstack.CaptureContext(ctx, err)
}

// FaceThumbnail authorizes a cached Immich face at the requested version and
// returns the Immich person ID to fetch. A superseded version is not found, so
// once a refresh records a newer version the old URL stops serving.
func (m *Module) FaceThumbnail(ctx context.Context, sourceID, version string) (string, error) {
	if sourceID == "" {
		return "", errcodes.NotFound("Face thumbnail")
	}
	available, err := m.db.NewSelect().Model((*models.MediaFaceAssociation)(nil)).
		Where("face.source_face_id = ? AND face.source_version = ?", sourceID, version).Exists(ctx)
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	if !available {
		return "", errcodes.NotFound("Face thumbnail")
	}
	return sourceID, nil
}

// PersonAvatar authorizes a Person's avatar at the requested version and
// returns the linked Immich person ID to fetch.
func (m *Module) PersonAvatar(ctx context.Context, personID, version string) (string, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return "", errcodes.NotFound("Avatar")
	}
	var avatar struct {
		SourceID string
		Version  string
	}
	err := m.db.NewSelect().TableExpr("persons AS person").
		ColumnExpr("person.avatar_face_id AS source_id, coalesce(max(face.source_version), '') AS version").
		Join("JOIN immich_face_links AS link ON link.source_id = person.avatar_face_id AND link.person_id = person.id AND NOT link.ignored").
		Join("LEFT JOIN media_face_associations AS face ON face.source_face_id = person.avatar_face_id").
		Where("person.id = ?", personID).Group("person.avatar_face_id").Scan(ctx, &avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errcodes.NotFound("Avatar")
	}
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	if avatarVersion(avatar.SourceID, avatar.Version) != version {
		return "", errcodes.NotFound("Avatar")
	}
	return avatar.SourceID, nil
}

// personThumbnail opens an authorized Immich person thumbnail.
func (m *Module) personThumbnail(ctx context.Context, sourceID string) (immich.Thumbnail, error) {
	return m.source.PersonThumbnail(ctx, sourceID)
}

// viewerImage preserves diagnostic causes for local logs while keeping
// installation details out of ordinary and preview media responses.
func (m *Module) viewerImage(ctx context.Context, sourceID, version string, large bool) (immich.Thumbnail, error) {
	image, err := m.generatedImage(ctx, sourceID, version, large)
	return image, redactUpstream(ctx, err)
}

// viewerOriginal opens the uploaded file behind an authorized photo or video
// with the same version check and redaction as generated variants.
func (m *Module) viewerOriginal(ctx context.Context, sourceID, version string) (immich.Original, error) {
	if err := m.checkVersion(ctx, sourceID, version, "Download"); err != nil {
		return immich.Original{}, redactUpstream(ctx, err)
	}
	original, err := m.source.Original(ctx, sourceID)
	return original, redactUpstream(ctx, err)
}

// checkOriginal answers a HEAD download: the same version check, no stream.
func (m *Module) checkOriginal(ctx context.Context, sourceID, version string) error {
	return redactUpstream(ctx, m.checkVersion(ctx, sourceID, version, "Download"))
}

// viewerPlayback opens Immich's playback stream for an authorized video after
// the same version check, forwarding at most one byte range.
func (m *Module) viewerPlayback(ctx context.Context, sourceID, version string, request immich.PlaybackRequest) (immich.Playback, error) {
	if err := m.checkVersion(ctx, sourceID, version, "Video"); err != nil {
		return immich.Playback{}, redactUpstream(ctx, err)
	}
	playback, err := m.source.Playback(ctx, sourceID, request)
	return playback, redactUpstream(ctx, err)
}

func redactUpstream(ctx context.Context, err error) error {
	if err == nil || errorstack.IsContextCancellation(ctx, err) {
		return err
	}
	var public error = &errcodes.Error{HTTPCode: http.StatusBadGateway, Code: "media_unavailable", Message: "Media is unavailable. Try again later."}
	if coded, ok := errors.AsType[*errcodes.Error](err); ok && coded.HTTPCode == http.StatusNotFound {
		public = errcodes.NotFound("Media")
	}
	return errors.Join(public, err)
}

func (m *Module) generatedThumbnail(ctx context.Context, sourceID, version string) (immich.Thumbnail, error) {
	return m.generatedImage(ctx, sourceID, version, false)
}

// generatedImage serves a fixed Immich variant only while the asset still
// matches the requested content version.
func (m *Module) generatedImage(ctx context.Context, sourceID, version string, large bool) (immich.Thumbnail, error) {
	if err := m.checkVersion(ctx, sourceID, version, "Thumbnail"); err != nil {
		return immich.Thumbnail{}, err
	}
	if large {
		return m.source.Preview(ctx, sourceID)
	}
	return m.source.Thumbnail(ctx, sourceID)
}

// checkVersion confirms the live asset still matches the requested content
// version, so an edited or removed source never serves under an old URL.
func (m *Module) checkVersion(ctx context.Context, sourceID, version, resource string) error {
	asset, err := m.source.GetAsset(ctx, sourceID)
	if err != nil {
		return err
	}
	if asset.ID != sourceID || asset.Offline || asset.Trashed || ContentVersion(asset) != version {
		return errcodes.NotFound(resource)
	}
	return nil
}
