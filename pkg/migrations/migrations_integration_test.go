//go:build integration

package migrations

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyFromEmptyDatabaseUnderConcurrentLock(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- Apply(ctx, db)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, Current(ctx, db))
	require.NoError(t, Extensions(ctx, db))
	require.NoError(t, SetupConsistent(ctx, db))

	var settingsCount, jobsCount int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM system_settings`).Scan(ctx, &settingsCount))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM jobs`).Scan(ctx, &jobsCount))
	assert.Equal(t, 1, settingsCount)
	assert.Zero(t, jobsCount)
}

func TestEmailDeliveryInfrastructureEnforcesDurableState(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))

	var tables int
	require.NoError(t, db.NewRaw(`
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name IN ('email_deliveries', 'delivery_problems', 'outbox_events')
	`).Scan(ctx, &tables))
	assert.Equal(t, 3, tables)

	_, err := db.ExecContext(ctx, `
		INSERT INTO outbox_events (kind, aggregate_kind, aggregate_id, aggregate_version)
		VALUES ('send_required_email', 'email_delivery', '1', 1),
		       ('send_required_email', 'email_delivery', '1', 1)
	`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "outbox_events_aggregate_kind_aggregate_id_aggregate_version")
}

func TestSetupInfrastructureEnforcesSingletonCuratorAndSecurityEpoch(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))

	var epochLength int
	require.NoError(t, db.NewRaw(`SELECT octet_length(security_epoch) FROM system_settings WHERE id = 1`).Scan(ctx, &epochLength))
	assert.Equal(t, 32, epochLength)

	_, err := db.ExecContext(ctx, `
		INSERT INTO people (id, display_name, sort_name)
		VALUES ('00000000-0000-0000-0000-000000000001', 'First', 'first'),
		       ('00000000-0000-0000-0000-000000000002', 'Second', 'second');
		INSERT INTO person_roles (person_id, role)
		VALUES ('00000000-0000-0000-0000-000000000001', 'curator');
		INSERT INTO person_roles (person_id, role)
		VALUES ('00000000-0000-0000-0000-000000000002', 'curator');
	`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "person_roles_sole_curator_idx")
}

func TestJobsRejectRunningStateWithoutAReclaimableLease(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))

	_, err := db.ExecContext(ctx, `INSERT INTO jobs (kind, status) VALUES ('test', 'running')`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "jobs_check")
}

func TestCurrentDetectsUnappliedMigration(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, Apply(ctx, db))
	require.NoError(t, Current(ctx, db))
	_, err := db.ExecContext(ctx, `DELETE FROM bun_migrations`)
	require.NoError(t, err)
	assert.EqualError(t, Current(ctx, db), "database has unapplied migrations")
}

func TestSetupConsistentRejectsMissingOrMismatchedState(t *testing.T) {
	t.Run("missing singleton", func(t *testing.T) {
		db := testdb.Open(t)
		ctx := context.Background()
		require.NoError(t, Apply(ctx, db))
		_, err := db.ExecContext(ctx, `DELETE FROM system_settings`)
		require.NoError(t, err)
		assert.EqualError(t, SetupConsistent(ctx, db), "system settings singleton is inconsistent")
	})

	t.Run("complete without Curator", func(t *testing.T) {
		db := testdb.Open(t)
		ctx := context.Background()
		require.NoError(t, Apply(ctx, db))
		_, err := db.ExecContext(ctx, `UPDATE system_settings SET setup_complete = true WHERE id = 1`)
		require.NoError(t, err)
		assert.EqualError(t, SetupConsistent(ctx, db), "system settings singleton is inconsistent")
	})
}
