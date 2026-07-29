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
					`CREATE TABLE publication_notification_media (
						publication_id uuid NOT NULL REFERENCES publications(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
						PRIMARY KEY (publication_id, recipient_access_generation_id, media_item_id),
						FOREIGN KEY (publication_id, recipient_access_generation_id)
							REFERENCES publication_activity_items(publication_id, recipient_access_generation_id) ON DELETE CASCADE
					)`,
					`CREATE INDEX publication_notification_media_recipient_idx
						ON publication_notification_media (recipient_access_generation_id, publication_id, media_item_id)`,
					`CREATE TABLE notification_batches (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						public_id uuid NOT NULL UNIQUE,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						channel text NOT NULL CHECK (channel IN ('email')),
						window_started_at timestamptz NOT NULL,
						closes_at timestamptz NOT NULL,
						status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'suppressed', 'failed')),
						truncated boolean NOT NULL DEFAULT false,
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
						last_safe_error text,
						sent_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						UNIQUE (recipient_access_generation_id, channel, window_started_at),
						CHECK (closes_at = window_started_at + interval '15 minutes'),
						CHECK ((status = 'sent') = (sent_at IS NOT NULL))
					)`,
					`CREATE INDEX notification_batches_due_idx ON notification_batches (closes_at, id) WHERE status = 'pending'`,
					`ALTER TABLE delivery_problems ALTER COLUMN email_delivery_id DROP NOT NULL`,
					`ALTER TABLE delivery_problems ADD COLUMN notification_batch_id bigint UNIQUE REFERENCES notification_batches(id) ON DELETE RESTRICT`,
					`ALTER TABLE delivery_problems ADD CONSTRAINT delivery_problems_source_check CHECK (
						(email_delivery_id IS NOT NULL AND notification_batch_id IS NULL) OR
						(email_delivery_id IS NULL AND notification_batch_id IS NOT NULL)
					)`,
					`CREATE TABLE notification_batch_items (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						batch_id bigint NOT NULL REFERENCES notification_batches(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						kind text NOT NULL CHECK (kind IN ('publication', 'comment')),
						publication_id uuid REFERENCES publications(id) ON DELETE RESTRICT,
						comment_id uuid REFERENCES comments(id) ON DELETE RESTRICT,
						activity_created_at timestamptz NOT NULL,
						created_at timestamptz NOT NULL DEFAULT now(),
						CHECK (
							(kind = 'publication' AND publication_id IS NOT NULL AND comment_id IS NULL) OR
							(kind = 'comment' AND publication_id IS NULL AND comment_id IS NOT NULL)
						)
					)`,
					`CREATE UNIQUE INDEX notification_batch_publication_idx
						ON notification_batch_items (batch_id, publication_id) WHERE kind = 'publication'`,
					`CREATE UNIQUE INDEX notification_batch_comment_idx
						ON notification_batch_items (batch_id, comment_id) WHERE kind = 'comment'`,
					`CREATE UNIQUE INDEX notification_publication_once_idx
						ON notification_batch_items (recipient_access_generation_id, publication_id) WHERE kind = 'publication'`,
					`CREATE UNIQUE INDEX notification_comment_once_idx
						ON notification_batch_items (recipient_access_generation_id, comment_id) WHERE kind = 'comment'`,
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
					`DELETE FROM jobs WHERE kind = 'send_immediate_email'`,
					`DELETE FROM outbox_events WHERE kind = 'send_immediate_email'`,
					`DELETE FROM delivery_problems WHERE notification_batch_id IS NOT NULL`,
					`DROP TABLE IF EXISTS notification_batch_items`,
					`ALTER TABLE delivery_problems DROP CONSTRAINT delivery_problems_source_check`,
					`ALTER TABLE delivery_problems DROP COLUMN notification_batch_id`,
					`ALTER TABLE delivery_problems ALTER COLUMN email_delivery_id SET NOT NULL`,
					`DROP TABLE IF EXISTS notification_batches`,
					`DROP TABLE IF EXISTS publication_notification_media`,
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
