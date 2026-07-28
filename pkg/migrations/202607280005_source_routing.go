package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.ExecContext(ctx, `
					ALTER TABLE event_sources
					ADD COLUMN include_future_media boolean NOT NULL DEFAULT true
				`); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `
					UPDATE event_sources AS source
					SET include_future_media = false
					WHERE EXISTS (
						SELECT 1 FROM source_album_memberships AS membership
						WHERE membership.source_album_id = source.source_album_id
						  AND NOT EXISTS (
							SELECT 1 FROM draft_media_placements AS placement
							WHERE placement.event_id = source.event_id
							  AND placement.media_item_id = membership.media_item_id
						  )
					)
				`)
				return err
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `ALTER TABLE event_sources DROP COLUMN include_future_media`)
			return err
		},
	)
}
