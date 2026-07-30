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

func TestCuratorActivityAttributesEveryRoutineCategoryWithoutPrivatePayloads(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	curatorID, recipientID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mediaID, eventID, publicationID, commentID, suggestionID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true;
		INSERT INTO people (id, display_name, sort_name) VALUES
			(?, 'Robin Curator', 'curator'), (?, 'Alex Recipient', 'recipient');
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient');
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at)
		VALUES (?, ?, 1, 'completed', true, ?);
		INSERT INTO sessions
			(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			 session_type, created_at, last_activity_at, idle_expires_at)
		SELECT ?, digest(?::text, 'sha256'), ?, ?, security_epoch, 'trusted', ?, ?, ?
		FROM system_settings WHERE id = 1;
		INSERT INTO media_items
			(id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at)
		VALUES (?, ?, 'image', 100, 100, '2026-07-30T12:00:00Z', ?, ?);
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, created_at, updated_at)
		VALUES (?, 'draft', 'Family Event', '', 'UTC', ?, ?);
		INSERT INTO publications
			(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, true, ?);
		INSERT INTO publication_curator_activity_items (publication_id, actor_person_id, created_at)
		VALUES (?, ?, ?);
		INSERT INTO publication_audit_events
			(event_id, target_kind, target_id, actor_person_id, action, metadata, created_at)
		VALUES (?, 'event', ?, ?, 'content_withdrawn', '{"reason":"private reason"}'::jsonb, ?);
		INSERT INTO comments
			(id, media_item_id, author_person_id, author_access_generation_id, idempotency_key, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'PRIVATE COMMENT BODY', ?, ?);
		INSERT INTO interaction_activity_items
			(kind, recipient_access_generation_id, actor_person_id, media_item_id, comment_id, action, created_at)
		VALUES ('comment', ?, ?, ?, ?, 'comment_created', ?);
		INSERT INTO interaction_activity_items
			(kind, actor_person_id, media_item_id, favorite_recipient_person_id, action, created_at)
		VALUES ('favorite', ?, ?, ?, 'favorite_added', ?);
		INSERT INTO invitation_suggestions
			(id, requester_person_id, requester_access_generation_id, requester_session_id, name, email,
			 normalized_email, relationship_context, spoke_with_person, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'Private Person', 'secret@example.com', 'secret@example.com',
			 'PRIVATE RELATIONSHIP NOTE', true, ?, ?);
		INSERT INTO curator_activity_items
			(actor_person_id, invitation_suggestion_id, action, created_at)
		VALUES (?, ?, 'invitation_suggestion_submitted', ?);
		INSERT INTO security_audit_events
			(actor_person_id, subject_person_id, action, outcome, session_id, metadata, created_at)
		VALUES (?, ?, 'session_created', 'success', ?, '{"token":"PRIVATE TOKEN"}'::jsonb, ?),
		       (?, ?, 'recipient_suspended', 'success', ?, '{"email":"secret@example.com"}'::jsonb, ?);
		INSERT INTO email_deliveries
			(public_id, kind, recipient, subject, body, status, created_at, updated_at)
		VALUES ('activity-delivery', 'required_test', 'secret@example.com', 'Private subject',
			 'PRIVATE EMAIL BODY', 'failed', ?, ?);
	`, curatorID, recipientID, curatorID, recipientID,
		accessID, recipientID, now, sessionID, sessionID.String(), recipientID, accessID, now, now, now.Add(time.Hour),
		mediaID, uuid.New(), now, now, eventID, now, now,
		publicationID, eventID, curatorID, now.Add(-8*time.Minute), publicationID, curatorID, now.Add(-8*time.Minute),
		eventID, eventID, curatorID, now.Add(-7*time.Minute),
		commentID, mediaID, recipientID, accessID, uuid.New(), now.Add(-6*time.Minute), now.Add(-6*time.Minute),
		accessID, recipientID, mediaID, commentID, now.Add(-6*time.Minute),
		recipientID, mediaID, recipientID, now.Add(-5*time.Minute),
		suggestionID, recipientID, accessID, sessionID, now.Add(-4*time.Minute), now.Add(-4*time.Minute),
		recipientID, suggestionID, now.Add(-4*time.Minute),
		recipientID, recipientID, sessionID, now.Add(-3*time.Minute),
		curatorID, recipientID, sessionID, now.Add(-2*time.Minute),
		now.Add(-time.Minute), now.Add(-time.Minute)).Exec(ctx)
	require.NoError(t, err)
	var deliveryID int64
	require.NoError(t, db.NewRaw(`SELECT id FROM email_deliveries WHERE public_id = 'activity-delivery'`).Scan(ctx, &deliveryID))
	_, err = db.NewRaw(`INSERT INTO delivery_problems (email_delivery_id, diagnostic, created_at)
		VALUES (?, 'provider said secret@example.com PRIVATE', ?)`, deliveryID, now).Exec(ctx)
	require.NoError(t, err)

	response, err := New(db).ListCuratorActivity(ctx, PageRequest{Limit: 50})
	require.NoError(t, err)
	categories := map[string]bool{}
	for _, item := range response.Items {
		categories[item.Category] = true
	}
	for _, category := range []string{"security", "access", "publication", "withdrawal", "comment", "favorite", "invitation_suggestion", "delivery"} {
		assert.True(t, categories[category], category)
	}
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	for _, private := range []string{"PRIVATE COMMENT BODY", "PRIVATE RELATIONSHIP NOTE", "secret@example.com", "PRIVATE TOKEN", "PRIVATE EMAIL BODY", "provider said"} {
		assert.NotContains(t, string(encoded), private)
	}
	comment := findActivityCategory(response.Items, "comment")
	require.NotNil(t, comment.Actor)
	assert.Equal(t, recipientID.String(), comment.Actor.PersonID)
	assert.Equal(t, mediaID.String(), *comment.TargetID)
	require.NoError(t, New(db).MarkRead(ctx, curatorID, MarkReadRequest{
		Surface: "activity", SourceKind: comment.SourceKind, SourceID: comment.SourceID, Version: comment.Version,
	}))
	commentPage, err := New(db).ListCuratorActivity(ctx, PageRequest{Category: "comment", Limit: 10})
	require.NoError(t, err)
	require.Len(t, commentPage.Items, 1)
	assert.True(t, commentPage.Items[0].Read)
	unreadComments, err := New(db).ListCuratorActivity(ctx, PageRequest{Category: "comment", Unread: true, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, unreadComments.Items)

	filtered, err := New(db).ListCuratorActivity(ctx, PageRequest{Category: "withdrawal", Limit: 10})
	require.NoError(t, err)
	require.Len(t, filtered.Items, 1)
	assert.Equal(t, "content_withdrawn", filtered.Items[0].Action)
}

func findActivityCategory(items []CuratorActivityItem, category string) CuratorActivityItem {
	for _, item := range items {
		if item.Category == category {
			return item
		}
	}
	return CuratorActivityItem{}
}
