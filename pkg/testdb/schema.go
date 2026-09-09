// Package testdb isolates integration tests and local QA in temporary PostgreSQL schemas.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// Schema owns a temporary schema. Close its application pools before Close.
type Schema struct {
	URL  string
	name string
	db   *bun.DB
}

// Open uses a small pool suitable for isolated tests and fixture processes.
func Open(dsn string) (*bun.DB, error) {
	sqlDB, err := database.OpenSQL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse test database URL: invalid PostgreSQL URL")
	}
	sqlDB.SetMaxOpenConns(3)
	sqlDB.SetMaxIdleConns(1)
	return bun.NewDB(sqlDB, pgdialect.New()), nil
}

// NewSchema creates an empty schema without touching existing application data.
func NewSchema(ctx context.Context, baseURL string) (*Schema, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("TEST_DATABASE_URL: invalid PostgreSQL URL")
	}
	db, err := Open(baseURL)
	if err != nil {
		return nil, err
	}
	name := "memento_test_" + uuid.New().String()[:8] + "_" + uuid.New().String()[:8]
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA ?", bun.Ident(name)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create test schema: %w", err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return &Schema{URL: u.String(), name: name, db: db}, nil
}

// Close drops only the schema allocated by NewSchema and closes its pool.
func (s *Schema) Close(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DROP SCHEMA ? CASCADE", bun.Ident(s.name))
	closeErr := s.db.Close()
	if err != nil {
		return err
	}
	return closeErr
}
