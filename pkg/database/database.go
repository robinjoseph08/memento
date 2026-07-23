// Package database owns the Memento PostgreSQL connection.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Open connects only after config validation has confirmed the selected logical database.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*bun.DB, error) {
	connector := pgdriver.NewConnector(pgdriver.WithDSN(cfg.URL))
	db := bun.NewDB(sql.OpenDB(connector), pgdialect.New())
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to Memento database: %w", err)
	}
	var databaseName string
	if err := db.NewRaw(`SELECT current_database()`).Scan(ctx, &databaseName); err != nil {
		_ = db.Close()
		return nil, errors.New("verify Memento logical database")
	}
	if databaseName != cfg.Name {
		_ = db.Close()
		return nil, errors.New("connected logical database does not match Memento configuration")
	}
	return db, nil
}
