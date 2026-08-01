//go:build integration

package testdb

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

var (
	errDatabaseURLInvalid = errors.New("test database URL is invalid")
	errDatabaseNotLocal   = errors.New("test database must be loopback-only")
)

// OpenURL opens a test-only PostgreSQL pool after proving loopback readiness.
func OpenURL(ctx context.Context, dsn string) (*bun.DB, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Hostname() == "" {
		return nil, errDatabaseURLInvalid
	}
	host := parsed.Hostname()
	if host != "localhost" {
		address := net.ParseIP(host)
		if address == nil || !address.IsLoopback() {
			return nil, errDatabaseNotLocal
		}
	}
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
