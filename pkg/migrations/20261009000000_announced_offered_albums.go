package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// An Album is announced as new to view once per Person, the first time
		// it holds offered media they have not joined. The row outlives Leave
		// and withdrawn Offers so neither makes the Album new again. DDL stays
		// raw like the earlier migrations.
		_, err := db.ExecContext(ctx, `
CREATE TABLE announced_offered_albums (
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 announced_at timestamptz NOT NULL,
 PRIMARY KEY (person_id, album_id)
);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE announced_offered_albums;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
