package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/web-app-template/pkg/config"
	"github.com/robinjoseph08/web-app-template/pkg/database"
	"github.com/robinjoseph08/web-app-template/pkg/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"github.com/urfave/cli/v2"
)

func main() {
	log := logger.New()
	if err := run(); err != nil {
		log.Err(err).Fatal("migrations command failed")
	}
}

func run() error {
	app := &cli.App{
		Name:  "migrations",
		Usage: "interact with database migrations",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "create migration tables",
				Action: withDatabase(func(c *cli.Context, db *bun.DB) error {
					return migrations.NewMigrator(db).Init(c.Context)
				}),
			},
			{
				Name:  "migrate",
				Usage: "run pending migrations",
				Action: withDatabase(func(c *cli.Context, db *bun.DB) error {
					group, err := migrations.Migrate(c.Context, db)
					if err != nil {
						return err
					}
					if group.ID == 0 {
						fmt.Println("There are no new migrations to run")
						return nil
					}
					fmt.Printf("Migrated to %s\n", group)
					return nil
				}),
			},
			{
				Name:  "rollback",
				Usage: "roll back the last migration group",
				Action: withDatabase(func(c *cli.Context, db *bun.DB) error {
					group, err := migrations.Rollback(c.Context, db)
					if err != nil {
						return err
					}
					if group.ID == 0 {
						fmt.Println("There are no groups to roll back")
						return nil
					}
					fmt.Printf("Rolled back %s\n", group)
					return nil
				}),
			},
			{
				Name:  "create",
				Usage: "create a Go migration",
				Action: func(c *cli.Context) error {
					name := strings.Join(c.Args().Slice(), "_")
					if name == "" {
						return fmt.Errorf("migration name is required")
					}
					file, err := migrations.NewMigrator(nil).CreateGoMigration(
						c.Context,
						name,
						migrate.WithGoTemplate(migrationTemplate),
					)
					if err != nil {
						return err
					}
					fmt.Printf("Created migration %s (%s)\n", file.Name, file.Path)
					return nil
				},
			},
			{
				Name:  "status",
				Usage: "print migration status",
				Action: withDatabase(func(c *cli.Context, db *bun.DB) error {
					migrator := migrations.NewMigrator(db)
					if err := migrator.Init(c.Context); err != nil {
						return err
					}
					status, err := migrator.MigrationsWithStatus(c.Context)
					if err != nil {
						return err
					}
					fmt.Printf("Migrations: %s\n", status)
					fmt.Printf("Unapplied migrations: %s\n", status.Unapplied())
					fmt.Printf("Last migration group: %s\n", status.LastGroup())
					return nil
				}),
			},
		},
	}

	return app.Run(os.Args)
}

func withDatabase(action func(*cli.Context, *bun.DB) error) cli.ActionFunc {
	return func(c *cli.Context) (returnErr error) {
		cfg, err := config.New()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		db, err := database.New(cfg)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() {
			if closeErr := db.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close database: %w", closeErr))
			}
		}()

		return action(c, db)
	}
}

const migrationTemplate = `package %s

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	up := func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "")
		return err
	}

	down := func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "")
		return err
	}

	Migrations.MustRegister(up, down)
}
`
