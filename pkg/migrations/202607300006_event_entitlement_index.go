package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `CREATE INDEX current_entitlements_event_idx
				ON current_audience_entitlements (event_id, recipient_access_generation_id, media_item_id)`)
			return err
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `DROP INDEX current_entitlements_event_idx`)
			return err
		},
	)
}
