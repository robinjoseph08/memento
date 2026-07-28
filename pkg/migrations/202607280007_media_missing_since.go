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
					`ALTER TABLE media_items ADD COLUMN missing_since timestamptz`,
					`UPDATE media_items SET missing_since = updated_at WHERE availability = 'source_missing'`,
					`ALTER TABLE media_items ADD CONSTRAINT media_items_missing_since_check
						CHECK ((availability = 'source_missing') = (missing_since IS NOT NULL))`,
					`CREATE INDEX media_items_missing_since_idx ON media_items (missing_since, id) WHERE availability = 'source_missing'`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `
				DROP INDEX media_items_missing_since_idx;
				ALTER TABLE media_items DROP CONSTRAINT media_items_missing_since_check, DROP COLUMN missing_since
			`)
			return err
		},
	)
}
