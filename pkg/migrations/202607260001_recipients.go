package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`CREATE TABLE invitations (
						id uuid PRIMARY KEY,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
						issued_at timestamptz NOT NULL,
						expires_at timestamptz NOT NULL,
						sent_at timestamptz,
						accepted_at timestamptz,
						revoked_at timestamptz,
						superseded_at timestamptz,
						automatic_reminder_scheduled_at timestamptz NOT NULL,
						automatic_reminded_at timestamptz,
						last_manual_reminder_requested_at timestamptz,
						last_manual_reminded_at timestamptz,
						manual_reminder_count integer NOT NULL DEFAULT 0 CHECK (manual_reminder_count >= 0),
						created_at timestamptz NOT NULL DEFAULT now(),
						CHECK (expires_at = issued_at + interval '14 days'),
						CHECK (automatic_reminder_scheduled_at = issued_at + interval '7 days'),
						CHECK (accepted_at IS NULL OR (revoked_at IS NULL AND superseded_at IS NULL))
					)`,
					`CREATE UNIQUE INDEX invitations_one_live_generation_idx ON invitations (recipient_access_generation_id)
						WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`,
					`CREATE INDEX invitations_expiry_idx ON invitations (expires_at, id)
						WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`,
					`CREATE INDEX invitations_reminder_due_idx ON invitations (automatic_reminder_scheduled_at, id)
						WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL AND automatic_reminded_at IS NULL`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (
						kind IN ('required_test', 'setup_code', 'invitation_initial', 'invitation_automatic_reminder', 'invitation_manual_reminder'))`,
					`ALTER TABLE email_deliveries ADD COLUMN invitation_id uuid REFERENCES invitations(id) ON DELETE RESTRICT`,
					`ALTER TABLE email_deliveries ADD COLUMN available_at timestamptz NOT NULL DEFAULT now()`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_setup_deadline_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_deadline_check CHECK (
						(kind IN ('setup_code', 'invitation_initial', 'invitation_automatic_reminder', 'invitation_manual_reminder')) = (deliver_before IS NOT NULL))`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_invitation_check CHECK (
						(kind LIKE 'invitation_%') = (invitation_id IS NOT NULL))`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`DELETE FROM jobs WHERE kind = 'send_required_email' AND payload ->> 'delivery_id' IN (SELECT id::text FROM email_deliveries WHERE kind LIKE 'invitation_%')`,
					`DELETE FROM outbox_events WHERE aggregate_kind = 'email_delivery' AND aggregate_id IN (SELECT public_id FROM email_deliveries WHERE kind LIKE 'invitation_%')`,
					`DELETE FROM delivery_problems WHERE email_delivery_id IN (SELECT id FROM email_deliveries WHERE kind LIKE 'invitation_%')`,
					`DELETE FROM email_deliveries WHERE kind LIKE 'invitation_%'`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_invitation_check`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_deadline_check`,
					`ALTER TABLE email_deliveries DROP COLUMN available_at`,
					`ALTER TABLE email_deliveries DROP COLUMN invitation_id`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (kind IN ('required_test', 'setup_code'))`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_setup_deadline_check CHECK ((kind = 'setup_code') = (deliver_before IS NOT NULL))`,
					`DROP TABLE invitations`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
