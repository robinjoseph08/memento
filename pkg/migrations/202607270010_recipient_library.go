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
					`CREATE TABLE content_withdrawals (
						id uuid PRIMARY KEY,
						target_kind text NOT NULL CHECK (target_kind IN ('event', 'moment', 'media')),
						target_id uuid NOT NULL,
						withdrawn_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						withdrawn_at timestamptz NOT NULL,
						restored_by_publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT,
						restored_at timestamptz,
						CHECK ((restored_by_publication_id IS NULL) = (restored_at IS NULL))
					)`,
					`CREATE UNIQUE INDEX content_withdrawals_active_target_idx
						ON content_withdrawals (target_kind, target_id)
						WHERE restored_at IS NULL`,
					`CREATE TABLE favorites (
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						is_current boolean NOT NULL DEFAULT true,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						PRIMARY KEY (recipient_person_id, media_item_id)
					)`,
					`CREATE INDEX favorites_recipient_current_idx
						ON favorites (recipient_person_id, updated_at DESC, media_item_id)
						WHERE is_current`,
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
					`DROP TABLE favorites`,
					`DROP TABLE content_withdrawals`,
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
