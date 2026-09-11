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
