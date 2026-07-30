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
					`ALTER TABLE system_settings
						ADD COLUMN recovery_hold boolean NOT NULL DEFAULT false,
						ADD COLUMN recovery_nonce_hash bytea,
						ADD COLUMN recovery_started_at timestamptz,
						ADD COLUMN recovery_reviewed_at timestamptz,
						ADD COLUMN recovery_reviewed_by_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						ADD COLUMN recovery_reviewed_by_session_id uuid REFERENCES sessions(id) ON DELETE RESTRICT,
						ADD COLUMN recovery_released_at timestamptz`,
					`ALTER TABLE system_settings ADD CONSTRAINT system_settings_recovery_state_check CHECK (
						(recovery_nonce_hash IS NULL AND recovery_started_at IS NULL AND recovery_reviewed_at IS NULL AND
						 recovery_reviewed_by_person_id IS NULL AND recovery_reviewed_by_session_id IS NULL AND
						 recovery_released_at IS NULL AND NOT recovery_hold) OR
						(octet_length(recovery_nonce_hash) = 32 AND recovery_started_at IS NOT NULL AND
						 ((recovery_reviewed_at IS NULL AND recovery_reviewed_by_person_id IS NULL AND recovery_reviewed_by_session_id IS NULL) OR
						  (recovery_reviewed_at IS NOT NULL AND recovery_reviewed_at >= recovery_started_at AND
						   recovery_reviewed_by_person_id IS NOT NULL AND recovery_reviewed_by_session_id IS NOT NULL)) AND
						 ((recovery_hold AND recovery_released_at IS NULL) OR
						  (NOT recovery_hold AND recovery_reviewed_at IS NOT NULL AND recovery_released_at IS NOT NULL AND
						   recovery_released_at >= recovery_reviewed_at)))
					)`,
					`CREATE TABLE recovery_nonce_history (
						nonce_hash bytea PRIMARY KEY CHECK (octet_length(nonce_hash) = 32),
						consumed_at timestamptz NOT NULL
					)`,
					`ALTER TABLE sign_in_challenges ADD COLUMN security_epoch bytea`,
					`UPDATE sign_in_challenges AS challenge SET security_epoch = settings.security_epoch
					 FROM system_settings AS settings WHERE settings.id = 1`,
					`ALTER TABLE sign_in_challenges ALTER COLUMN security_epoch SET NOT NULL`,
					`ALTER TABLE sign_in_challenges ADD CONSTRAINT sign_in_challenges_security_epoch_length
					 CHECK (octet_length(security_epoch) = 32)`,
					`CREATE VIEW recovery_curator_sign_in_deliveries AS
					 SELECT delivery.id, delivery.public_id
					 FROM email_deliveries AS delivery
					 JOIN sign_in_challenges AS challenge ON challenge.email_delivery_id = delivery.id
					 JOIN recipient_access_generations AS access ON access.id = challenge.recipient_access_generation_id
					 JOIN person_roles AS role ON role.person_id = access.person_id AND role.role = 'curator'
					 JOIN system_settings AS settings ON settings.id = 1 AND settings.recovery_hold
					  AND settings.security_epoch = challenge.security_epoch
					 WHERE delivery.kind = 'sign_in_code'`,
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
					`DROP VIEW recovery_curator_sign_in_deliveries`,
					`ALTER TABLE sign_in_challenges DROP CONSTRAINT sign_in_challenges_security_epoch_length`,
					`ALTER TABLE sign_in_challenges DROP COLUMN security_epoch`,
					`DROP TABLE recovery_nonce_history`,
					`ALTER TABLE system_settings DROP CONSTRAINT system_settings_recovery_state_check`,
					`ALTER TABLE system_settings DROP COLUMN recovery_released_at,
					 DROP COLUMN recovery_reviewed_by_session_id, DROP COLUMN recovery_reviewed_by_person_id,
					 DROP COLUMN recovery_reviewed_at, DROP COLUMN recovery_started_at,
					 DROP COLUMN recovery_nonce_hash, DROP COLUMN recovery_hold`,
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
