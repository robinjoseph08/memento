package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `CREATE TABLE notification_preference_tokens (
				token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
				recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
				notification_batch_id bigint NOT NULL REFERENCES notification_batches(id) ON DELETE RESTRICT,
				created_at timestamptz NOT NULL DEFAULT now(),
				expires_at timestamptz NOT NULL,
				CHECK (expires_at > created_at)
			)`)
			return err
		},
		func(ctx context.Context, db *bun.DB) error {
			_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS notification_preference_tokens`)
			return err
		},
	)
}
