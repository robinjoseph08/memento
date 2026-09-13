package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// Multi-statement schema DDL stays raw; Bun has no builder for these constraints.
			_, err := tx.ExecContext(ctx, `
CREATE TABLE mail_deliveries (
 id uuid PRIMARY KEY,
 kind text NOT NULL CHECK (kind <> ''),
 recipient text NOT NULL CHECK (recipient <> ''),
 subject text NOT NULL,
 body text NOT NULL,
 status text NOT NULL CHECK (status IN ('queued', 'sending', 'delivered', 'failed', 'uncertain')),
 attempts integer NOT NULL DEFAULT 0,
 message text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 delivered_at timestamptz,
 CHECK ((status = 'delivered') = (delivered_at IS NOT NULL))
);
CREATE TABLE invitations (
 id uuid PRIMARY KEY,
 person_id uuid NOT NULL REFERENCES persons(id),
 preauthorization_id uuid NOT NULL UNIQUE REFERENCES preauthorizations(id),
 delivery_id uuid NOT NULL UNIQUE REFERENCES mail_deliveries(id),
 sent_by uuid NOT NULL REFERENCES persons(id),
 created_at timestamptz NOT NULL
);
CREATE INDEX invitations_person_id_idx ON invitations(person_id);
CREATE TABLE access_requests (
 id uuid PRIMARY KEY,
 kind text NOT NULL CHECK (kind IN ('join', 'album')),
 provider text NOT NULL CHECK (provider <> ''),
 subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
 email text NOT NULL CHECK (email <> ''),
 email_verified boolean NOT NULL,
 display_name text NOT NULL,
 person_id uuid REFERENCES persons(id),
 album_id uuid REFERENCES albums(id) ON DELETE SET NULL,
 status text NOT NULL CHECK (status IN ('pending', 'approved', 'denied')),
 sign_in_count integer NOT NULL DEFAULT 1 CHECK (sign_in_count >= 1),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 resolved_at timestamptz,
 resolved_by uuid REFERENCES persons(id),
 CHECK ((status = 'pending') = (resolved_at IS NULL)),
 CHECK (kind = 'join' OR person_id IS NOT NULL)
);
CREATE UNIQUE INDEX access_requests_open_identity_idx ON access_requests(provider, subject) WHERE status <> 'approved' AND kind = 'join';
CREATE UNIQUE INDEX access_requests_open_album_idx ON access_requests(person_id, album_id) WHERE status <> 'approved' AND kind = 'album';
CREATE TABLE announced_albums (
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 announced_at timestamptz NOT NULL,
 PRIMARY KEY (person_id, album_id)
);
CREATE TABLE announced_entries (
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 entry_id uuid NOT NULL REFERENCES album_entries(id) ON DELETE CASCADE,
 announced_at timestamptz NOT NULL,
 PRIMARY KEY (person_id, entry_id)
);
`)
			return errorstack.CaptureContext(ctx, err)
		})
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE announced_entries, announced_albums, access_requests, invitations, mail_deliveries`)
		return errorstack.CaptureContext(ctx, err)
	})
}
