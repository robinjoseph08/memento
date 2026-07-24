//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestFailedMigrationRollsBackAndIsNotMarkedApplied(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	shouldFail := true
	migrations := migrate.NewMigrations()
	migrations.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.ExecContext(ctx, `CREATE TABLE failed_migration_side_effect (id bigint PRIMARY KEY)`); err != nil {
					return err
				}
				if shouldFail {
					return errors.New("injected migration failure")
				}
				return nil
			})
		},
		func(context.Context, *bun.DB) error { return nil },
	)

	err := applyCollection(ctx, db, migrations)
	require.ErrorContains(t, err, "injected migration failure")
	var relation sql.NullString
	require.NoError(t, db.NewRaw(`SELECT to_regclass(current_schema() || '.failed_migration_side_effect')`).Scan(ctx, &relation))
	assert.False(t, relation.Valid)
	var applied int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM bun_migrations WHERE name = '202607220002'`).Scan(ctx, &applied))
	assert.Zero(t, applied)

	shouldFail = false
	require.NoError(t, applyCollection(ctx, db, migrations))
	require.NoError(t, db.NewRaw(`SELECT to_regclass(current_schema() || '.failed_migration_side_effect')`).Scan(ctx, &relation))
	assert.True(t, relation.Valid)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM bun_migrations WHERE name = '202607220002'`).Scan(ctx, &applied))
	assert.Equal(t, 1, applied)
}
