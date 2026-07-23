package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkerFunc func(context.Context) error

func (f checkerFunc) Check(ctx context.Context) error { return f(ctx) }

type fakeWorker struct{ healthy bool }

func (w fakeWorker) Healthy(time.Duration) bool { return w.healthy }

func request(t *testing.T, handler echo.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil), recorder)
	require.NoError(t, handler(ctx))
	return recorder
}

func TestLiveDoesNotCallDependencies(t *testing.T) {
	var calls atomic.Int32
	failIfCalled := func(context.Context) error {
		calls.Add(1)
		return errors.New("should not be called")
	}
	service := newWithChecks(failIfCalled, failIfCalled, failIfCalled, checkerFunc(failIfCalled), fakeWorker{}, time.Second, time.Second)

	response := request(t, service.Live)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"status":"live"}`, response.Body.String())
	assert.Zero(t, calls.Load())
}

func TestReadyReportsAllowlistedHealthyChecks(t *testing.T) {
	ok := func(context.Context) error { return nil }
	service := newWithChecks(ok, ok, ok, checkerFunc(ok), fakeWorker{healthy: true}, time.Second, time.Second)

	response := request(t, service.Ready)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{
		"status":"ready",
		"checks":{"postgresql":"ok","migrations":"ok","setup":"ok","worker":"ok","immich":"ok"}
	}`, response.Body.String())
}

func TestReadyReportsEachUnsafeDependencySymmetrically(t *testing.T) {
	dependencyError := errors.New("postgresql://secret@private/recipient-data")
	tests := []struct {
		name      string
		postgres  error
		migration error
		setup     error
		worker    bool
		immich    error
		failedKey string
	}{
		{"PostgreSQL", dependencyError, nil, nil, true, nil, "postgresql"},
		{"migrations", nil, dependencyError, nil, true, nil, "migrations"},
		{"setup", nil, nil, dependencyError, true, nil, "setup"},
		{"worker", nil, nil, nil, false, nil, "worker"},
		{"Immich", nil, nil, nil, true, dependencyError, "immich"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newWithChecks(
				func(context.Context) error { return test.postgres },
				func(context.Context) error { return test.migration },
				func(context.Context) error { return test.setup },
				checkerFunc(func(context.Context) error { return test.immich }),
				fakeWorker{healthy: test.worker}, time.Second, time.Second,
			)
			response := request(t, service.Ready)
			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			assert.Contains(t, response.Body.String(), `"`+test.failedKey+`":"unavailable"`)
			assert.NotContains(t, response.Body.String(), "secret")
			assert.NotContains(t, response.Body.String(), "private")
			assert.NotContains(t, response.Body.String(), "recipient")
		})
	}
}

func TestDrainingDropsReadinessBeforeCallingDependencies(t *testing.T) {
	var calls atomic.Int32
	called := func(context.Context) error {
		calls.Add(1)
		return nil
	}
	service := newWithChecks(called, called, called, checkerFunc(called), fakeWorker{healthy: true}, time.Second, time.Second)
	service.SetDraining()

	response := request(t, service.Ready)

	assert.True(t, service.IsDraining())
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Zero(t, calls.Load())
}

func TestReadyBoundsPostgreSQLCheck(t *testing.T) {
	service := newWithChecks(
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		func(context.Context) error { return nil },
		func(context.Context) error { return nil },
		checkerFunc(func(context.Context) error { return nil }),
		fakeWorker{healthy: true}, time.Millisecond, time.Second,
	)
	started := time.Now()
	response := request(t, service.Ready)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Less(t, time.Since(started), 100*time.Millisecond)
}
