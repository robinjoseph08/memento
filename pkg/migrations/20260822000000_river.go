package migrations

import (
	"context"
	"fmt"

	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return migrateRiver(ctx, db, rivermigrate.DirectionUp, 7)
	}, func(ctx context.Context, db *bun.DB) error {
		return migrateRiver(ctx, db, rivermigrate.DirectionDown, -1)
	})
}

// River commits each schema version separately. Pin this installation to version
// seven; upgrades belong in later Bun migrations. Down removes all queued jobs.
func migrateRiver(ctx context.Context, db *bun.DB, direction rivermigrate.Direction, target int) error {
	migrator, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		return fmt.Errorf("configure River migrations: %w", errorstack.CaptureContext(ctx, err))
	}
	_, err = migrator.Migrate(ctx, direction, &rivermigrate.MigrateOpts{TargetVersion: target})
	if err != nil {
		return fmt.Errorf("migrate River schema: %w", errorstack.CaptureContext(ctx, err))
	}
	return nil
}
