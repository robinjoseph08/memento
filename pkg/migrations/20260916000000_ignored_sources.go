package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// An Immich album a Curator keeps off the import list. The row is the
		// whole decision, so restoring it is a delete. DDL stays raw; Bun has
		// no builder for tables and constraints.
		_, err := db.ExecContext(ctx, `
CREATE TABLE ignored_sources (
 source_id text PRIMARY KEY CHECK (length(source_id) > 0),
 created_at timestamptz NOT NULL
);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE ignored_sources;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
