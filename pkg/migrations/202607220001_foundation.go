// Package migrations contains the ordered Bun migrations for Memento.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

var (
	collection                    = migrate.NewMigrations()
	errUnappliedMigrations        = errors.New("database has unapplied migrations")
	errUnknownMigrations          = errors.New("database contains unknown migrations")
	errRequiredExtensions         = errors.New("required PostgreSQL extensions are unavailable")
	errSystemSettingsInconsistent = errors.New("system settings singleton is inconsistent")
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public`,
					`CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`,
					`CREATE TABLE system_settings (
						id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
						setup_complete boolean NOT NULL DEFAULT false,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now()
					)`,
					`INSERT INTO system_settings (id) VALUES (1)`,
					`CREATE TABLE jobs (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						kind text NOT NULL,
						payload jsonb NOT NULL DEFAULT '{}'::jsonb,
						status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed')),
						attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
						available_at timestamptz NOT NULL DEFAULT now(),
						lease_owner text,
						lease_expires_at timestamptz,
						created_at timestamptz NOT NULL DEFAULT now(),
						updated_at timestamptz NOT NULL DEFAULT now(),
						CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
						CHECK ((status = 'running') = (lease_owner IS NOT NULL))
					)`,
					`CREATE INDEX jobs_claimable_idx ON jobs (available_at, id)
						WHERE status = 'pending' OR status = 'running'`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS jobs`); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS system_settings`)
				return err
			})
		},
	)
}

// Apply initializes Bun's migration ledger and applies migrations while holding its PostgreSQL advisory lock.
func Apply(ctx context.Context, db *bun.DB) error {
	return applyCollection(ctx, db, collection)
}

func applyCollection(ctx context.Context, db *bun.DB, migrations *migrate.Migrations) (err error) {
	// The lock must precede Migrator.Init. PostgreSQL can race two concurrent
	// CREATE TABLE IF NOT EXISTS statements while their catalog rows are still
	// invisible, and extensions are scoped to the whole logical database rather
	// than the caller's search path.
	lockConnection, err := db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended(current_database() || ':memento:migrations', 0))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := lockConnection.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended(current_database() || ':memento:migrations', 0))`); unlockErr != nil {
			unlockErr = fmt.Errorf("unlock migrations: %w", unlockErr)
			if err == nil {
				err = unlockErr
			} else {
				err = errors.Join(err, unlockErr)
			}
		}
	}()

	migrator := migrate.NewMigrator(db, migrations, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Current reports whether the database has every known migration and no unknown applied migrations.
func Current(ctx context.Context, db *bun.DB) error {
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	statuses, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		return fmt.Errorf("read migration status: %w", err)
	}
	for _, migration := range statuses {
		if !migration.IsApplied() {
			return errUnappliedMigrations
		}
	}
	missing, err := migrator.MissingMigrations(ctx)
	if err != nil {
		return fmt.Errorf("read missing migrations: %w", err)
	}
	if len(missing) != 0 {
		return errUnknownMigrations
	}
	return nil
}

// Extensions verifies that PostgreSQL loaded both search extensions in this logical database.
func Extensions(ctx context.Context, db *bun.DB) error {
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent', 'pg_trgm')`).Scan(ctx, &count); err != nil {
		return fmt.Errorf("verify required extensions: %w", err)
	}
	if count != 2 {
		return errRequiredExtensions
	}
	return nil
}

// SetupConsistent verifies the foundational singleton without exposing its state.
func SetupConsistent(ctx context.Context, db *bun.DB) error {
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM system_settings WHERE id = 1`).Scan(ctx, &count); err != nil {
		return fmt.Errorf("verify system settings: %w", err)
	}
	if count != 1 {
		return errSystemSettingsInconsistent
	}
	return nil
}
