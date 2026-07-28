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
					`ALTER TABLE content_withdrawals
						ADD COLUMN reason text NOT NULL DEFAULT 'Legacy withdrawal'
						CHECK (char_length(reason) BETWEEN 1 AND 1000)`,
					`ALTER TABLE publication_audit_events
						DROP CONSTRAINT publication_audit_events_target_kind_check,
						ADD CONSTRAINT publication_audit_events_target_kind_check
						CHECK (target_kind IN ('event', 'moment', 'media', 'loose_item'))`,
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
					`ALTER TABLE publication_audit_events
						DROP CONSTRAINT publication_audit_events_target_kind_check,
						ADD CONSTRAINT publication_audit_events_target_kind_check
						CHECK (target_kind IN ('moment', 'loose_item'))`,
					`ALTER TABLE content_withdrawals DROP COLUMN reason`,
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
