package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// Whether Immich reports the asset as edited. Rows written before this
		// stay NULL, unknown, until the next import or synchronization writes
		// them. DDL stays raw like the earlier migrations.
		_, err := db.ExecContext(ctx, `ALTER TABLE media_items ADD COLUMN edited boolean;`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE media_items DROP COLUMN edited;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
