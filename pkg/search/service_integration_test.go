//go:build integration

package search

import (
	"context"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestSearchTransactionAppliesThePostgreSQLStatementTimeout(t *testing.T) {
	db := testdb.Open(t)
	var actual string
	err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		if err := setSearchStatementTimeout(ctx, tx); err != nil {
			return err
		}
		return tx.NewRaw(`SHOW statement_timeout`).Scan(ctx, &actual)
	})
	require.NoError(t, err)
	assert.Equal(t, searchStatementTimeout, actual)
}
