package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A Moment's place in its Album's Cover Order. Absent means the Moment
		// only competes in capture order, so an untouched Album keeps its
		// existing covers. DDL stays raw; Bun has no builder for constraints.
		_, err := db.ExecContext(ctx, `
ALTER TABLE moments ADD COLUMN cover_position integer CHECK (cover_position > 0);
ALTER TABLE moments ADD CONSTRAINT moments_album_cover_position_key UNIQUE (album_id, cover_position);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE moments DROP COLUMN cover_position;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
