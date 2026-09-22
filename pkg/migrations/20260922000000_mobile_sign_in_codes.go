package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A code the web sign-in hands to the Mobile App, which exchanges it
		// once for a session of its own. Only the hash is kept, as with
		// sessions. DDL stays raw; Bun has no builder for tables.
		_, err := db.ExecContext(ctx, `
CREATE TABLE mobile_sign_in_codes (
 code_hash bytea PRIMARY KEY,
 identity_id uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
)`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE mobile_sign_in_codes`)
		return errorstack.CaptureContext(ctx, err)
	})
}
