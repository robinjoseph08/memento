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

var (
	errParseURL             = errors.New("parse Memento database URL")
	errVerifyDatabase       = errors.New("verify Memento logical database")
	errDatabaseNameMismatch = errors.New("connected logical database does not match Memento configuration")
)

// Open connects only after config validation has confirmed the selected logical database.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*bun.DB, error) {
	connector, err := pgdriver.NewDriver().OpenConnector(cfg.URL)
	if err != nil {
		return nil, errParseURL
	}
	db := bun.NewDB(sql.OpenDB(connector), pgdialect.New())
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to Memento database: %w", err)
	}
	var databaseName string
	if err := db.NewRaw(`SELECT current_database()`).Scan(ctx, &databaseName); err != nil {
		_ = db.Close()
		return nil, errVerifyDatabase
	}
	if databaseName != cfg.Name {
		_ = db.Close()
		return nil, errDatabaseNameMismatch
	}
	return db, nil
}
