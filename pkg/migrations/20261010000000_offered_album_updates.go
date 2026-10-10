package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// "Tell me about albums I can join" sits beside the email preference
		// and starts on for new and existing People. DDL stays raw like the
		// earlier migrations.
		_, err := db.ExecContext(ctx, `ALTER TABLE persons ADD COLUMN offered_album_updates boolean NOT NULL DEFAULT true;`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE persons DROP COLUMN offered_album_updates;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
