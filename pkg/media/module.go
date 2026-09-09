// Package media resolves Curator media requests to fixed Immich-generated variants.
package media

import (
	"context"
	"database/sql"
	"errors"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// Source exposes only source album lookup and generated thumbnails.
type Source interface {
	GetAlbum(context.Context, string) (immich.Album, error)
	GetAsset(context.Context, string) (immich.Asset, error)
	Thumbnail(context.Context, string) (immich.Thumbnail, error)
	PersonThumbnail(context.Context, string) (immich.Thumbnail, error)
}

type Module struct {
	db     *bun.DB
	source Source
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

// EntryThumbnail resolves a content-versioned entry only after verifying active
// membership in a completed Album. A stale URL must never serve different bytes.
func (m *Module) EntryThumbnail(ctx context.Context, id, version string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", errcodes.NotFound("Thumbnail")
	}
	var sourceID string
	err := m.db.NewSelect().TableExpr("album_entries AS entry").Column("item.source_id").
		Join("JOIN albums AS album ON album.id = entry.album_id").Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Where("entry.id = ? AND entry.removed_at IS NULL AND album.import_status = 'complete'", id).
		Where("item.content_version = ? AND NOT item.offline AND NOT item.trashed", version).Scan(ctx, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errcodes.NotFound("Thumbnail")
	}
	return sourceID, errorstack.CaptureContext(ctx, err)
}

func (m *Module) FaceThumbnail(ctx context.Context, sourceID string) (immich.Thumbnail, error) {
	if sourceID == "" {
		return immich.Thumbnail{}, errcodes.NotFound("Face thumbnail")
	}
	available, err := m.db.NewSelect().Model((*models.MediaFaceAssociation)(nil)).
		Where("face.source_face_id = ?", sourceID).Exists(ctx)
	if err != nil {
		return immich.Thumbnail{}, errorstack.CaptureContext(ctx, err)
	}
	if !available {
		return immich.Thumbnail{}, errcodes.NotFound("Face thumbnail")
	}
	return m.source.PersonThumbnail(ctx, sourceID)
}

func (m *Module) PersonAvatar(ctx context.Context, personID string) (immich.Thumbnail, error) {
	if _, err := uuid.Parse(personID); err != nil {
		return immich.Thumbnail{}, errcodes.NotFound("Avatar")
	}
	var sourceID string
	err := m.db.NewSelect().TableExpr("persons AS person").Column("person.avatar_face_id").
		Join("JOIN immich_face_links AS link ON link.source_id = person.avatar_face_id AND link.person_id = person.id AND NOT link.ignored").
		Where("person.id = ? AND person.deactivated_at IS NULL", personID).Scan(ctx, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return immich.Thumbnail{}, errcodes.NotFound("Avatar")
	}
	if err != nil {
		return immich.Thumbnail{}, errorstack.CaptureContext(ctx, err)
	}
	return m.source.PersonThumbnail(ctx, sourceID)
}

func (m *Module) generatedThumbnail(ctx context.Context, sourceID, version string) (immich.Thumbnail, error) {
	asset, err := m.source.GetAsset(ctx, sourceID)
	if err != nil {
		return immich.Thumbnail{}, err
	}
	if asset.ID != sourceID || asset.Offline || asset.Trashed || ContentVersion(asset) != version {
		return immich.Thumbnail{}, errcodes.NotFound("Thumbnail")
	}
	return m.source.Thumbnail(ctx, sourceID)
}
