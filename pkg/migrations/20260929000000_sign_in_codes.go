package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A Sign-in Code emailed to an address. Only its hash is kept, and only
		// the newest code for an address can be verified. Rows outlive their
		// code by up to an hour so the send limits can count them. A decoy
		// row stands in for a code the unknown-address cap kept from being
		// sent, so wrong guesses are answered alike for every address. Every
		// Access Request now comes from a verified address, so the flag goes.
		// DDL stays raw; Bun has no builder for tables and checks.
		_, err := db.ExecContext(ctx, `
CREATE TABLE sign_in_codes (
 id uuid PRIMARY KEY,
 email text NOT NULL CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254),
 code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
 known boolean NOT NULL,
 decoy boolean NOT NULL DEFAULT false CHECK (NOT (decoy AND known)),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
 used_at timestamptz
);
CREATE INDEX sign_in_codes_email_idx ON sign_in_codes(email, created_at);
CREATE INDEX sign_in_codes_created_at_idx ON sign_in_codes(created_at);
ALTER TABLE access_requests DROP COLUMN email_verified`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `
DROP TABLE sign_in_codes;
ALTER TABLE access_requests ADD COLUMN email_verified boolean NOT NULL DEFAULT true;
ALTER TABLE access_requests ALTER COLUMN email_verified DROP DEFAULT`)
		return errorstack.CaptureContext(ctx, err)
	})
}
