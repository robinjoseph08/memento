package migrations

import (
	"context"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.ExecContext(ctx, `
CREATE TABLE persons (
 id uuid PRIMARY KEY,
 display_name text NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 100),
 is_curator boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL
);
CREATE TABLE identities (
 id uuid PRIMARY KEY,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 provider text NOT NULL CHECK (provider <> ''),
 subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
 email text NOT NULL CHECK (email <> ''),
 created_at timestamptz NOT NULL,
 UNIQUE (provider, subject)
);
CREATE TABLE installation (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 claimed_by uuid REFERENCES persons(id),
 claimed_at timestamptz,
 CHECK ((claimed_by IS NULL) = (claimed_at IS NULL))
);
INSERT INTO installation (singleton) VALUES (true);
CREATE TABLE sessions (
 token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
 identity_id uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 renewed_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at > renewed_at)
);
CREATE INDEX sessions_identity_id_idx ON sessions(identity_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);
`)
			return err
		})
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE sessions, installation, identities, persons`)
		return err
	})
}
