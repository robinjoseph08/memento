package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// DDL defines the scope-specific access constraints.
		_, err := db.ExecContext(ctx, `
CREATE TABLE album_access_decisions (
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 decision text NOT NULL CHECK (decision = 'allow'),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (album_id,person_id)
);
ALTER TABLE album_entries ADD CONSTRAINT album_entries_id_album_id_key UNIQUE (id,album_id);
CREATE TABLE entry_access_decisions (
 entry_id uuid NOT NULL,
 album_id uuid NOT NULL,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 decision text NOT NULL CHECK (decision IN ('allow','deny')),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (entry_id,person_id),
 FOREIGN KEY (entry_id,album_id) REFERENCES album_entries(id,album_id) ON DELETE CASCADE
);
CREATE INDEX entry_access_decisions_album_person_idx ON entry_access_decisions(album_id,person_id);
CREATE TABLE access_deletion_undos (
 id uuid PRIMARY KEY,
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 moment_id uuid,
 entry_id uuid,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 updated_at timestamptz NOT NULL,
 CHECK (moment_id IS NULL OR entry_id IS NULL),
 FOREIGN KEY (moment_id,album_id) REFERENCES moments(id,album_id) ON DELETE CASCADE,
 FOREIGN KEY (entry_id,album_id) REFERENCES album_entries(id,album_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX access_deletion_undos_album_key ON access_deletion_undos(album_id,person_id) WHERE moment_id IS NULL AND entry_id IS NULL;
CREATE UNIQUE INDEX access_deletion_undos_moment_key ON access_deletion_undos(moment_id,person_id) WHERE moment_id IS NOT NULL;
CREATE UNIQUE INDEX access_deletion_undos_entry_key ON access_deletion_undos(entry_id,person_id) WHERE entry_id IS NOT NULL;
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		// DDL reverses the access tables.
		_, err := db.ExecContext(ctx, `DROP TABLE access_deletion_undos; DROP TABLE entry_access_decisions; ALTER TABLE album_entries DROP CONSTRAINT album_entries_id_album_id_key; DROP TABLE album_access_decisions;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
