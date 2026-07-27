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
	"github.com/uptrace/bun/migrate"
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

func TestSourceReconciliationMigrationBackfillsExistingAlbums(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	allMigrations := collection.Sorted()
	require.Greater(t, len(allMigrations), 2)
	priorMigrations := migrate.NewMigrations()
	foundSourceReconciliation := false
	for _, migration := range allMigrations {
		if migration.Name == "202607260001" {
			foundSourceReconciliation = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundSourceReconciliation)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))

	const sourceAlbumID = "11111111-1111-4111-8111-111111111111"
	_, err := db.ExecContext(ctx, `
		INSERT INTO source_albums (
			id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at
		) VALUES (
			?, '22222222-2222-4222-8222-222222222222', 'Existing album', 0, now(), now(),
			now(), now(), decode(repeat('00', 32), 'hex'), now()
		)
	`, sourceAlbumID)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db))

	var jobs int
	var payloadSourceID string
	require.NoError(t, db.NewRaw(`
		SELECT count(*), max(payload->>'source_album_id')
		FROM jobs WHERE kind = 'reconcile_source_album'
		  AND idempotency_key = 'source-reconcile:' || ?
	`, sourceAlbumID).Scan(ctx, &jobs, &payloadSourceID))
	assert.Equal(t, 1, jobs)
	assert.Equal(t, sourceAlbumID, payloadSourceID)
}

func TestRecipientMigrationAppliesAfterExistingMigrationLedger(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	allMigrations := collection.Sorted()
	priorMigrations := migrate.NewMigrations()
	foundRecipients := false
	for _, migration := range allMigrations {
		if migration.Name == "202607260003" {
			foundRecipients = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundRecipients)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))

	var before int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'invitations'`).Scan(ctx, &before))
	assert.Zero(t, before)
	require.NoError(t, Apply(ctx, db))
	require.NoError(t, Current(ctx, db))
	var after int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'invitations'`).Scan(ctx, &after))
	assert.Equal(t, 1, after)
}

func TestDraftMigrationRollbackRestoresRequiredCaptureDates(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607270001" {
			break
		}
		priorMigrations.Add(migration)
	}
	require.NoError(t, applyCollection(ctx, db, priorMigrations))
	require.NoError(t, Apply(ctx, db))

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_items (
			id, immich_asset_id, media_type, local_date_time, first_seen_at, last_seen_at
		) VALUES (
			'11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222',
			'image', NULL, now(), now()
		)
	`)
	require.NoError(t, err)
	_, err = migrator.Rollback(ctx)
	require.ErrorContains(t, err, "contains null values", "rollback must fail instead of fabricating or deleting unknown capture dates")
	_, err = db.ExecContext(ctx, `DELETE FROM media_items WHERE local_date_time IS NULL`)
	require.NoError(t, err)
	_, err = migrator.Rollback(ctx)
	require.NoError(t, err)
	var nullable string
	require.NoError(t, db.NewRaw(`
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'media_items' AND column_name = 'local_date_time'
	`).Scan(ctx, &nullable))
	assert.Equal(t, "NO", nullable)
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
