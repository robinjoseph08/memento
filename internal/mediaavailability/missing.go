// Package mediaavailability owns atomic source availability transitions for Media backings.
package mediaavailability

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Backing identifies the exact active backing whose not-found response is
// evidence that a stable portal Media identity is no longer deliverable.
type Backing struct {
	MediaID   uuid.UUID
	BackingID uuid.UUID
	AssetID   uuid.UUID
}

// MarkSourceMissing atomically marks every still-current Media item whose exact
// active backing participated in a delivery request that returned not found.
// Stale observations are harmless.
func MarkSourceMissing(ctx context.Context, db *bun.DB, backings []Backing) error {
	if len(backings) == 0 {
		return nil
	}
	unique := make(map[uuid.UUID]Backing, len(backings))
	for _, backing := range backings {
		if backing.MediaID == uuid.Nil || backing.BackingID == uuid.Nil || backing.AssetID == uuid.Nil {
			continue
		}
		unique[backing.MediaID] = backing
	}
	ordered := make([]Backing, 0, len(unique))
	for _, backing := range unique {
		ordered = append(ordered, backing)
	}
	slices.SortFunc(ordered, func(left, right Backing) int {
		return bytes.Compare(left.MediaID[:], right.MediaID[:])
	})
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, backing := range ordered {
			var lockedMediaID uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM media_items WHERE id = ? FOR UPDATE`, backing.MediaID).Scan(ctx, &lockedMediaID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
			if _, err := tx.NewRaw(`
				UPDATE media_items AS media
				SET availability = 'source_missing', missing_since = COALESCE(missing_since, now()), updated_at = now()
				WHERE media.id = ? AND media.availability = 'current'
				  AND EXISTS (
					SELECT 1 FROM media_backings AS backing
					WHERE backing.id = ? AND backing.media_item_id = media.id
					  AND backing.immich_asset_id = ? AND backing.active
				  )
			`, backing.MediaID, backing.BackingID, backing.AssetID).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}
