//go:build integration

package activity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCuratorWorkReadsSafeUnresolvedImmediateDeliveryProblems(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	personID, accessID, batchPublicID := uuid.New(), uuid.New(), uuid.New()
	_, err := db.NewRaw(`
		INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Private Recipient', 'recipient');
		INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current)
		VALUES (?, ?, 1, 'completed', true);
		INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current)
		VALUES (gen_random_uuid(), ?, 'private-recipient@example.com', 'private-recipient@example.com', true);
		INSERT INTO notification_batches
			(public_id, recipient_access_generation_id, channel, window_started_at, closes_at, status)
		VALUES (?, ?, 'email', clock_timestamp() - interval '20 minutes', clock_timestamp() - interval '5 minutes', 'failed')
		RETURNING id
	`, personID, accessID, personID, accessID, accessID, batchPublicID, accessID).Exec(ctx)
	require.NoError(t, err)
	var batchID int64
	require.NoError(t, db.NewRaw(`SELECT id FROM notification_batches WHERE public_id = ?`, batchPublicID).Scan(ctx, &batchID))
	_, err = db.NewRaw(`INSERT INTO delivery_problems (notification_batch_id, diagnostic, created_at)
		VALUES (?, 'recipient_rejected', ?)`, batchID, time.Now().UTC()).Exec(ctx)
	require.NoError(t, err)

	response, err := New(db).ListCuratorWork(ctx)
	require.NoError(t, err)
	require.Len(t, response.Items, 1)
	assert.Equal(t, CuratorWorkItem{
		ID: response.Items[0].ID, Kind: "delivery_problem", SourceKind: "notification_batch",
		SourceID: batchPublicID.String(), Diagnostic: "recipient_rejected", CreatedAt: response.Items[0].CreatedAt,
	}, response.Items[0])
	assert.NotEmpty(t, response.Items[0].ID)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-recipient@example.com")
	assert.NotContains(t, string(encoded), "Private Recipient")
}
