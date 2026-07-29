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
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return MarkSourceMissingInTx(ctx, tx, backings)
	})
}

// MarkSourceMissingInTx applies the same exact-backing transition inside the
// caller's transaction, alongside any broader locks or updates the caller requires.
func MarkSourceMissingInTx(ctx context.Context, tx bun.Tx, backings []Backing) error {
	unique := make(map[uuid.UUID]Backing, len(backings))
	for _, backing := range backings {
		if backing.MediaID == uuid.Nil || backing.BackingID == uuid.Nil || backing.AssetID == uuid.Nil {
			continue
		}
		unique[backing.BackingID] = backing
	}
	ordered := make([]Backing, 0, len(unique))
	for _, backing := range unique {
		ordered = append(ordered, backing)
	}
	slices.SortFunc(ordered, func(left, right Backing) int {
		if order := bytes.Compare(left.MediaID[:], right.MediaID[:]); order != 0 {
			return order
		}
		return bytes.Compare(left.BackingID[:], right.BackingID[:])
	})
	for _, backing := range ordered {
		// Confirmation may move an active addition backing to the stable Media
		// identity while this not-found response is in flight. Resolve by the
		// exact backing, then lock Media before rechecking the backing to retain
		// the repository-wide Media-to-backing lock order. A move between those
		// statements is retried against its surviving identity.
		for {
			var currentMediaID uuid.UUID
			err := tx.NewRaw(`
					SELECT media_item_id FROM media_backings
					WHERE id = ? AND immich_asset_id = ? AND active
				`, backing.BackingID, backing.AssetID).Scan(ctx, &currentMediaID)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			var lockedMediaID uuid.UUID
			err = tx.NewRaw(`SELECT id FROM media_items WHERE id = ? FOR UPDATE`, currentMediaID).Scan(ctx, &lockedMediaID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			result, err := tx.NewRaw(`
					UPDATE media_items AS media
					SET availability = 'source_missing', missing_since = COALESCE(missing_since, now()), updated_at = now()
					WHERE media.id = ? AND media.availability = 'current'
					  AND EXISTS (
						SELECT 1 FROM media_backings AS backing
						WHERE backing.id = ? AND backing.media_item_id = media.id
						  AND backing.immich_asset_id = ? AND backing.active
					  )
				`, currentMediaID, backing.BackingID, backing.AssetID).Exec(ctx)
			if err != nil {
				return err
			}
			updated, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if updated > 0 {
				break
			}
			var stillCurrent bool
			if err := tx.NewRaw(`SELECT EXISTS (
					SELECT 1 FROM media_backings
					WHERE id = ? AND media_item_id = ? AND immich_asset_id = ? AND active
				)`, backing.BackingID, currentMediaID, backing.AssetID).Scan(ctx, &stillCurrent); err != nil {
				return err
			}
			if stillCurrent {
				break
			}
		}
	}
	return nil
}
