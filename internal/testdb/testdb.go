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
	"strings"
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

// Clone opens another pool in the source database schema with driver options
// that are specific to the behavior under test.
func Clone(t *testing.T, source *bun.DB, options ...pgdriver.Option) *bun.DB {
	t.Helper()
	var schema string
	if err := source.NewRaw(`SELECT current_schema()`).Scan(context.Background(), &schema); err != nil {
		t.Fatalf("read test schema: %v", err)
	}
	parsed, err := url.Parse(os.Getenv("MEMENTO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	connectorOptions := append([]pgdriver.Option{pgdriver.WithDSN(parsed.String())}, options...)
	clone := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(connectorOptions...)), pgdialect.New())
	t.Cleanup(func() {
		_ = clone.Close()
	})
	requirePing(t, clone)
	return clone
}

// WaitForBlockedQueries waits until at least count matching PostgreSQL backends
// are observably blocked by blockerPID. The four-second bound is long enough for
// a transaction to reach a controlled lock under full-suite contention while
// keeping lock-order failures fast and diagnostic.
func WaitForBlockedQueries(t *testing.T, ctx context.Context, db *bun.DB, blockerPID int, pattern string, count int) []int {
	t.Helper()
	if count < 1 {
		t.Fatalf("blocked query count must be positive: %d", count)
	}
	pollCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	lastState := "no matching backend"
	for {
		type waitState struct {
			PID             int
			WaitType        string
			WaitEvent       string
			Blockers        string
			BlockedByTarget bool
			Query           string
		}
		var states []waitState
		err := db.NewRaw(`
			SELECT pid, COALESCE(wait_event_type, '') AS wait_type,
				COALESCE(wait_event, '') AS wait_event,
				array_to_string(pg_blocking_pids(pid), ',') AS blockers,
				? = ANY(pg_blocking_pids(pid)) AS blocked_by_target, query
			FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> ? AND query LIKE ?
			ORDER BY pid
		`, blockerPID, blockerPID, pattern).Scan(pollCtx, &states)
		if err != nil {
			if pollCtx.Err() != nil {
				t.Fatalf("queries did not reach the controlled lock before the 4s bound: pattern=%q blocker_pid=%d wanted=%d last_state=%s error=%v", pattern, blockerPID, count, lastState, pollCtx.Err())
			}
			t.Fatalf("inspect blocked queries: pattern=%q blocker_pid=%d last_state=%s error=%v", pattern, blockerPID, lastState, err)
		}
		blocked := make([]int, 0, len(states))
		observed := make([]string, 0, len(states))
		for _, state := range states {
			observed = append(observed, fmt.Sprintf("pid=%d wait_type=%q wait_event=%q blockers=%q blocked_by_target=%t query=%q", state.PID, state.WaitType, state.WaitEvent, state.Blockers, state.BlockedByTarget, state.Query))
			if state.WaitType == "Lock" && state.BlockedByTarget {
				blocked = append(blocked, state.PID)
			}
		}
		lastState = "no matching backend"
		if len(observed) > 0 {
			lastState = strings.Join(observed, "; ")
		}
		if len(blocked) >= count {
			return blocked
		}
		select {
		case <-ticker.C:
		case <-pollCtx.Done():
			t.Fatalf("queries did not reach the controlled lock before the 4s bound: pattern=%q blocker_pid=%d wanted=%d observed=%d last_state=%s error=%v", pattern, blockerPID, count, len(blocked), lastState, pollCtx.Err())
		}
	}
}

// WaitForDatabaseDeadline polls a database timestamp until PostgreSQL's wall
// clock reaches it. The three-second bound is six times the subsecond test
// deadlines that use this helper and reports the last observed database state.
func WaitForDatabaseDeadline(t *testing.T, ctx context.Context, db bun.IDB, description, query string, args ...any) {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastDatabaseTime, lastDeadline time.Time
	for {
		err := db.NewRaw(query, args...).Scan(pollCtx, &lastDatabaseTime, &lastDeadline)
		if err != nil {
			if pollCtx.Err() != nil {
				t.Fatalf("database deadline was not reached before the 3s bound: description=%q database_time=%s deadline=%s error=%v", description, lastDatabaseTime.Format(time.RFC3339Nano), lastDeadline.Format(time.RFC3339Nano), pollCtx.Err())
			}
			t.Fatalf("inspect database deadline: description=%q database_time=%s deadline=%s error=%v", description, lastDatabaseTime.Format(time.RFC3339Nano), lastDeadline.Format(time.RFC3339Nano), err)
		}
		if !lastDatabaseTime.Before(lastDeadline) {
			return
		}
		select {
		case <-ticker.C:
		case <-pollCtx.Done():
			t.Fatalf("database deadline was not reached before the 3s bound: description=%q database_time=%s deadline=%s error=%v", description, lastDatabaseTime.Format(time.RFC3339Nano), lastDeadline.Format(time.RFC3339Nano), pollCtx.Err())
		}
	}
}

// WaitForErrorResult bounds completion after a controlled lock is released.
// Five seconds permits full-suite scheduling contention while surfacing a
// deadlock well before the package timeout.
func WaitForErrorResult(t *testing.T, result <-chan error, description string) error {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		t.Fatalf("asynchronous result was not observed within 5s: description=%q last_state=result pending", description)
	}
	return nil
}

func requirePing(t *testing.T, db *bun.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(fmt.Errorf("ping PostgreSQL: %w", err))
	}
}
