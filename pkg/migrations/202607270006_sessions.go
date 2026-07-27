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
					`ALTER TABLE sessions ADD COLUMN label text NOT NULL DEFAULT '' CHECK (char_length(label) <= 80)`,
					`ALTER TABLE sessions ADD COLUMN browser text NOT NULL DEFAULT '' CHECK (char_length(browser) <= 80)`,
					`ALTER TABLE sessions ADD COLUMN platform text NOT NULL DEFAULT '' CHECK (char_length(platform) <= 80)`,
					`ALTER TABLE sessions ADD COLUMN client_ip inet`,
					`ALTER TABLE sessions ADD COLUMN location text CHECK (location IS NULL OR char_length(location) <= 160)`,
					`CREATE TABLE sign_in_challenges (
						id uuid PRIMARY KEY,
						challenge_hash bytea NOT NULL UNIQUE CHECK (octet_length(challenge_hash) = 32),
						code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						email_delivery_id bigint NOT NULL REFERENCES email_deliveries(id) ON DELETE RESTRICT,
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
						expires_at timestamptz NOT NULL,
						consumed_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX sign_in_challenges_active_idx ON sign_in_challenges (expires_at, id) WHERE consumed_at IS NULL`,
					`CREATE TABLE email_change_requests (
						id uuid PRIMARY KEY,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
						old_email text NOT NULL,
						new_email text NOT NULL,
						new_normalized_email text NOT NULL,
						old_code_hash bytea NOT NULL CHECK (octet_length(old_code_hash) = 32),
						new_code_hash bytea NOT NULL CHECK (octet_length(new_code_hash) = 32),
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
						expires_at timestamptz NOT NULL,
						consumed_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX email_change_requests_active_idx ON email_change_requests (person_id, expires_at DESC) WHERE consumed_at IS NULL`,
					`CREATE TABLE curator_recovery_requests (
						id uuid PRIMARY KEY,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						new_email text NOT NULL,
						new_normalized_email text NOT NULL,
						code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
						expires_at timestamptz NOT NULL,
						consumed_at timestamptz,
						created_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX curator_recovery_requests_active_idx ON curator_recovery_requests (person_id, expires_at DESC) WHERE consumed_at IS NULL`,
					`CREATE TABLE push_subscriptions (
						id uuid PRIMARY KEY,
						session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						endpoint_hash bytea NOT NULL UNIQUE CHECK (octet_length(endpoint_hash) = 32),
						created_at timestamptz NOT NULL DEFAULT now(),
						disabled_at timestamptz
					)`,
					`CREATE INDEX push_subscriptions_session_active_idx ON push_subscriptions (session_id) WHERE disabled_at IS NULL`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (
						kind IN ('required_test', 'setup_code', 'invitation_initial', 'invitation_automatic_reminder',
						'invitation_manual_reminder', 'sign_in_code', 'email_change_old_code', 'email_change_new_code', 'curator_recovery_code'))`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_deadline_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_deadline_check CHECK (
						(kind <> 'required_test') = (deliver_before IS NOT NULL))`,
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
					`DELETE FROM jobs WHERE kind = 'send_required_email' AND payload ->> 'delivery_id' IN (SELECT id::text FROM email_deliveries WHERE kind IN ('sign_in_code', 'email_change_old_code', 'email_change_new_code', 'curator_recovery_code'))`,
					`DELETE FROM outbox_events WHERE aggregate_kind = 'email_delivery' AND aggregate_id IN (SELECT public_id FROM email_deliveries WHERE kind IN ('sign_in_code', 'email_change_old_code', 'email_change_new_code', 'curator_recovery_code'))`,
					`DELETE FROM delivery_problems WHERE email_delivery_id IN (SELECT id FROM email_deliveries WHERE kind IN ('sign_in_code', 'email_change_old_code', 'email_change_new_code', 'curator_recovery_code'))`,
					`DROP TABLE IF EXISTS push_subscriptions`,
					`DROP TABLE IF EXISTS curator_recovery_requests`,
					`DROP TABLE IF EXISTS email_change_requests`,
					`DROP TABLE IF EXISTS sign_in_challenges`,
					`DELETE FROM email_deliveries WHERE kind IN ('sign_in_code', 'email_change_old_code', 'email_change_new_code', 'curator_recovery_code')`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_deadline_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_deadline_check CHECK ((kind IN ('setup_code', 'invitation_initial', 'invitation_automatic_reminder', 'invitation_manual_reminder')) = (deliver_before IS NOT NULL))`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (kind IN ('required_test', 'setup_code', 'invitation_initial', 'invitation_automatic_reminder', 'invitation_manual_reminder'))`,
					`ALTER TABLE sessions DROP COLUMN location`,
					`ALTER TABLE sessions DROP COLUMN client_ip`,
					`ALTER TABLE sessions DROP COLUMN platform`,
					`ALTER TABLE sessions DROP COLUMN browser`,
					`ALTER TABLE sessions DROP COLUMN label`,
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
