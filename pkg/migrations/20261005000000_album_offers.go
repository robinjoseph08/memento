package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// An Album Offer makes a whole Album available to a Circle (ADR 0015).
		// An Album can only be offered, so a row is the Offer and deleting it
		// withdraws it. Deleting the Album or the Circle withdraws it too. DDL
		// stays raw like the earlier migrations.
		_, err := db.ExecContext(ctx, `
CREATE TABLE album_offers (
 album_id uuid NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
 circle_id uuid NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (album_id, circle_id)
);
CREATE INDEX album_offers_circle_id_idx ON album_offers(circle_id);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE album_offers;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
