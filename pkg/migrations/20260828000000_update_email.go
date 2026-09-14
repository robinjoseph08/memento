package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Multi-statement schema DDL stays raw; Bun has no builder for these constraints.
			_, err := tx.ExecContext(ctx, `
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_status_check;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_status_check CHECK (status IN ('queued', 'sending', 'delivered', 'failed', 'uncertain', 'skipped'));
ALTER TABLE update_notifications ADD COLUMN delivery_id uuid UNIQUE REFERENCES mail_deliveries(id);
CREATE TABLE unsubscribe_tokens (
 person_id uuid PRIMARY KEY REFERENCES persons(id) ON DELETE CASCADE,
 token text NOT NULL UNIQUE CHECK (length(token) BETWEEN 32 AND 64),
 created_at timestamptz NOT NULL
);
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		// The older constraint has no neutral state for a skipped email, so a
		// rollback shows those rows as failed. Nothing was sent for them.
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.ExecContext(ctx, `
DROP TABLE unsubscribe_tokens;
ALTER TABLE update_notifications DROP COLUMN delivery_id;
UPDATE mail_deliveries SET status = 'failed' WHERE status = 'skipped';
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_status_check;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_status_check CHECK (status IN ('queued', 'sending', 'delivered', 'failed', 'uncertain'));
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	})
}
