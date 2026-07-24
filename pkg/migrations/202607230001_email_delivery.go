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
					`ALTER TABLE jobs DROP CONSTRAINT jobs_status_check`,
					`ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'completed', 'failed'))`,
					`ALTER TABLE jobs ADD COLUMN idempotency_key text`,
					`ALTER TABLE jobs ADD COLUMN last_safe_error text`,
					`CREATE UNIQUE INDEX jobs_idempotency_key_idx ON jobs (idempotency_key) WHERE idempotency_key IS NOT NULL`,
					`CREATE TABLE email_deliveries (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						public_id text NOT NULL UNIQUE,
						kind text NOT NULL CHECK (kind IN ('required_test')),
						recipient text NOT NULL,
						subject text NOT NULL,
						body text NOT NULL,
						status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
						last_safe_error text,
						next_retry_at timestamptz,
						sent_at timestamptz,
						failed_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE TABLE delivery_problems (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						email_delivery_id bigint NOT NULL UNIQUE REFERENCES email_deliveries(id) ON DELETE RESTRICT,
						diagnostic text NOT NULL,
						created_at timestamptz NOT NULL DEFAULT now(),
						resolved_at timestamptz
					)`,
					`CREATE INDEX delivery_problems_unresolved_idx ON delivery_problems (created_at, id) WHERE resolved_at IS NULL`,
					`CREATE TABLE outbox_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						kind text NOT NULL,
						aggregate_kind text NOT NULL,
						aggregate_id text NOT NULL,
						aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
						payload jsonb NOT NULL DEFAULT '{}'::jsonb,
						available_at timestamptz NOT NULL DEFAULT now(),
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
						lease_owner text,
						lease_expires_at timestamptz,
						delivered_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						UNIQUE (aggregate_kind, aggregate_id, aggregate_version, kind),
						CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
						CHECK (delivered_at IS NULL OR lease_owner IS NULL)
					)`,
					`CREATE INDEX outbox_events_claimable_idx ON outbox_events (available_at, id) WHERE delivered_at IS NULL`,
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
					`DROP TABLE IF EXISTS outbox_events`,
					`DROP TABLE IF EXISTS delivery_problems`,
					`DROP TABLE IF EXISTS email_deliveries`,
					`DELETE FROM jobs WHERE kind = 'send_required_email'`,
					`UPDATE jobs SET status = 'pending', available_at = now(), lease_owner = NULL, lease_expires_at = NULL WHERE status = 'failed'`,
					`DROP INDEX IF EXISTS jobs_idempotency_key_idx`,
					`ALTER TABLE jobs DROP COLUMN IF EXISTS last_safe_error`,
					`ALTER TABLE jobs DROP COLUMN IF EXISTS idempotency_key`,
					`ALTER TABLE jobs DROP CONSTRAINT jobs_status_check`,
					`ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'completed'))`,
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
