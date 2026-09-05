package testdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/internal/devtool"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/uptrace/bun"
)

// New migrates an isolated schema per top-level test and records setup cost.
// TEST_DATABASE_URL overrides the local worktree database, and is required in CI.
func New(t *testing.T) *bun.DB {
	t.Helper()
	started := time.Now()
	ctx := t.Context()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("TEST_DATABASE_URL is required in CI")
		}
		env, err := devtool.ResolveEnvironment(ctx)
		if err != nil {
			t.Fatalf("resolve local test database: %v", err)
		}
		baseURL = fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", env.PostgresPort, env.CurrentDatabase)
	}
	schema, err := NewSchema(ctx, baseURL)
	if err != nil {
		t.Fatalf("test database: %v; run mise setup or set TEST_DATABASE_URL", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := schema.Close(ctx); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	db, err := Open(schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := migrations.BringUpToDate(ctx, db); err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated database setup including migrations: %s", time.Since(started))
	return db
}
