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
CREATE TABLE update_notifications (
 id uuid PRIMARY KEY,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 version integer NOT NULL CHECK (version >= 1),
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 read_at timestamptz
);
CREATE INDEX update_notifications_person_idx ON update_notifications(person_id, created_at DESC, id DESC);
ALTER TABLE announced_entries ADD COLUMN notification_id uuid REFERENCES update_notifications(id) ON DELETE SET NULL;
CREATE INDEX announced_entries_notification_idx ON announced_entries(notification_id) WHERE notification_id IS NOT NULL;
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE announced_entries DROP COLUMN notification_id; DROP TABLE update_notifications`)
		return errorstack.CaptureContext(ctx, err)
	})
}
