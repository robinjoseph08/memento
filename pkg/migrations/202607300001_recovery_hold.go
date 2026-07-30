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
						ADD COLUMN recovery_released_at timestamptz`,
					`ALTER TABLE system_settings ADD CONSTRAINT system_settings_recovery_state_check CHECK (
						(recovery_nonce_hash IS NULL AND recovery_started_at IS NULL AND
						 recovery_released_at IS NULL AND NOT recovery_hold) OR
						(octet_length(recovery_nonce_hash) = 32 AND recovery_started_at IS NOT NULL AND
						 ((recovery_hold AND recovery_released_at IS NULL) OR
						  (NOT recovery_hold AND recovery_released_at IS NOT NULL AND recovery_released_at >= recovery_started_at)))
					)`,
					`ALTER TABLE sign_in_challenges ADD COLUMN security_epoch bytea`,
					`UPDATE sign_in_challenges AS challenge SET security_epoch = settings.security_epoch
					 FROM system_settings AS settings WHERE settings.id = 1`,
					`ALTER TABLE sign_in_challenges ALTER COLUMN security_epoch SET NOT NULL`,
					`ALTER TABLE sign_in_challenges ADD CONSTRAINT sign_in_challenges_security_epoch_length
					 CHECK (octet_length(security_epoch) = 32)`,
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
					`ALTER TABLE sign_in_challenges DROP CONSTRAINT sign_in_challenges_security_epoch_length`,
					`ALTER TABLE sign_in_challenges DROP COLUMN security_epoch`,
					`ALTER TABLE system_settings DROP CONSTRAINT system_settings_recovery_state_check`,
					`ALTER TABLE system_settings DROP COLUMN recovery_released_at, DROP COLUMN recovery_started_at,
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
