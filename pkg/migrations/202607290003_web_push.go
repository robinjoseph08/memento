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
					`DELETE FROM push_subscriptions`,
					`DROP INDEX push_subscriptions_session_active_idx`,
					`ALTER TABLE push_subscriptions
						ADD COLUMN material_ciphertext bytea NOT NULL DEFAULT decode(repeat('00', 32), 'hex')
							CHECK (octet_length(material_ciphertext) >= 32),
						ADD COLUMN expiration_at timestamptz,
						ADD COLUMN enrollment_version bigint NOT NULL DEFAULT 1 CHECK (enrollment_version > 0),
						ADD COLUMN last_reconciled_at timestamptz NOT NULL DEFAULT now(),
						ADD COLUMN last_success_at timestamptz,
						ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now()`,
					`CREATE UNIQUE INDEX push_subscriptions_session_active_idx
						ON push_subscriptions (session_id) WHERE disabled_at IS NULL`,
					`ALTER TABLE notification_batches DROP CONSTRAINT notification_batches_channel_check`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_channel_check
						CHECK (channel IN ('email', 'push'))`,
					`ALTER TABLE notification_batches ADD COLUMN push_subscription_id uuid
						REFERENCES push_subscriptions(id) ON DELETE RESTRICT`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_push_target_check CHECK (
						(channel = 'push' AND cadence = 'immediate' AND push_subscription_id IS NOT NULL) OR
						(channel = 'email' AND push_subscription_id IS NULL)
					)`,
					`DROP INDEX notification_batches_immediate_window_idx`,
					`DROP INDEX notification_batches_weekly_delivery_idx`,
					`CREATE UNIQUE INDEX notification_batches_immediate_email_window_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, window_started_at)
						WHERE cadence = 'immediate' AND channel = 'email'`,
					`CREATE UNIQUE INDEX notification_batches_immediate_push_window_idx
						ON notification_batches (push_subscription_id, preference_version, window_started_at)
						WHERE cadence = 'immediate' AND channel = 'push'`,
					`CREATE UNIQUE INDEX notification_batches_weekly_delivery_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, closes_at)
						WHERE cadence = 'weekly' AND channel = 'email' AND status = 'pending'`,
					`ALTER TABLE notification_batch_items
						ADD COLUMN channel text NOT NULL DEFAULT 'email' CHECK (channel IN ('email', 'push')),
						ADD COLUMN push_subscription_id uuid REFERENCES push_subscriptions(id) ON DELETE RESTRICT`,
					`ALTER TABLE notification_batch_items ADD CONSTRAINT notification_batch_items_push_target_check CHECK (
						(channel = 'push' AND push_subscription_id IS NOT NULL) OR
						(channel = 'email' AND push_subscription_id IS NULL)
					)`,
					`DROP INDEX notification_publication_once_idx`,
					`DROP INDEX notification_comment_once_idx`,
					`CREATE UNIQUE INDEX notification_email_publication_once_idx
						ON notification_batch_items (recipient_access_generation_id, publication_id)
						WHERE channel = 'email' AND kind = 'publication'`,
					`CREATE UNIQUE INDEX notification_email_comment_once_idx
						ON notification_batch_items (recipient_access_generation_id, comment_id)
						WHERE channel = 'email' AND kind = 'comment'`,
					`CREATE UNIQUE INDEX notification_push_publication_once_idx
						ON notification_batch_items (push_subscription_id, publication_id)
						WHERE channel = 'push' AND kind = 'publication'`,
					`CREATE UNIQUE INDEX notification_push_comment_once_idx
						ON notification_batch_items (push_subscription_id, comment_id)
						WHERE channel = 'push' AND kind = 'comment'`,
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
					`DELETE FROM jobs WHERE kind = 'send_immediate_push'`,
					`DELETE FROM outbox_events WHERE kind = 'send_immediate_push'`,
					`DELETE FROM delivery_problems WHERE notification_batch_id IN
						(SELECT id FROM notification_batches WHERE channel = 'push')`,
					`DELETE FROM notification_batch_items WHERE channel = 'push'`,
					`DELETE FROM notification_batches WHERE channel = 'push'`,
					`DROP INDEX notification_push_comment_once_idx`,
					`DROP INDEX notification_push_publication_once_idx`,
					`DROP INDEX notification_email_comment_once_idx`,
					`DROP INDEX notification_email_publication_once_idx`,
					`CREATE UNIQUE INDEX notification_publication_once_idx
						ON notification_batch_items (recipient_access_generation_id, publication_id) WHERE kind = 'publication'`,
					`CREATE UNIQUE INDEX notification_comment_once_idx
						ON notification_batch_items (recipient_access_generation_id, comment_id) WHERE kind = 'comment'`,
					`ALTER TABLE notification_batch_items DROP CONSTRAINT notification_batch_items_push_target_check`,
					`ALTER TABLE notification_batch_items DROP COLUMN push_subscription_id, DROP COLUMN channel`,
					`DROP INDEX notification_batches_weekly_delivery_idx`,
					`DROP INDEX notification_batches_immediate_push_window_idx`,
					`DROP INDEX notification_batches_immediate_email_window_idx`,
					`CREATE UNIQUE INDEX notification_batches_immediate_window_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, window_started_at)
						WHERE cadence = 'immediate'`,
					`CREATE UNIQUE INDEX notification_batches_weekly_delivery_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, closes_at)
						WHERE cadence = 'weekly' AND status = 'pending'`,
					`ALTER TABLE notification_batches DROP CONSTRAINT notification_batches_push_target_check`,
					`ALTER TABLE notification_batches DROP COLUMN push_subscription_id`,
					`ALTER TABLE notification_batches DROP CONSTRAINT notification_batches_channel_check`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_channel_check CHECK (channel IN ('email'))`,
					`DROP INDEX push_subscriptions_session_active_idx`,
					`ALTER TABLE push_subscriptions DROP COLUMN updated_at, DROP COLUMN last_success_at,
						DROP COLUMN last_reconciled_at, DROP COLUMN enrollment_version, DROP COLUMN expiration_at,
						DROP COLUMN material_ciphertext`,
					`CREATE INDEX push_subscriptions_session_active_idx ON push_subscriptions (session_id) WHERE disabled_at IS NULL`,
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
