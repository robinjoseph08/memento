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
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', true, clock_timestamp());
		INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current)
		VALUES (gen_random_uuid(), ?, 'private-recipient@example.com', 'private-recipient@example.com', true);
		INSERT INTO notification_batches
			(public_id, recipient_access_generation_id, channel, window_started_at, closes_at, status)
		VALUES (?, ?, 'email', statement_timestamp() - interval '20 minutes', statement_timestamp() - interval '5 minutes', 'failed')
		RETURNING id
	`, personID, accessID, personID, accessID, batchPublicID, accessID).Exec(ctx)
	require.NoError(t, err)
	var batchID int64
	require.NoError(t, db.NewRaw(`SELECT id FROM notification_batches WHERE public_id = ?`, batchPublicID).Scan(ctx, &batchID))
	_, err = db.NewRaw(`INSERT INTO delivery_problems (notification_batch_id, diagnostic, created_at)
		VALUES (?, 'recipient_rejected', ?)`, batchID, time.Now().UTC()).Exec(ctx)
	require.NoError(t, err)

	response, err := New(db).ListCuratorWork(ctx)
	require.NoError(t, err)
	require.Len(t, response.Items, 1)
	assert.Equal(t, "delivery_problem", response.Items[0].Kind)
	assert.Equal(t, "delivery_problem", response.Items[0].SourceKind)
	assert.Equal(t, response.Items[0].ID, response.Items[0].SourceID)
	assert.Equal(t, "recipient_rejected", response.Items[0].Summary)
	assert.Equal(t, 0, response.Items[0].Priority)
	assert.Equal(t, "review_delivery", response.Items[0].NextAction)
	assert.False(t, response.Items[0].Read)
	assert.NotEmpty(t, response.Items[0].ID)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-recipient@example.com")
	assert.NotContains(t, string(encoded), "Private Recipient")
}

func TestCuratorWorkPrioritizesProblemsAndVersionedReadState(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 15, 0, 0, 0, time.UTC)
	curatorID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Curator', 'curator');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator')`, curatorID, curatorID).Exec(ctx)
	require.NoError(t, err)

	privacySourceID, newSourceID, eventID := uuid.New(), uuid.New(), uuid.New()
	fingerprint := make([]byte, 32)
	_, err = db.NewRaw(`
		INSERT INTO source_albums
			(id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			 first_seen_at, last_seen_at, disposition, ignored_at, source_missing, missing_since,
			 source_fingerprint, next_reconciliation_at)
		VALUES (?, ?, 'Missing published source', 0, ?, ?, ?, ?, 'ignored', ?, true, ?, ?, ?),
		       (?, ?, 'New family album', 0, ?, ?, ?, ?, 'unreviewed', NULL, false, NULL, ?, ?);
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'draft', 'Draft Event', '', 'UTC', 3, ?, ?)
	`, privacySourceID, uuid.New(), now, now, now.Add(-4*time.Hour), now, now.Add(-4*time.Hour), now.Add(-time.Hour), fingerprint, now,
		newSourceID, uuid.New(), now, now, now.Add(-3*time.Hour), now, fingerprint, now,
		eventID, now.Add(-2*time.Hour), now.Add(-2*time.Hour)).Exec(ctx)
	require.NoError(t, err)

	service := New(db)
	response, err := service.ListCuratorWork(ctx)
	require.NoError(t, err)
	require.Len(t, response.Items, 3)
	assert.Equal(t, []string{"privacy_problem", "draft_event", "new_source_album"}, []string{
		response.Items[0].Kind, response.Items[1].Kind, response.Items[2].Kind,
	})
	assert.Equal(t, []int{10, 20, 30}, []int{
		response.Items[0].Priority, response.Items[1].Priority, response.Items[2].Priority,
	})
	assert.Equal(t, "organize_media", response.Items[1].NextAction)
	assert.Equal(t, "draft 3", response.Items[1].Version)

	require.NoError(t, service.MarkRead(ctx, curatorID, MarkReadRequest{
		Surface: "work", SourceKind: response.Items[1].SourceKind, SourceID: response.Items[1].SourceID,
		Version: response.Items[1].Version,
	}))
	response, err = service.ListCuratorWork(ctx)
	require.NoError(t, err)
	assert.True(t, response.Items[1].Read)

	_, err = db.NewRaw(`UPDATE events SET version = 4, updated_at = ? WHERE id = ?`, now.Add(time.Minute), eventID).Exec(ctx)
	require.NoError(t, err)
	response, err = service.ListCuratorWork(ctx)
	require.NoError(t, err)
	assert.False(t, response.Items[1].Read, "changed work becomes unread again")
	assert.ErrorIs(t, service.MarkRead(ctx, curatorID, MarkReadRequest{
		Surface: "work", SourceKind: "event", SourceID: eventID.String(), Version: "draft 3",
	}), ErrVersionConflict)
}
