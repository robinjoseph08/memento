package migrations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

type acceptingConnector struct{}

type acceptingDriver struct{}

type acceptingConn struct{}

func (acceptingConnector) Connect(context.Context) (driver.Conn, error) {
	return acceptingConn{}, nil
}

func (acceptingConnector) Driver() driver.Driver {
	return acceptingDriver{}
}

func (acceptingDriver) Open(string) (driver.Conn, error) {
	return acceptingConn{}, nil
}

func (acceptingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (acceptingConn) Close() error {
	return nil
}

func (acceptingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (acceptingConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func TestNewMigrator(t *testing.T) {
	t.Parallel()

	db := bun.NewDB(sql.OpenDB(acceptingConnector{}), pgdialect.New())
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	assert.NotNil(t, NewMigrator(db))
	assert.NotNil(t, newMigrator(db, migrate.NewMigrations()))
}

func TestMigrateRegisteredAllowsEmptyRegistry(t *testing.T) {
	t.Parallel()

	db := bun.NewDB(sql.OpenDB(acceptingConnector{}), pgdialect.New())
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	group, err := migrateRegistered(context.Background(), db, migrate.NewMigrations())
	require.NoError(t, err)
	assert.Zero(t, group.ID)
	assert.Empty(t, group.Migrations)
}
