//go:build integration

package main

import (
	"context"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/uptrace/bun"
)

func openFixtureDatabase(ctx context.Context, dsn string) (*bun.DB, error) {
	return testdb.OpenURL(ctx, dsn)
}
