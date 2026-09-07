package migrations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const unlockTimeout = 5 * time.Second

// Migrations contains every registered application migration.
var Migrations = migrate.NewMigrations()

// NewMigrator creates a migrator for the application migration registry.
func NewMigrator(db *bun.DB) *migrate.Migrator {
	return newMigrator(db, Migrations)
}

func newMigrator(db *bun.DB, migrations *migrate.Migrations) *migrate.Migrator {
	return migrate.NewMigrator(db, migrations, migrate.WithMarkAppliedOnSuccess(true))
}

// BringUpToDate initializes the migration tables and applies pending
// migrations.
func BringUpToDate(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	return Migrate(ctx, db)
}

// Migrate initializes the migration tables and applies pending migrations while
// holding Bun's migration lock.
func Migrate(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	return migrateRegistered(ctx, db, Migrations)
}

func migrateRegistered(ctx context.Context, db *bun.DB, registered *migrate.Migrations) (*migrate.MigrationGroup, error) {
	migrator := newMigrator(db, registered)
	if err := migrator.Init(ctx); err != nil {
		return nil, fmt.Errorf("initialize migrations: %w", errorstack.CaptureContext(ctx, err))
	}
	if len(registered.Sorted()) == 0 {
		return new(migrate.MigrationGroup), nil
	}

	return withLock(ctx, migrator, func() (*migrate.MigrationGroup, error) {
		group, err := migrator.Migrate(ctx)
		if err != nil {
			return nil, fmt.Errorf("run migrations: %w", errorstack.CaptureContext(ctx, err))
		}
		return group, nil
	})
}

// Rollback rolls back the last migration group while holding Bun's migration
// lock.
func Rollback(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	return rollbackRegistered(ctx, db, Migrations)
}

func rollbackRegistered(ctx context.Context, db *bun.DB, registered *migrate.Migrations) (*migrate.MigrationGroup, error) {
	migrator := newMigrator(db, registered)
	if err := migrator.Init(ctx); err != nil {
		return nil, fmt.Errorf("initialize migrations: %w", errorstack.CaptureContext(ctx, err))
	}
	if len(registered.Sorted()) == 0 {
		return new(migrate.MigrationGroup), nil
	}

	return withLock(ctx, migrator, func() (*migrate.MigrationGroup, error) {
		group, err := migrator.Rollback(ctx)
		if err != nil {
			return nil, fmt.Errorf("roll back migrations: %w", errorstack.CaptureContext(ctx, err))
		}
		return group, nil
	})
}

func withLock(
	ctx context.Context,
	migrator *migrate.Migrator,
	action func() (*migrate.MigrationGroup, error),
) (group *migrate.MigrationGroup, returnErr error) {
	if err := migrator.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock migrations: %w", errorstack.CaptureContext(ctx, err))
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancel()
		if err := migrator.Unlock(unlockCtx); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("unlock migrations: %w", errorstack.Capture(err)))
		}
	}()

	return action()
}
