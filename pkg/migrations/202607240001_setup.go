package migrations

import (
	"context"
	"crypto/rand"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			securityEpoch := make([]byte, 32)
			if _, err := rand.Read(securityEpoch); err != nil {
				return err
			}
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`ALTER TABLE system_settings ADD COLUMN security_epoch bytea`,
					`ALTER TABLE system_settings ADD CONSTRAINT system_settings_security_epoch_length CHECK (octet_length(security_epoch) = 32)`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (kind IN ('required_test', 'setup_code'))`,
					`CREATE TABLE people (
						id uuid PRIMARY KEY,
						display_name text NOT NULL CHECK (display_name <> '' AND char_length(display_name) <= 120),
						sort_name text NOT NULL CHECK (sort_name <> '' AND char_length(sort_name) <= 120),
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						archived_at timestamptz
					)`,
					`CREATE TABLE person_roles (
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						role text NOT NULL CHECK (role IN ('curator', 'recipient')),
						created_at timestamptz NOT NULL DEFAULT now(),
						PRIMARY KEY (person_id, role)
					)`,
					`CREATE UNIQUE INDEX person_roles_sole_curator_idx ON person_roles (role) WHERE role = 'curator'`,
					`CREATE TABLE recipient_access_generations (
						id uuid PRIMARY KEY,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						generation integer NOT NULL CHECK (generation > 0),
						state text NOT NULL CHECK (state IN ('pending', 'onboarding', 'completed', 'suspended', 'revoked')),
						is_current boolean NOT NULL DEFAULT true,
						onboarding_completed_at timestamptz,
						ended_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						UNIQUE (person_id, generation),
						CHECK ((state = 'revoked') = (ended_at IS NOT NULL)),
						CHECK ((state = 'completed' AND onboarding_completed_at IS NOT NULL) OR state <> 'completed')
					)`,
					`CREATE UNIQUE INDEX recipient_access_generations_current_idx ON recipient_access_generations (person_id) WHERE is_current`,
					`CREATE TABLE recipient_emails (
						id uuid PRIMARY KEY,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						email text NOT NULL CHECK (email <> ''),
						normalized_email text NOT NULL CHECK (normalized_email <> ''),
						is_current boolean NOT NULL DEFAULT true,
						created_at timestamptz NOT NULL DEFAULT now(),
						ended_at timestamptz
					)`,
					`CREATE UNIQUE INDEX recipient_emails_current_generation_idx ON recipient_emails (recipient_access_generation_id) WHERE is_current`,
					`CREATE UNIQUE INDEX recipient_emails_current_normalized_idx ON recipient_emails (normalized_email) WHERE is_current`,
					`CREATE TABLE onboarding_choices (
						recipient_access_generation_id uuid PRIMARY KEY REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						privacy_acknowledged boolean NOT NULL,
						engagement_acknowledged boolean NOT NULL,
						interest_list_acknowledged boolean NOT NULL,
						email_preference text NOT NULL CHECK (email_preference IN ('immediate', 'weekly', 'none')),
						completed_at timestamptz NOT NULL,
						CHECK (privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged)
					)`,
					`CREATE TABLE notification_preferences (
						recipient_access_generation_id uuid PRIMARY KEY REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						email_preference text NOT NULL CHECK (email_preference IN ('immediate', 'weekly', 'none')),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE TABLE login_challenges (
						id uuid PRIMARY KEY,
						challenge_hash bytea NOT NULL UNIQUE CHECK (octet_length(challenge_hash) = 32),
						code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
						display_name text NOT NULL CHECK (display_name <> ''),
						email text NOT NULL CHECK (email <> ''),
						normalized_email text NOT NULL CHECK (normalized_email <> ''),
						email_delivery_id bigint NOT NULL REFERENCES email_deliveries(id) ON DELETE RESTRICT,
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
						expires_at timestamptz NOT NULL,
						verified_at timestamptz,
						verification_token_hash bytea UNIQUE CHECK (verification_token_hash IS NULL OR octet_length(verification_token_hash) = 32),
						consumed_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						CHECK (verified_at IS NULL OR verification_token_hash IS NOT NULL),
						CHECK (consumed_at IS NULL OR verified_at IS NOT NULL)
					)`,
					`CREATE INDEX login_challenges_active_idx ON login_challenges (expires_at, id) WHERE consumed_at IS NULL`,
					`CREATE TABLE sessions (
						id uuid PRIMARY KEY,
						credential_hash bytea NOT NULL UNIQUE CHECK (octet_length(credential_hash) = 32),
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						security_epoch bytea NOT NULL CHECK (octet_length(security_epoch) = 32),
						session_type text NOT NULL CHECK (session_type IN ('trusted', 'public')),
						created_at timestamptz NOT NULL DEFAULT now(),
						last_activity_at timestamptz NOT NULL DEFAULT now(),
						idle_expires_at timestamptz,
						absolute_expires_at timestamptz,
						revoked_at timestamptz,
						CHECK ((session_type = 'trusted' AND idle_expires_at IS NOT NULL AND absolute_expires_at IS NULL) OR
						       (session_type = 'public' AND idle_expires_at IS NULL AND absolute_expires_at IS NOT NULL))
					)`,
					`CREATE INDEX sessions_active_credential_idx ON sessions (credential_hash) WHERE revoked_at IS NULL`,
					`CREATE INDEX sessions_person_active_idx ON sessions (person_id, created_at DESC) WHERE revoked_at IS NULL`,
					`CREATE INDEX sessions_expiry_idx ON sessions (COALESCE(idle_expires_at, absolute_expires_at)) WHERE revoked_at IS NULL`,
					`CREATE TABLE security_audit_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						actor_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						subject_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action <> ''),
						outcome text NOT NULL CHECK (outcome <> ''),
						client_ip inet,
						user_agent text NOT NULL DEFAULT '',
						session_id uuid REFERENCES sessions(id) ON DELETE RESTRICT,
						metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX security_audit_subject_time_idx ON security_audit_events (subject_person_id, created_at DESC) WHERE subject_person_id IS NOT NULL`,
					`CREATE INDEX security_audit_actor_time_idx ON security_audit_events (actor_person_id, created_at DESC) WHERE actor_person_id IS NOT NULL`,
					`CREATE INDEX security_audit_action_time_idx ON security_audit_events (action, created_at DESC)`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				if _, err := tx.NewRaw(`UPDATE system_settings SET security_epoch = ? WHERE id = 1`, securityEpoch).Exec(ctx); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `ALTER TABLE system_settings ALTER COLUMN security_epoch SET NOT NULL`)
				return err
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`DROP TABLE IF EXISTS security_audit_events`,
					`DROP TABLE IF EXISTS sessions`,
					`DROP TABLE IF EXISTS login_challenges`,
					`DROP TABLE IF EXISTS notification_preferences`,
					`DROP TABLE IF EXISTS onboarding_choices`,
					`DROP TABLE IF EXISTS recipient_emails`,
					`DROP TABLE IF EXISTS recipient_access_generations`,
					`DROP TABLE IF EXISTS person_roles`,
					`DROP TABLE IF EXISTS people`,
					`DELETE FROM jobs WHERE kind = 'send_required_email' AND payload ->> 'delivery_id' IN (SELECT id::text FROM email_deliveries WHERE kind = 'setup_code')`,
					`DELETE FROM outbox_events WHERE aggregate_kind = 'email_delivery' AND aggregate_id IN (SELECT public_id FROM email_deliveries WHERE kind = 'setup_code')`,
					`DELETE FROM delivery_problems WHERE email_delivery_id IN (SELECT id FROM email_deliveries WHERE kind = 'setup_code')`,
					`DELETE FROM email_deliveries WHERE kind = 'setup_code'`,
					`ALTER TABLE email_deliveries DROP CONSTRAINT email_deliveries_kind_check`,
					`ALTER TABLE email_deliveries ADD CONSTRAINT email_deliveries_kind_check CHECK (kind IN ('required_test'))`,
					`ALTER TABLE system_settings DROP COLUMN IF EXISTS security_epoch`,
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
