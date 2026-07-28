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
					`CREATE TABLE staged_updates (
						id uuid PRIMARY KEY,
						event_id uuid NOT NULL UNIQUE REFERENCES events(id) ON DELETE RESTRICT,
						base_publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						net_changes jsonb NOT NULL,
						created_at timestamptz NOT NULL,
						updated_at timestamptz NOT NULL
					)`,
					`ALTER TABLE events ADD COLUMN current_staged_update_id uuid UNIQUE REFERENCES staged_updates(id) ON DELETE RESTRICT`,
					`CREATE INDEX staged_updates_updated_idx ON staged_updates (updated_at DESC, event_id)`,
					`CREATE TABLE staged_source_removals (
						event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						draft_moment_id uuid,
						position integer NOT NULL CHECK (position >= 0),
						was_cover boolean NOT NULL DEFAULT false,
						created_at timestamptz NOT NULL,
						PRIMARY KEY (event_id, media_item_id)
					)`,
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
					`DROP TABLE staged_source_removals`,
					`ALTER TABLE events DROP COLUMN current_staged_update_id`,
					`DROP TABLE staged_updates`,
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
