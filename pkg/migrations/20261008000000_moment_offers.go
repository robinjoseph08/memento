package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A Moment Offer offers one Moment to a Circle or withholds it from an
		// Album Offer (ADR 0015). No row inherits the Album Offer, so Moments
		// added later are offered unless withheld. Deleting the Moment or the
		// Circle removes its decisions. DDL stays raw like the earlier
		// migrations.
		_, err := db.ExecContext(ctx, `
CREATE TABLE moment_offers (
 moment_id uuid NOT NULL,
 album_id uuid NOT NULL,
 circle_id uuid NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
 decision text NOT NULL CHECK (decision IN ('offer','withhold')),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (moment_id, circle_id),
 FOREIGN KEY (moment_id, album_id) REFERENCES moments(id, album_id) ON DELETE CASCADE
);
CREATE INDEX moment_offers_album_id_idx ON moment_offers(album_id);
CREATE INDEX moment_offers_circle_id_idx ON moment_offers(circle_id);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE moment_offers;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
