//go:build integration

package migrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
