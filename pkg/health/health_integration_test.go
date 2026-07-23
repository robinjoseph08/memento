//go:build integration

package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWiresRealPostgreSQLMigrationAndSetupChecks(t *testing.T) {
	ok := checkerFunc(func(context.Context) error { return nil })

	t.Run("PostgreSQL", func(t *testing.T) {
		db := testdb.Open(t)
		require.NoError(t, migrations.Apply(context.Background(), db))
		service := New(db, ok, fakeWorker{healthy: true}, 50*time.Millisecond, time.Second)
		require.NoError(t, db.Close())

		response := request(t, service.Ready)

		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), `"postgresql":"unavailable"`)
	})

	t.Run("migrations", func(t *testing.T) {
		db := testdb.Open(t)
		require.NoError(t, migrations.Apply(context.Background(), db))
		_, err := db.ExecContext(context.Background(), `DELETE FROM bun_migrations`)
		require.NoError(t, err)
		service := New(db, ok, fakeWorker{healthy: true}, time.Second, time.Second)

		response := request(t, service.Ready)

		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), `"postgresql":"ok"`)
		assert.Contains(t, response.Body.String(), `"migrations":"unavailable"`)
		assert.Contains(t, response.Body.String(), `"setup":"ok"`)
	})

	t.Run("setup", func(t *testing.T) {
		db := testdb.Open(t)
		require.NoError(t, migrations.Apply(context.Background(), db))
		_, err := db.ExecContext(context.Background(), `DELETE FROM system_settings`)
		require.NoError(t, err)
		service := New(db, ok, fakeWorker{healthy: true}, time.Second, time.Second)

		response := request(t, service.Ready)

		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), `"migrations":"ok"`)
		assert.Contains(t, response.Body.String(), `"setup":"unavailable"`)
	})
}
