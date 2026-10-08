package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A Join places an Album's offered media among a Person's own (ADR
		// 0015). It outlives the Offers it covers, so nothing but Leave,
		// deleting the Album, or deleting the Person removes it. DDL stays raw
		// like the earlier migrations.
		_, err := db.ExecContext(ctx, `
CREATE TABLE album_joins (
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (album_id, person_id)
);
CREATE INDEX album_joins_person_id_idx ON album_joins(person_id);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE album_joins;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
