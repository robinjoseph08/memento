package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// The browser-to-app code is a Hand-off Code, not a Sign-in Code, which
		// is the emailed verification code of ADR 0014. DDL stays raw; Bun has
		// no builder for renames.
		_, err := db.ExecContext(ctx, `
ALTER TABLE mobile_sign_in_codes RENAME TO handoff_codes;
ALTER INDEX mobile_sign_in_codes_identity_id_idx RENAME TO handoff_codes_identity_id_idx`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `
ALTER INDEX handoff_codes_identity_id_idx RENAME TO mobile_sign_in_codes_identity_id_idx;
ALTER TABLE handoff_codes RENAME TO mobile_sign_in_codes`)
		return errorstack.CaptureContext(ctx, err)
	})
}
