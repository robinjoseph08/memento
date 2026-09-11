package publishing

import (
	"context"
	"fmt"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"
)

type faceRefreshItem struct {
	MediaItemID models.UUID
	SourceID    string
	RefreshedAt *time.Time
	Faces       []immich.Face
}

// RefreshMomentFaces reads Immich before opening a transaction. Cached face
// associations are replaced only after every source read succeeds, so a
// temporary Immich failure cannot erase the last usable recommendations.
func (m *Module) RefreshMomentFaces(ctx context.Context, albumID, momentID string) (AlbumDetail, error) {
	if _, err := albumRow(ctx, m.db, albumID, false); err != nil {
		return AlbumDetail{}, err
	}
	if _, err := momentRow(ctx, m.db, albumID, momentID, false); err != nil {
		return AlbumDetail{}, err
	}
	var items []faceRefreshItem
	if err := m.db.NewSelect().TableExpr("album_entries AS entry").
		ColumnExpr("entry.media_item_id, item.source_id, refresh.refreshed_at").
		Join("JOIN media_items AS item ON item.id = entry.media_item_id").
		Join("LEFT JOIN media_face_refreshes AS refresh ON refresh.media_item_id = entry.media_item_id").
		Where("entry.album_id = ? AND entry.moment_id = ? AND entry.removed_at IS NULL", albumID, momentID).
		OrderExpr("item.source_id COLLATE \"C\", entry.id").Scan(ctx, &items); err != nil {
		return AlbumDetail{}, errorstack.CaptureContext(ctx, err)
	}
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for index := range items {
		group.Go(func() error {
			faces, err := m.source.ListFaces(groupContext, items[index].SourceID)
			if err != nil {
				return fmt.Errorf("refresh faces for Immich asset %q: %w", items[index].SourceID, errorstack.CaptureContext(groupContext, err))
			}
			items[index].Faces = faces
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return AlbumDetail{}, err
	}
	now := time.Now().UTC()
	err := m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := albumRow(ctx, tx, albumID, true); err != nil {
			return err
		}
		if _, err := momentRow(ctx, tx, albumID, momentID, true); err != nil {
			return err
		}
		mediaIDs := make([]models.UUID, 0, len(items))
		for _, item := range items {
			mediaIDs = append(mediaIDs, item.MediaItemID)
		}
		if len(mediaIDs) > 0 {
			var locked []models.MediaItem
			if err := tx.NewSelect().Model(&locked).Column("id").Where("id IN (?)", bun.List(mediaIDs)).OrderExpr("id").For("UPDATE").Scan(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		var currentRefreshes []models.MediaFaceRefresh
		if len(mediaIDs) > 0 {
			if err := tx.NewSelect().Model(&currentRefreshes).Where("media_item_id IN (?)", bun.List(mediaIDs)).Scan(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		currentByMedia := make(map[models.UUID]time.Time, len(currentRefreshes))
		for _, refresh := range currentRefreshes {
			currentByMedia[refresh.MediaItemID] = refresh.RefreshedAt
		}
		for _, item := range items {
			current, exists := currentByMedia[item.MediaItemID]
			if item.RefreshedAt == nil && exists || item.RefreshedAt != nil && (!exists || !current.Equal(*item.RefreshedAt)) {
				return nil
			}
		}
		for _, item := range items {
			if _, err := tx.NewDelete().Model((*models.MediaFaceAssociation)(nil)).
				Where("media_item_id = ?", item.MediaItemID).Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
			seen := map[string]bool{}
			associations := []models.MediaFaceAssociation{}
			for _, face := range item.Faces {
				if face.ID == "" || face.Hidden || seen[face.ID] {
					continue
				}
				seen[face.ID] = true
				associations = append(associations, models.MediaFaceAssociation{
					MediaItemID:   item.MediaItemID,
					SourceFaceID:  face.ID,
					SourceName:    face.Name,
					SourceVersion: face.UpdatedAt,
				})
			}
			if len(associations) > 0 {
				if _, err := tx.NewInsert().Model(&associations).Exec(ctx); err != nil {
					return errorstack.CaptureContext(ctx, err)
				}
			}
			refresh := models.MediaFaceRefresh{MediaItemID: item.MediaItemID, RefreshedAt: now}
			if _, err := tx.NewInsert().Model(&refresh).On("CONFLICT (media_item_id) DO UPDATE").
				Set("refreshed_at = EXCLUDED.refreshed_at").Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		return nil
	})
	if err != nil {
		return AlbumDetail{}, transactionError(ctx, err)
	}
	return m.GetAlbum(ctx, albumID)
}
