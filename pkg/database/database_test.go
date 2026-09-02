package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenUsesPostgreSQL(t *testing.T) {
	t.Parallel()

	db, err := open(config.NewForTest())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	assert.Contains(t, fmt.Sprintf("%T", db.Dialect()), "pgdialect")
}

func TestOpenRejectsMalformedDatabaseURL(t *testing.T) {
	t.Parallel()

	cfg := config.NewForTest()
	cfg.DatabaseURL = ":bad"
	db, err := open(cfg)
	require.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "parse database URL")
}

func TestWithLogging(t *testing.T) {
	t.Parallel()

	ctx := WithLogging(context.Background())
	enabled, ok := ctx.Value(loggingContextKey).(bool)
	require.True(t, ok)
	assert.True(t, enabled)
}
