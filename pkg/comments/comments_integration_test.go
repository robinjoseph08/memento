//go:build integration

package comments

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/activity"
	"github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type interactionFixture struct {
	db          *bun.DB
	comments    *Service
	favorites   *favorites.Service
	actors      map[string]setup.SessionActor
	people      map[string]uuid.UUID
	access      map[string]uuid.UUID
	media       uuid.UUID
	event       uuid.UUID
	publication uuid.UUID
	moment      uuid.UUID
}

func newInteractionFixture(t *testing.T) interactionFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	interactionActivity := activity.New(db)
	commentService := New(db)
	commentService.SetHandoff(interactionActivity.RecordComment)
	fixture := interactionFixture{
		db: db, comments: commentService, favorites: favorites.New(db, interactionActivity), actors: map[string]setup.SessionActor{},
		people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{}, media: uuid.New(),
		event: uuid.New(), publication: uuid.New(), moment: uuid.New(),
	}
	now := time.Now().UTC().Truncate(time.Second)
	for index, name := range []string{"curator", "alex", "blair", "casey"} {
		personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New()
		fixture.people[name], fixture.access[name] = personID, accessID
		fixture.actors[name] = setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID, Curator: name == "curator"}
		_, err := db.NewRaw(`
			INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?);
			INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
			INSERT INTO recipient_access_generations
				(id, person_id, generation, state, is_current, onboarding_completed_at)
			VALUES (?, ?, 1, 'completed', true, ?);
			INSERT INTO sessions
				(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
			SELECT ?, decode(repeat(?, 32), 'hex'), ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 hour'
			FROM system_settings WHERE id = 1
		`, personID, titleName(name), name, personID, accessID, personID, now,
			sessionID, []string{"11", "22", "33", "44"}[index], personID, accessID, now).Exec(ctx)
		require.NoError(t, err)
	}
	snapshot := uuid.New()
	_, err := db.NewRaw(`
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator');
		UPDATE system_settings SET setup_complete = true WHERE id = 1;
		INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, created_at, updated_at)
		VALUES (?, 'published', 'Shared Event', '', 'UTC', 1, ?, ?);
		INSERT INTO publications
			(id, event_id, revision, editable_version, published_by_person_id, notify_recipients, committed_at)
		VALUES (?, ?, 1, 1, ?, true, ?);
		UPDATE events SET current_publication_id = ? WHERE id = ?;
		INSERT INTO current_published_events
			(event_id, publication_id, title, description, grouping_timezone, committed_at)
		VALUES (?, ?, 'Shared Event', '', 'UTC', ?);
		INSERT INTO audience_snapshots
			(id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO media_items
			(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
		VALUES (?, gen_random_uuid(), 'image', 1200, 800, '2026-07-28T12:00:00Z', 'current', ?, ?)
	`, fixture.people["curator"], fixture.event, now, now,
		fixture.publication, fixture.event, fixture.people["curator"], now,
		fixture.publication, fixture.event, fixture.event, fixture.publication, now,
		snapshot, fixture.moment, fixture.people["curator"], now, fixture.media, now, now).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-28');
		INSERT INTO published_media_placements
			(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		VALUES (?, ?, 0, 'image', 1200, 800, '2026-07-28T12:00:00Z');
		INSERT INTO current_published_placements
			(event_id, publication_id, published_moment_id, media_item_id, position)
		VALUES (?, ?, ?, ?, 0)
	`, fixture.moment, fixture.publication, fixture.moment, snapshot,
		fixture.moment, fixture.media, fixture.event, fixture.publication, fixture.moment, fixture.media).Exec(ctx)
	require.NoError(t, err)
	fixture.grant(t, "alex")
	fixture.grant(t, "blair")
	return fixture
}

func titleName(name string) string {
	return map[string]string{"curator": "Curator", "alex": "Alex", "blair": "Blair", "casey": "Casey"}[name]
}

func (fixture interactionFixture) grant(t *testing.T, name string) {
	t.Helper()
	_, err := fixture.db.NewRaw(`INSERT INTO current_audience_entitlements
		(event_id, publication_id, recipient_person_id, recipient_access_generation_id, media_item_id)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, fixture.event, fixture.publication,
		fixture.people[name], fixture.access[name], fixture.media).Exec(context.Background())
	require.NoError(t, err)
}

func (fixture interactionFixture) removeGrant(t *testing.T, name string) {
	t.Helper()
	_, err := fixture.db.NewRaw(`DELETE FROM current_audience_entitlements
		WHERE event_id = ? AND recipient_access_generation_id = ? AND media_item_id = ?`,
		fixture.event, fixture.access[name], fixture.media).Exec(context.Background())
	require.NoError(t, err)
}

func TestCommentsAuthorizeChronologyOwnershipAndModerationHistory(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	step := 0
	fixture.comments.now = func() time.Time {
		step++
		return base.Add(time.Duration(step) * time.Second)
	}

	err := fixture.comments.SetMuted(ctx, fixture.actors["alex"], fixture.media, true)
	require.ErrorIs(t, err, ErrInvalidMute, "access alone does not create a Comment subscription")
	alexComment, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, BodyRequest{Body: " First "})
	require.NoError(t, err)
	assert.True(t, alexComment.AuthoredByMe)
	blairComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "Second"})
	require.NoError(t, err)

	listed, err := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	require.Len(t, listed.Comments, 2)
	assert.Equal(t, []string{"First", "Second"}, []string{listed.Comments[0].Body, listed.Comments[1].Body})
	assert.True(t, listed.Comments[0].CanEdit)
	assert.False(t, listed.Comments[1].CanEdit)

	_, err = fixture.comments.Edit(ctx, fixture.actors["blair"], uuid.MustParse(alexComment.ID), alexComment.Version, BodyRequest{Body: "stolen"})
	require.ErrorIs(t, err, ErrNotFound)
	edited, err := fixture.comments.Edit(ctx, fixture.actors["alex"], uuid.MustParse(alexComment.ID), alexComment.Version, BodyRequest{Body: "Edited"})
	require.NoError(t, err)
	assert.Equal(t, "Edited", edited.Body)
	require.NotNil(t, edited.EditedAt)

	require.NoError(t, fixture.comments.Moderate(ctx, fixture.actors["curator"], uuid.MustParse(blairComment.ID), blairComment.Version, ModerateRequest{Reason: "Family privacy"}))
	listed, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	assert.Equal(t, "moderated", listed.Comments[1].State)
	assert.Empty(t, listed.Comments[1].Body)
	require.NotNil(t, listed.Comments[1].ModeratorName)
	assert.Equal(t, "Curator", *listed.Comments[1].ModeratorName)

	_, err = fixture.comments.ModerationHistory(ctx, fixture.actors["alex"], uuid.MustParse(blairComment.ID))
	require.ErrorIs(t, err, ErrNotCurator)
	history, err := fixture.comments.ModerationHistory(ctx, fixture.actors["curator"], uuid.MustParse(blairComment.ID))
	require.NoError(t, err)
	require.Len(t, history.History, 1)
	assert.Equal(t, "Second", history.History[0].PriorBody)
	assert.Equal(t, "Family privacy", history.History[0].Reason)
	assert.Equal(t, "Curator", history.History[0].ActorName)

	require.NoError(t, fixture.comments.Delete(ctx, fixture.actors["alex"], uuid.MustParse(alexComment.ID), edited.Version))
	listed, err = fixture.comments.List(ctx, fixture.actors["blair"], fixture.media)
	require.NoError(t, err)
	assert.Equal(t, "deleted", listed.Comments[0].State)
	assert.Empty(t, listed.Comments[0].Body)

	_, err = fixture.comments.List(ctx, fixture.actors["casey"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.comments.Create(ctx, fixture.actors["casey"], fixture.media, BodyRequest{Body: "guessed"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestConcurrentAndStaleCommentMutationsCannotOverwriteNewerState(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	created, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, BodyRequest{Body: "Original"})
	require.NoError(t, err)
	commentID := uuid.MustParse(created.ID)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, body := range []string{"First concurrent edit", "Second concurrent edit"} {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			<-start
			_, editErr := fixture.comments.Edit(ctx, fixture.actors["alex"], commentID, created.Version, BodyRequest{Body: body})
			results <- editErr
		}(body)
	}
	close(start)
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent Edit result: %v", result)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)

	listed, err := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	require.Len(t, listed.Comments, 1)
	current := listed.Comments[0]
	assert.Equal(t, int64(2), current.Version)
	assert.Contains(t, []string{"First concurrent edit", "Second concurrent edit"}, current.Body)

	require.ErrorIs(t, fixture.comments.Delete(ctx, fixture.actors["alex"], commentID, created.Version), ErrVersionConflict)
	require.ErrorIs(t, fixture.comments.Moderate(ctx, fixture.actors["curator"], commentID, created.Version,
		ModerateRequest{Reason: "stale moderation"}), ErrVersionConflict)
	require.NoError(t, fixture.comments.Moderate(ctx, fixture.actors["curator"], commentID, current.Version,
		ModerateRequest{Reason: "current moderation"}))
}

func TestCommentSubscriptionsMuteSelfSuppressionAndNoBacklog(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	alexFirst, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, BodyRequest{Body: "Alex subscribes"})
	require.NoError(t, err)

	var activityCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, alexFirst.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount, "an author is never notified for their own Comment")

	blairFirst, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "Blair activity"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, blairFirst.ID, fixture.access["blair"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount, "self-suppression also applies when other subscribers exist")
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, blairFirst.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Equal(t, 1, activityCount)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, blairFirst.ID, fixture.access["curator"]).Scan(ctx, &activityCount))
	assert.Equal(t, 1, activityCount, "the Curator receives new Comment activity independently")

	var before int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items`).Scan(ctx, &before))
	edited, err := fixture.comments.Edit(ctx, fixture.actors["blair"], uuid.MustParse(blairFirst.ID), blairFirst.Version, BodyRequest{Body: "Edited quietly"})
	require.NoError(t, err)
	require.NoError(t, fixture.comments.Delete(ctx, fixture.actors["blair"], uuid.MustParse(blairFirst.ID), edited.Version))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items`).Scan(ctx, &activityCount))
	assert.Equal(t, before, activityCount, "edits and deletions create no activity")

	require.NoError(t, fixture.comments.SetMuted(ctx, fixture.actors["alex"], fixture.media, true))
	mutedComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "Muted activity"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, mutedComment.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount)

	require.NoError(t, fixture.comments.SetMuted(ctx, fixture.actors["alex"], fixture.media, false))
	fixture.removeGrant(t, "alex")
	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	lostAccessComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "While access is lost"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, lostAccessComment.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount)
	fixture.grant(t, "alex")
	listed, err := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	assert.Len(t, listed.Comments, 4, "Comments persist and become readable after fresh authorization")
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, lostAccessComment.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount, "reauthorization creates no Comment backlog")

	var outboxCount, activities int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE kind = ?`, CommentJobKind).Scan(ctx, &outboxCount))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items`).Scan(ctx, &activities))
	assert.Equal(t, activities, outboxCount)
}

func TestCommentHandoffReauthorizesAndWithdrawalAndRevocationDenyImmediately(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	_, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, BodyRequest{Body: "Subscribe"})
	require.NoError(t, err)
	notificationComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "Notify"})
	require.NoError(t, err)
	var curatorPayloadText string
	require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ? AND activity.recipient_access_generation_id = ?`,
		CommentJobKind, notificationComment.ID, fixture.access["curator"]).Scan(ctx, &curatorPayloadText))
	fixture.comments.SetHandoff(nil)
	require.ErrorIs(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(curatorPayloadText)}), ErrHandoffNotConfigured)

	handoffFailure := errors.New("handoff unavailable")
	fixture.comments.SetHandoff(func(context.Context, uuid.UUID, uuid.UUID) error { return handoffFailure })
	require.ErrorIs(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(curatorPayloadText)}), handoffFailure)
	var terminal bool
	require.NoError(t, fixture.db.NewRaw(`SELECT dispatched_at IS NOT NULL OR suppressed_at IS NOT NULL
		FROM comment_activity_items WHERE comment_id = ? AND recipient_access_generation_id = ?`,
		notificationComment.ID, fixture.access["curator"]).Scan(ctx, &terminal))
	assert.False(t, terminal, "a failed handoff remains retryable")

	var deliveredAccess, deliveredComment uuid.UUID
	activityService := activity.New(fixture.db)
	fixture.comments.SetHandoff(func(ctx context.Context, accessID, commentID uuid.UUID) error {
		deliveredAccess, deliveredComment = accessID, commentID
		return activityService.RecordComment(ctx, accessID, commentID)
	})
	require.NoError(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(curatorPayloadText)}))
	assert.Equal(t, fixture.access["curator"], deliveredAccess)
	assert.Equal(t, uuid.MustParse(notificationComment.ID), deliveredComment)
	var integrated int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interaction_activity_items
		WHERE kind = 'comment' AND comment_id = ? AND recipient_access_generation_id = ?`,
		notificationComment.ID, fixture.access["curator"]).Scan(ctx, &integrated))
	assert.Equal(t, 1, integrated, "the production handoff integrates Curator Comment activity")

	var payloadText string
	require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ? AND activity.recipient_access_generation_id = ?
		ORDER BY outbox.id DESC LIMIT 1`, CommentJobKind, notificationComment.ID, fixture.access["alex"]).Scan(ctx, &payloadText))
	require.NoError(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(payloadText)}))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interaction_activity_items
		WHERE kind = 'comment' AND comment_id = ? AND recipient_access_generation_id = ?`,
		notificationComment.ID, fixture.access["alex"]).Scan(ctx, &integrated))
	assert.Equal(t, 1, integrated, "a subscribed Recipient receives integrated Comment activity")

	withdrawnComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, BodyRequest{Body: "Suppress after Withdrawal"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ? AND activity.recipient_access_generation_id = ?`,
		CommentJobKind, withdrawnComment.ID, fixture.access["alex"]).Scan(ctx, &payloadText))
	payload := json.RawMessage(payloadText)

	_, err = fixture.db.NewRaw(`INSERT INTO content_withdrawals
		(id, target_kind, target_id, reason, withdrawn_by_person_id, withdrawn_at, content_revision)
		VALUES (gen_random_uuid(), 'media', ?, 'Privacy', ?, now(), 1)`, fixture.media, fixture.people["curator"]).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: payload}))
	var suppressed bool
	require.NoError(t, fixture.db.NewRaw(`SELECT suppressed_at IS NOT NULL FROM comment_activity_items
		WHERE recipient_access_generation_id = ? ORDER BY id DESC LIMIT 1`, fixture.access["alex"]).Scan(ctx, &suppressed))
	assert.True(t, suppressed, "delivery is reauthorized after Withdrawal")

	_, err = fixture.db.NewRaw(`UPDATE content_withdrawals SET restored_at = now(), restored_by_publication_id = ?, content_revision = 2 WHERE target_id = ?`, fixture.publication, fixture.media).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE recipient_access_generations SET state = 'revoked', is_current = false, ended_at = now() WHERE id = ?;
		UPDATE sessions SET revoked_at = now() WHERE id = ?`, fixture.access["alex"], fixture.actors["alex"].SessionID).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	var persisted int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments WHERE media_item_id = ?`, fixture.media).Scan(ctx, &persisted))
	assert.Equal(t, 3, persisted)
}

func TestFavoritesRemainPrivateAndPersistAcrossAccessLoss(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	state, err := fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, true)
	require.NoError(t, err)
	assert.True(t, state.Favorite)
	alex, err := fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	blair, err := fixture.favorites.Get(ctx, fixture.actors["blair"], fixture.media)
	require.NoError(t, err)
	assert.True(t, alex.Favorite)
	assert.False(t, blair.Favorite, "Favorite state is not shared across Recipients")

	_, err = fixture.favorites.CuratorList(ctx, fixture.actors["blair"], fixture.people["alex"])
	require.ErrorIs(t, err, favorites.ErrNotCurator)
	curatorView, err := fixture.favorites.CuratorList(ctx, fixture.actors["curator"], fixture.people["alex"])
	require.NoError(t, err)
	assert.Equal(t, []string{fixture.media.String()}, curatorView.MediaItemIDs)
	_, err = fixture.favorites.Get(ctx, fixture.actors["casey"], fixture.media)
	require.ErrorIs(t, err, favorites.ErrNotFound)

	fixture.removeGrant(t, "alex")
	_, err = fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, favorites.ErrNotFound)
	var retained bool
	require.NoError(t, fixture.db.NewRaw(`SELECT is_current FROM favorites WHERE recipient_person_id = ? AND media_item_id = ?`, fixture.people["alex"], fixture.media).Scan(ctx, &retained))
	assert.True(t, retained)
	fixture.grant(t, "alex")
	alex, err = fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	assert.True(t, alex.Favorite)
	state, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
	require.NoError(t, err)
	assert.False(t, state.Favorite)
	_, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
	require.NoError(t, err)
	var actions []string
	require.NoError(t, fixture.db.NewRaw(`SELECT action FROM interaction_activity_items
		WHERE kind = 'favorite' AND favorite_recipient_person_id = ? AND media_item_id = ? ORDER BY id`, fixture.people["alex"], fixture.media).Scan(ctx, &actions))
	assert.Equal(t, []string{"favorite_added", "favorite_removed"}, actions, "Favorite activity records only state changes in the shared interaction feed")
}
