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
					`ALTER TABLE system_settings ADD COLUMN weekly_timezone text NOT NULL DEFAULT 'UTC'
						CHECK (weekly_timezone <> '' AND length(weekly_timezone) <= 255)`,
					`ALTER TABLE notification_preferences
						ADD COLUMN preference_version bigint NOT NULL DEFAULT 0 CHECK (preference_version >= 0),
						ADD COLUMN weekly_schedule_overridden boolean NOT NULL DEFAULT false,
						ADD COLUMN weekly_day text NOT NULL DEFAULT 'sunday'
							CHECK (weekly_day IN ('sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday')),
						ADD COLUMN weekly_local_time text NOT NULL DEFAULT '09:00'
							CHECK (weekly_local_time ~ '^(?:[01][0-9]|2[0-3]):[0-5][0-9]$'),
						ADD COLUMN weekly_timezone text NOT NULL DEFAULT 'UTC'
							CHECK (weekly_timezone <> '' AND length(weekly_timezone) <= 255)`,
					`ALTER TABLE notification_batches
						ADD COLUMN cadence text NOT NULL DEFAULT 'immediate'
							CHECK (cadence IN ('immediate', 'weekly')),
						ADD COLUMN preference_version bigint NOT NULL DEFAULT 0 CHECK (preference_version >= 0)`,
					`DO $$
					DECLARE constraint_name text;
					BEGIN
						SELECT conname INTO constraint_name
						FROM pg_constraint
						WHERE conrelid = 'notification_batches'::regclass AND contype = 'u'
						  AND pg_get_constraintdef(oid) = 'UNIQUE (recipient_access_generation_id, channel, window_started_at)';
						IF constraint_name IS NULL THEN
							RAISE EXCEPTION 'notification batch window uniqueness constraint is missing';
						END IF;
						EXECUTE format('ALTER TABLE notification_batches DROP CONSTRAINT %I', constraint_name);
					END $$`,
					`ALTER TABLE notification_batches DROP CONSTRAINT notification_batches_check`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_window_check CHECK (
						(cadence = 'immediate' AND closes_at = window_started_at + interval '15 minutes') OR
						(cadence = 'weekly' AND closes_at > window_started_at
						 AND closes_at <= window_started_at + interval '8 days')
					)`,
					`CREATE UNIQUE INDEX notification_batches_immediate_window_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, window_started_at)
						WHERE cadence = 'immediate'`,
					`CREATE UNIQUE INDEX notification_batches_weekly_delivery_idx
						ON notification_batches (recipient_access_generation_id, channel, preference_version, closes_at)
						WHERE cadence = 'weekly' AND status = 'pending'`,
					`ALTER TABLE notification_preference_tokens ADD COLUMN consumed_at timestamptz`,
					`CREATE INDEX notification_preference_tokens_current_idx
						ON notification_preference_tokens (expires_at, recipient_access_generation_id)
						WHERE consumed_at IS NULL`,
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
					`DELETE FROM jobs WHERE kind = 'send_weekly_email'`,
					`DELETE FROM outbox_events WHERE kind = 'send_weekly_email'`,
					`DELETE FROM notification_preference_tokens WHERE notification_batch_id IN
						(SELECT id FROM notification_batches WHERE cadence = 'weekly')`,
					`DELETE FROM delivery_problems WHERE notification_batch_id IN
						(SELECT id FROM notification_batches WHERE cadence = 'weekly')`,
					`DELETE FROM notification_batch_items WHERE batch_id IN
						(SELECT id FROM notification_batches WHERE cadence = 'weekly')`,
					`DELETE FROM notification_batches WHERE cadence = 'weekly'`,
					`DROP INDEX notification_preference_tokens_current_idx`,
					`ALTER TABLE notification_preference_tokens DROP COLUMN consumed_at`,
					`DROP INDEX notification_batches_weekly_delivery_idx`,
					`DROP INDEX notification_batches_immediate_window_idx`,
					`ALTER TABLE notification_batches DROP CONSTRAINT notification_batches_window_check`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_check
						CHECK (closes_at = window_started_at + interval '15 minutes')`,
					`ALTER TABLE notification_batches ADD CONSTRAINT notification_batches_recipient_channel_window_key
						UNIQUE (recipient_access_generation_id, channel, window_started_at)`,
					`ALTER TABLE notification_batches DROP COLUMN preference_version, DROP COLUMN cadence`,
					`ALTER TABLE notification_preferences
						DROP COLUMN weekly_timezone, DROP COLUMN weekly_local_time, DROP COLUMN weekly_day,
						DROP COLUMN weekly_schedule_overridden, DROP COLUMN preference_version`,
					`ALTER TABLE system_settings DROP COLUMN weekly_timezone`,
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
