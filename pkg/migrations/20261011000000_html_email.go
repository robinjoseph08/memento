package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// Queued email keeps its HTML alongside the text body. Rows written
		// before this stay text-only. DDL stays raw like the earlier migrations.
		_, err := db.ExecContext(ctx, `ALTER TABLE mail_deliveries ADD COLUMN html text NOT NULL DEFAULT '';`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE mail_deliveries DROP COLUMN html;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
