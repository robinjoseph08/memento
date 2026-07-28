//go:build integration

package migrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun/migrate"
)

func TestWithdrawalMigrationRollbackPreservesEventAndMediaAuditHistory(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	priorMigrations := migrate.NewMigrations()
	foundWithdrawalMigration := false
	for _, migration := range collection.Sorted() {
		if migration.Name == "202607280001" {
			foundWithdrawalMigration = true
			break
		}
		priorMigrations.Add(migration)
	}
	require.True(t, foundWithdrawalMigration)
	require.NoError(t, applyCollection(ctx, db, priorMigrations))
	require.NoError(t, Apply(ctx, db))
	personID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator')`, personID).Exec(ctx)
	require.NoError(t, err)
	for _, kind := range []string{"event", "media"} {
		_, err = db.NewRaw(`INSERT INTO publication_audit_events (
			target_kind, target_id, actor_person_id, action
		) VALUES (?, gen_random_uuid(), ?, 'content_withdrawn')`, kind, personID).Exec(ctx)
		require.NoError(t, err)
	}

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	_, err = migrator.Rollback(ctx)
	require.NoError(t, err)
	var preserved int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE action = 'content_withdrawn'`).Scan(ctx, &preserved))
	assert.Equal(t, 2, preserved)
	var validated bool
	require.NoError(t, db.NewRaw(`
		SELECT convalidated FROM pg_constraint
		WHERE conrelid = 'publication_audit_events'::regclass
		  AND conname = 'publication_audit_events_target_kind_check'
	`).Scan(ctx, &validated))
	assert.False(t, validated, "the legacy constraint must tolerate preserved newer audit rows")
	_, err = db.NewRaw(`INSERT INTO publication_audit_events (
		target_kind, target_id, actor_person_id, action
	) VALUES ('event', gen_random_uuid(), ?, 'new_legacy_write')`, personID).Exec(ctx)
	assert.Error(t, err, "the downgraded schema must reject new target kinds unsupported by the older application")
}

func TestWithdrawalMigrationRequiresReasonsAndSupportsEveryPublishedTargetKindInAudit(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, db))
	personID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator')`, personID).Exec(ctx)
	require.NoError(t, err)

	for _, kind := range []string{"event", "moment", "media"} {
		_, err := db.NewRaw(`INSERT INTO publication_audit_events (
			target_kind, target_id, actor_person_id, action
		) VALUES (?, gen_random_uuid(), ?, 'content_withdrawn')`, kind, personID).Exec(ctx)
		require.NoError(t, err)
	}
	_, err = db.NewRaw(`INSERT INTO content_withdrawals (
		id, target_kind, target_id, reason, withdrawn_by_person_id, withdrawn_at
	) VALUES (gen_random_uuid(), 'event', gen_random_uuid(), '', ?, now())`, personID).Exec(ctx)
	assert.Error(t, err, "an attributable Withdrawal reason cannot be empty")

	eventID, momentID, mediaID := uuid.New(), uuid.New(), uuid.New()
	for _, target := range []struct {
		kind string
		id   uuid.UUID
	}{
		{kind: "event", id: eventID},
		{kind: "moment", id: momentID},
		{kind: "media", id: mediaID},
	} {
		_, err = db.NewRaw(`INSERT INTO content_withdrawals (
			id, target_kind, target_id, reason, withdrawn_by_person_id, withdrawn_at
		) VALUES (gen_random_uuid(), ?, ?, 'Privacy review', ?, now())`, target.kind, target.id, personID).Exec(ctx)
		require.NoError(t, err)
	}
	var withdrawn bool
	require.NoError(t, db.NewRaw(`SELECT content_is_withdrawn(?, ?, ?)`, eventID, uuid.New(), uuid.New()).Scan(ctx, &withdrawn))
	assert.True(t, withdrawn)
	require.NoError(t, db.NewRaw(`SELECT content_is_withdrawn(?, ?, ?)`, uuid.New(), momentID, uuid.New()).Scan(ctx, &withdrawn))
	assert.True(t, withdrawn)
	require.NoError(t, db.NewRaw(`SELECT content_is_withdrawn(?, ?, ?)`, uuid.New(), uuid.New(), mediaID).Scan(ctx, &withdrawn))
	assert.True(t, withdrawn)
	require.NoError(t, db.NewRaw(`SELECT content_is_withdrawn(?, ?, ?)`, uuid.New(), uuid.New(), uuid.New()).Scan(ctx, &withdrawn))
	assert.False(t, withdrawn)
}
