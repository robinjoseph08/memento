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
}
