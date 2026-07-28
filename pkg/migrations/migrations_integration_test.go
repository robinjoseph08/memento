//go:build integration

package migrations

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun/driver/pgdriver"
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
	var jobKind, idempotencyKey string
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM system_settings`).Scan(ctx, &settingsCount))
	require.NoError(t, db.NewRaw(`SELECT count(*), max(kind), max(idempotency_key) FROM jobs`).Scan(ctx,
		&jobsCount, &jobKind, &idempotencyKey))
	assert.Equal(t, 1, settingsCount)
	assert.Equal(t, 1, jobsCount)
	assert.Equal(t, "cleanup_archive_plans", jobKind)
	assert.Equal(t, "archive-plans-cleanup", idempotencyKey)
}

func TestApplyWaitsForMigrationLockBeyondDriverReadTimeout(t *testing.T) {
	db := testdb.Open(t)
	holder, err := db.DB.Conn(context.Background())
	require.NoError(t, err)
	defer holder.Close()

	_, err = holder.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtextextended(current_database() || ':memento:migrations', 0))`)
	require.NoError(t, err)

	const driverReadTimeout = 50 * time.Millisecond
	waiterApplicationName := "memento-migration-lock-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	waiter := testdb.Clone(t, db,
		pgdriver.WithApplicationName(waiterApplicationName),
		pgdriver.WithReadTimeout(driverReadTimeout),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- Apply(ctx, waiter)
	}()

	var observedAttempt bool
	require.Eventually(t, func() bool {
		queryErr := db.NewRaw(`
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = ?
					AND query LIKE '%pg_try_advisory_lock%'
			)
		`, waiterApplicationName).Scan(context.Background(), &observedAttempt)
		return queryErr == nil && observedAttempt
	}, time.Second, 5*time.Millisecond, "migration waiter never attempted the advisory lock")
	select {
	case applyErr := <-result:
		require.Failf(t, "migration waiter returned while lock was held", "error: %v", applyErr)
	case <-time.After(4 * driverReadTimeout):
	}

	_, err = holder.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended(current_database() || ':memento:migrations', 0))`)
	require.NoError(t, err)
	require.NoError(t, <-result)
	require.NoError(t, Current(ctx, db))
}

func TestApplyStopsWaitingForMigrationLockWhenContextExpires(t *testing.T) {
	db := testdb.Open(t)
	holder, err := db.DB.Conn(context.Background())
	require.NoError(t, err)
	defer holder.Close()

	_, err = holder.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtextextended(current_database() || ':memento:migrations', 0))`)
	require.NoError(t, err)
	defer func() {
		_, unlockErr := holder.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended(current_database() || ':memento:migrations', 0))`)
		require.NoError(t, unlockErr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, Apply(ctx, db), context.DeadlineExceeded)
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

func TestSearchMigrationPreservesExistingPublishedDocumentsAndIndexes(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	foundSearch := false
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607280002" {
			foundSearch = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundSearch)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))

	_, err := db.ExecContext(ctx, `
		INSERT INTO people (id, display_name, sort_name) VALUES
			('11111111-1111-4111-8111-111111111111', 'Existing Recipient', 'existing recipient'),
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'Published Attendee', 'published attendee'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'Staged Attendee', 'staged attendee');
		INSERT INTO recipient_access_generations (
			id, person_id, generation, state, onboarding_completed_at
		) VALUES (
			'22222222-2222-4222-8222-222222222222',
			'11111111-1111-4111-8111-111111111111', 1, 'completed', now()
		);
		INSERT INTO events (id, lifecycle, title, grouping_timezone)
		VALUES ('33333333-3333-4333-8333-333333333333', 'published', 'Legacy Event', 'UTC');
		INSERT INTO media_items (
			id, immich_asset_id, media_type, local_date_time, first_seen_at, last_seen_at
		) VALUES (
			'44444444-4444-4444-8444-444444444444',
			'55555555-5555-4555-8555-555555555555', 'image',
			'2026-07-27T10:30:00', now(), now()
		);
		INSERT INTO publications (
			id, event_id, revision, editable_version, published_by_person_id,
			notify_recipients, committed_at
		) VALUES (
			'66666666-6666-4666-8666-666666666666',
			'33333333-3333-4333-8333-333333333333', 1, 1,
			'11111111-1111-4111-8111-111111111111', false, '2026-07-27T12:00:00Z'
		);
		UPDATE events SET current_publication_id = '66666666-6666-4666-8666-666666666666'
		WHERE id = '33333333-3333-4333-8333-333333333333';
		INSERT INTO audience_snapshots (
			id, target_kind, target_id, approved_by_person_id, approved_at, label
		) VALUES (
			'77777777-7777-4777-8777-777777777777', 'moment',
			'99999999-9999-4999-8999-999999999999',
			'11111111-1111-4111-8111-111111111111', '2026-07-27T11:00:00Z', 'Shared'
		);
		INSERT INTO published_moments (
			id, publication_id, draft_moment_id, audience_snapshot_id,
			position, title, proposed_day
		) VALUES (
			'88888888-8888-4888-8888-888888888888',
			'66666666-6666-4666-8666-666666666666',
			'99999999-9999-4999-8999-999999999999',
			'77777777-7777-4777-8777-777777777777', 0, '', '2026-07-27'
		);
		INSERT INTO attendance (
			moment_id, person_id, source, confirmed_by_person_id, confirmed_at
		) VALUES
			('99999999-9999-4999-8999-999999999999',
			 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'manual',
			 '11111111-1111-4111-8111-111111111111', '2026-07-27T10:00:00Z'),
			('99999999-9999-4999-8999-999999999999',
			 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'manual',
			 '11111111-1111-4111-8111-111111111111', '2026-07-27T13:00:00Z');
		INSERT INTO published_media_placements (
			published_moment_id, media_item_id, position, media_type,
			width, height, local_date_time
		) VALUES (
			'88888888-8888-4888-8888-888888888888',
			'44444444-4444-4444-8444-444444444444', 0, 'image',
			1200, 800, '2026-07-27T10:30:00'
		);
		INSERT INTO current_published_events (
			event_id, publication_id, title, description, grouping_timezone, committed_at
		) VALUES (
			'33333333-3333-4333-8333-333333333333',
			'66666666-6666-4666-8666-666666666666', 'Legacy Event', '', 'UTC', now()
		);
		INSERT INTO current_published_placements (
			event_id, publication_id, published_moment_id, media_item_id, position
		) VALUES (
			'33333333-3333-4333-8333-333333333333',
			'66666666-6666-4666-8666-666666666666',
			'88888888-8888-4888-8888-888888888888',
			'44444444-4444-4444-8444-444444444444', 0
		);
		INSERT INTO published_search_documents (
			event_id, publication_id, recipient_access_generation_id,
			media_item_id, search_text
		) VALUES (
			'33333333-3333-4333-8333-333333333333',
			'66666666-6666-4666-8666-666666666666',
			'22222222-2222-4222-8222-222222222222',
			'44444444-4444-4444-8444-444444444444', 'Café legacy'
		)
	`)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db))

	var captureDate, normalized string
	var vectorIndex, trigramIndex bool
	require.NoError(t, db.NewRaw(`
		SELECT capture_date::text, normalized_search_text,
		       to_regclass('published_search_documents_vector_idx') IS NOT NULL,
		       to_regclass('published_search_documents_trigram_idx') IS NOT NULL
		FROM published_search_documents
	`).Scan(ctx, &captureDate, &normalized, &vectorIndex, &trigramIndex))
	assert.Equal(t, "2026-07-27", captureDate)
	assert.Equal(t, "cafe legacy", normalized)
	assert.True(t, vectorIndex)
	assert.True(t, trigramIndex)

	var publishedAttendees []string
	require.NoError(t, db.NewRaw(`
		SELECT person_id::text FROM published_attendance ORDER BY person_id
	`).Scan(ctx, &publishedAttendees))
	assert.Equal(t, []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, publishedAttendees,
		"upgrade backfill keeps approved Attendance and excludes later Staged Attendance")

	_, err = db.ExecContext(ctx, `SET enable_seqscan = off`)
	require.NoError(t, err)
	var plan []string
	require.NoError(t, db.NewRaw(`
		EXPLAIN (COSTS OFF)
		SELECT event_id FROM published_search_documents
		WHERE 'legaci'::text OPERATOR(public.<<%) normalized_search_text
	`).Scan(ctx, &plan))
	assert.Contains(t, strings.Join(plan, "\n"), "published_search_documents_trigram_idx")

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err = migrator.Rollback(ctx)
	require.NoError(t, err)
	var searchText string
	require.NoError(t, db.NewRaw(`SELECT search_text FROM published_search_documents`).Scan(ctx, &searchText))
	assert.Equal(t, "Café legacy", searchText)
	require.NoError(t, db.NewRaw(`
		SELECT to_regclass('published_search_documents_vector_idx') IS NOT NULL,
		       to_regclass('published_search_documents_trigram_idx') IS NOT NULL
	`).Scan(ctx, &vectorIndex, &trigramIndex))
	assert.True(t, vectorIndex)
	assert.False(t, trigramIndex)
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

func TestVisibilityMigrationAppliesAfterIdentityRepairMigrationLedger(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	allMigrations := collection.Sorted()
	priorMigrations := migrate.NewMigrations()
	foundVisibility := false
	for _, migration := range allMigrations {
		if migration.Name == "202607270003" {
			foundVisibility = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundVisibility)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))

	var before int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'visibility_circles'`).Scan(ctx, &before))
	assert.Zero(t, before)
	require.NoError(t, Apply(ctx, db))
	require.NoError(t, Current(ctx, db))
	var after int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('visibility_circles', 'visibility_circle_members', 'interest_list_entries', 'interest_list_history')`).Scan(ctx, &after))
	assert.Equal(t, 4, after)
}

func TestVisibilityMigrationRollbackPreservesIdentityRepairSchema(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607270003" {
			break
		}
		priorMigrations.Add(migration)
	}
	require.NoError(t, applyCollection(ctx, db, priorMigrations))
	require.NoError(t, Apply(ctx, db))

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err := migrator.Rollback(ctx)
	require.NoError(t, err)
	var visibilityTables, repairTables int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('visibility_circles', 'visibility_circle_members', 'interest_list_entries', 'interest_list_history')`).Scan(ctx, &visibilityTables))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('media_backings', 'immich_people_inventory', 'immich_person_links', 'immich_face_anchors', 'person_repair_candidates', 'media_repair_candidates')`).Scan(ctx, &repairTables))
	assert.Zero(t, visibilityTables)
	assert.Equal(t, 6, repairTables)
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

func TestOnboardingMigrationPreservesLegacyAcknowledgmentsAndAddsResumableProgress(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	foundOnboarding := false
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607270005" {
			foundOnboarding = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundOnboarding)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))
	const personID = "11111111-1111-4111-8111-111111111111"
	const accessID = "22222222-2222-4222-8222-222222222222"
	_, err := db.ExecContext(ctx, `INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Existing Curator', 'existing curator'); INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at) VALUES (?, ?, 1, 'completed', now()); INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged, interest_list_acknowledged, email_preference, completed_at) VALUES (?, true, true, true, 'immediate', now())`, personID, accessID, personID, accessID)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db))
	var previews, push bool
	var version int
	require.NoError(t, db.NewRaw(`SELECT email_previews_acknowledged, push_guidance_acknowledged, informed_choices_version FROM onboarding_choices WHERE recipient_access_generation_id = ?`, accessID).Scan(ctx, &previews, &push, &version))
	assert.False(t, previews, "the migration must not fabricate a disclosure acknowledgment")
	assert.False(t, push, "the migration must not fabricate a disclosure acknowledgment")
	assert.Equal(t, 1, version)
	_, err = db.ExecContext(ctx, `INSERT INTO onboarding_progress (recipient_access_generation_id) VALUES (?)`, accessID)
	require.NoError(t, err)
	var sessionType string
	require.NoError(t, db.NewRaw(`SELECT session_type FROM onboarding_progress WHERE recipient_access_generation_id = ?`, accessID).Scan(ctx, &sessionType))
	assert.Empty(t, sessionType, "a Recipient must explicitly choose how to treat the browser")
	_, err = db.ExecContext(ctx, `UPDATE onboarding_progress SET email_preference = 'invalid' WHERE recipient_access_generation_id = ?`, accessID)
	require.Error(t, err, "resumable preferences must remain in the constrained domains")
}

func TestAttendanceAudienceMigrationResetsLegacyReviewFlagsAndBackfillsVersions(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	foundAudienceMigration := false
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607270007" {
			foundAudienceMigration = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundAudienceMigration)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))
	const eventID = "11111111-1111-4111-8111-111111111111"
	const momentID = "22222222-2222-4222-8222-222222222222"
	const mediaID = "33333333-3333-4333-8333-333333333333"
	const assetID = "44444444-4444-4444-8444-444444444444"
	const looseID = "55555555-5555-4555-8555-555555555555"
	_, err := db.ExecContext(ctx, `
		INSERT INTO events (id, title, grouping_timezone) VALUES (?, 'Existing Event', 'UTC');
		INSERT INTO draft_moments (id, event_id, position, proposed_day, grouping_timezone, attendance_complete, audience_complete) VALUES (?, ?, 0, '2026-01-01', 'UTC', true, true);
		INSERT INTO media_items (id, immich_asset_id, media_type, first_seen_at, last_seen_at) VALUES (?, ?, 'image', now(), now());
		INSERT INTO loose_items (id, media_item_id, grouping_timezone) VALUES (?, ?, 'UTC')
	`, eventID, momentID, eventID, mediaID, assetID, looseID, mediaID)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db))

	var attendanceComplete, audienceComplete bool
	var momentVersion, looseVersion int64
	require.NoError(t, db.NewRaw(`SELECT attendance_complete, audience_complete, review_version FROM draft_moments WHERE id = ?`, momentID).Scan(ctx, &attendanceComplete, &audienceComplete, &momentVersion))
	require.NoError(t, db.NewRaw(`SELECT review_version FROM loose_items WHERE id = ?`, looseID).Scan(ctx, &looseVersion))
	assert.False(t, attendanceComplete)
	assert.False(t, audienceComplete)
	assert.Equal(t, int64(1), momentVersion)
	assert.Equal(t, int64(1), looseVersion)

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err = migrator.Rollback(ctx)
	require.NoError(t, err)
	var eventRows, looseRows, audienceTables int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM events WHERE id = ?`, eventID).Scan(ctx, &eventRows))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM loose_items WHERE id = ?`, looseID).Scan(ctx, &looseRows))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('attendance', 'audience_proposals', 'audience_reasons', 'audience_overrides', 'audience_snapshots', 'audience_snapshot_entries', 'current_audience_snapshots', 'publication_audit_events')`).Scan(ctx, &audienceTables))
	assert.Equal(t, 1, eventRows)
	assert.Equal(t, 1, looseRows)
	assert.Zero(t, audienceTables)
}

func TestVisibilityInfrastructureEnforcesPrivacyStateConstraints(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))
	const first = "11111111-1111-4111-8111-111111111111"
	const second = "22222222-2222-4222-8222-222222222222"
	const circle = "33333333-3333-4333-8333-333333333333"
	_, err := db.ExecContext(ctx, `INSERT INTO people (id, display_name, sort_name) VALUES (?, 'First', 'First'), (?, 'Second', 'Second'); INSERT INTO visibility_circles (id, name) VALUES (?, 'Family')`, first, second, circle)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO visibility_circles (id, name) VALUES ('44444444-4444-4444-8444-444444444444', 'family')`)
	require.Error(t, err, "active circle names must be case-insensitively unique")
	_, err = db.ExecContext(ctx, `INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, 'active', now(), now())`, first, first)
	require.Error(t, err, "a Recipient must not select their own Person")
	_, err = db.ExecContext(ctx, `INSERT INTO interest_list_entries (recipient_person_id, selected_person_id, state, chosen_at, updated_at) VALUES (?, ?, 'unknown', now(), now())`, first, second)
	require.Error(t, err, "Interest state must use a constrained domain value")
	_, err = db.ExecContext(ctx, `INSERT INTO visibility_circle_members (circle_id, person_id) VALUES (?, ?)`, circle, second)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, second)
	require.Error(t, err, "Visibility references must restrict Person deletion")
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

func TestInvitationSuggestionInfrastructureEnforcesDecisionAndActivityState(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))
	const requester = "11111111-1111-4111-8111-111111111111"
	const access = "22222222-2222-4222-8222-222222222222"
	const session = "33333333-3333-4333-8333-333333333333"
	const suggestion = "44444444-4444-4444-8444-444444444444"
	_, err := db.ExecContext(ctx, `
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Requester', 'Requester');
		INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at) VALUES (?, ?, 1, 'completed', now());
		INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		SELECT ?, decode(repeat('11', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 hour' FROM system_settings;
		INSERT INTO invitation_suggestions (id, requester_person_id, requester_access_generation_id, requester_session_id, name, email, normalized_email, relationship_context, spoke_with_person)
		VALUES (?, ?, ?, ?, 'Relative', 'relative@example.com', 'relative@example.com', 'Cousin', false)
	`, requester, access, requester, session, requester, access, suggestion, requester, access, session)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE invitation_suggestions SET status = 'accepted', resolved_at = now(), resolved_by_person_id = ? WHERE id = ?`, requester, suggestion)
	require.Error(t, err, "accepted suggestions require an explicit matched Person")
	_, err = db.ExecContext(ctx, `UPDATE invitation_suggestions SET withdrawn_at = now(), status = 'rejected', resolved_at = now(), resolved_by_person_id = ? WHERE id = ?`, requester, suggestion)
	require.Error(t, err, "withdrawal and Curator resolution cannot coexist")
	_, err = db.ExecContext(ctx, `INSERT INTO recipient_activity_items (recipient_person_id, actor_person_id, invitation_suggestion_id, action) VALUES (?, ?, ?, 'unknown')`, requester, requester, suggestion)
	require.Error(t, err, "activity actions use a constrained durable domain")
}
