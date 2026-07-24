//go:build integration

// Package testdb creates isolated PostgreSQL schemas for integration tests.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func Open(t *testing.T) *bun.DB {
	t.Helper()
	dsn := os.Getenv("MEMENTO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MEMENTO_TEST_DATABASE_URL is not set")
	}
	base := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	requirePing(t, base)
	schema := "test_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := base.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		_ = base.Close()
		t.Fatalf("create schema: %v", err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(parsed.String()))), pgdialect.New())
	requirePing(t, db)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = base.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = base.Close()
	})
	return db
}

func requirePing(t *testing.T, db *bun.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(fmt.Errorf("ping PostgreSQL: %w", err))
	}
}
