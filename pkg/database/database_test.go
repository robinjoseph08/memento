package database

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
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
	assert.Equal(t, "*stdlib.Driver", fmt.Sprintf("%T", db.Driver()))
	assert.Equal(t, 3, db.Stats().MaxOpenConnections)
}

func TestOpenRejectsMalformedDatabaseURL(t *testing.T) {
	t.Parallel()

	cfg := config.NewForTest()
	cfg.DatabaseURL = "postgres://user:secret@localhost:bad/db"
	db, err := open(cfg)
	require.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "parse database URL")
	assert.NotContains(t, err.Error(), "secret")
}

type stackTracer interface {
	StackTrace() pkgerrors.StackTrace
}

func TestNewCapturesConnectionFailureAtCallSite(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		serverDone <- connection.Close()
	}()

	cfg := config.NewForTest()
	cfg.DatabaseURL = fmt.Sprintf("postgres://test:test@%s/memento_test?sslmode=disable", listener.Addr())
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	db, err := New(ctx, cfg)

	assert.Nil(t, db)
	select {
	case serverErr := <-serverDone:
		require.NoError(t, serverErr)
	case <-ctx.Done():
		t.Fatal("test PostgreSQL listener did not accept the connection")
	}
	var tracer stackTracer
	require.ErrorAs(t, err, &tracer)
	stack := fmt.Sprintf("%+v", tracer.StackTrace())
	assert.Contains(t, stack, "database.New")
	assert.Contains(t, stack, "database.go")
}

func TestNewLeavesCancellationStackless(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.NewForTest()
	db, err := New(ctx, cfg)

	assert.Nil(t, db)
	require.ErrorIs(t, err, context.Canceled)
	var tracer stackTracer
	assert.NotErrorAs(t, err, &tracer)
}

func TestWithLogging(t *testing.T) {
	t.Parallel()

	ctx := WithLogging(context.Background())
	enabled, ok := ctx.Value(loggingContextKey).(bool)
	require.True(t, ok)
	assert.True(t, enabled)
}
