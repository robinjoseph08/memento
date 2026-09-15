package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// DDL records a Curator's exclusion of a source asset on the retained
		// Album Entry, so a later check never offers it as a new addition.
		_, err := db.ExecContext(ctx, `
ALTER TABLE album_entries ADD COLUMN IF NOT EXISTS excluded_at timestamptz
 CONSTRAINT album_entries_excluded_requires_removed_check CHECK (excluded_at IS NULL OR removed_at IS NOT NULL);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE album_entries DROP COLUMN IF EXISTS excluded_at;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
