package testdb_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedConnectorPreservesTimestampSemantics(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	var row struct {
		Event    time.Time
		Captured time.Time
	}
	require.NoError(t, db.NewSelect().ColumnExpr("'2026-07-01 06:00:00+00'::timestamptz AS event").ColumnExpr("'2026-07-01 23:30:00'::timestamp AS captured").Scan(t.Context(), &row))
	assert.Equal(t, time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC), row.Event)
	assert.Equal(t, time.Date(2026, 7, 1, 23, 30, 0, 0, time.UTC), row.Captured)
	var schema string
	require.NoError(t, db.NewSelect().ColumnExpr("current_schema()").Scan(t.Context(), &schema))
	assert.Contains(t, schema, "memento_test_")
	var number int
	err := db.NewSelect().ColumnExpr("1 / 0").Scan(t.Context(), &number)
	var postgresError *pgconn.PgError
	require.ErrorAs(t, err, &postgresError)
	assert.Equal(t, "22012", postgresError.SQLState())
}

func TestOpenDoesNotExposeMalformedURLCredentials(t *testing.T) {
	t.Parallel()
	db, err := testdb.Open("postgres://user:secret@localhost:bad/db")
	require.Error(t, err)
	assert.Nil(t, db)
	assert.NotContains(t, err.Error(), "secret")
}
