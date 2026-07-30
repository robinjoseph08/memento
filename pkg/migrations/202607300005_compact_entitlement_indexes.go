package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`ALTER TABLE current_audience_entitlements DROP CONSTRAINT current_audience_entitlements_pkey`,
					`DROP INDEX current_entitlements_recipient_idx`,
					`DROP INDEX current_entitlements_media_idx`,
					`ALTER TABLE current_audience_entitlements ADD CONSTRAINT current_audience_entitlements_pkey
						PRIMARY KEY (recipient_access_generation_id, event_id, media_item_id)`,
					`CREATE INDEX current_entitlements_media_idx ON current_audience_entitlements (media_item_id, recipient_access_generation_id)`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`DROP INDEX current_entitlements_media_idx`,
					`ALTER TABLE current_audience_entitlements DROP CONSTRAINT current_audience_entitlements_pkey`,
					`ALTER TABLE current_audience_entitlements ADD CONSTRAINT current_audience_entitlements_pkey
						PRIMARY KEY (event_id, recipient_access_generation_id, media_item_id)`,
					`CREATE INDEX current_entitlements_recipient_idx ON current_audience_entitlements (recipient_access_generation_id, event_id, media_item_id)`,
					`CREATE INDEX current_entitlements_media_idx ON current_audience_entitlements (media_item_id, recipient_access_generation_id)`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
