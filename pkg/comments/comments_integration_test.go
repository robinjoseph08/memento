//go:build integration

package comments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/activity"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type interactionMediaSource struct{}

func (interactionMediaSource) Thumbnail(_ context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return interactionMedia("thumbnail"), nil
}

func (interactionMediaSource) Preview(_ context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return interactionMedia("preview"), nil
}

func (interactionMediaSource) Video(_ context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return interactionMedia("video"), nil
}

func (interactionMediaSource) Original(_ context.Context, _ uuid.UUID, _ immich.MediaRequest) (immich.MediaResponse, error) {
	return interactionMedia("original"), nil
}

func interactionMedia(body string) immich.MediaResponse {
	return immich.MediaResponse{Body: io.NopCloser(strings.NewReader(body)), StatusCode: http.StatusOK,
		ContentType: "image/webp", ContentLength: int64(len(body))}
}

type interactionFixture struct {
	db          *bun.DB
	comments    *Service
	favorites   *favorites.Service
	actors      map[string]setup.SessionActor
	credentials map[string]string
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
		credentials: map[string]string{}, people: map[string]uuid.UUID{}, access: map[string]uuid.UUID{}, media: uuid.New(),
		event: uuid.New(), publication: uuid.New(), moment: uuid.New(),
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range []string{"curator", "alex", "blair", "casey"} {
		personID, accessID, sessionID := uuid.New(), uuid.New(), uuid.New()
		rawCredential := sha256.Sum256([]byte("interaction-session-" + name))
		credentialHash := sha256.Sum256(rawCredential[:])
		fixture.people[name], fixture.access[name] = personID, accessID
		fixture.credentials[name] = hex.EncodeToString(rawCredential[:])
		fixture.actors[name] = setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID, Curator: name == "curator"}
		_, err := db.NewRaw(`
			INSERT INTO people (id, display_name, sort_name) VALUES (?, ?, ?);
			INSERT INTO person_roles (person_id, role) VALUES (?, 'recipient');
			INSERT INTO recipient_access_generations
				(id, person_id, generation, state, is_current, onboarding_completed_at)
			VALUES (?, ?, 1, 'completed', true, ?);
			INSERT INTO recipient_emails
				(id, recipient_access_generation_id, email, normalized_email)
			VALUES (gen_random_uuid(), ?, ?, ?);
			INSERT INTO sessions
				(id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
			SELECT ?, ?, ?, ?, security_epoch, 'trusted', ?::timestamptz + interval '1 hour'
			FROM system_settings WHERE id = 1
		`, personID, titleName(name), name, personID, accessID, personID, now,
			accessID, name+"@example.com", name+"@example.com",
			sessionID, credentialHash[:], personID, accessID, now).Exec(ctx)
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
		INSERT INTO published_event_revisions
			(publication_id, event_id, title, description, grouping_timezone, created_at)
		VALUES (?, ?, 'Shared Event', '', 'UTC', ?);
		INSERT INTO audience_snapshots
			(id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO media_items
			(id, immich_asset_id, media_type, width, height, local_date_time, availability, first_seen_at, last_seen_at)
		VALUES (?, gen_random_uuid(), 'image', 1200, 800, '2026-07-28T12:00:00Z', 'current', ?, ?);
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, capture_at, filename, linked_at)
		SELECT gen_random_uuid(), id, immich_asset_id, '2026-07-28T12:00:00Z', 'shared-photo.jpg', ?
		FROM media_items WHERE id = ?
	`, fixture.people["curator"], fixture.event, now, now,
		fixture.publication, fixture.event, fixture.people["curator"], now,
		fixture.publication, fixture.event, fixture.event, fixture.publication, now,
		fixture.publication, fixture.event, now,
		snapshot, fixture.moment, fixture.people["curator"], now, fixture.media, now, now,
		now, fixture.media).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`
		INSERT INTO draft_moments
			(id, event_id, position, proposed_day, grouping_timezone, source_days,
			 title, cover_media_item_id, attendance_complete, audience_complete)
		VALUES (?, ?, 0, '2026-07-28', 'UTC', ARRAY['2026-07-28'::date], '', ?, true, true);
		INSERT INTO draft_media_placements
			(event_id, media_item_id, draft_moment_id, position, created_at)
		VALUES (?, ?, ?, 0, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		INSERT INTO audience_snapshot_entries
			(snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?), (?, ?, ?);
		INSERT INTO published_moments
			(id, publication_id, draft_moment_id, audience_snapshot_id, position, title, proposed_day)
		VALUES (?, ?, ?, ?, 0, '', '2026-07-28');
		INSERT INTO published_media_placements
			(published_moment_id, media_item_id, position, media_type, width, height, local_date_time)
		VALUES (?, ?, 0, 'image', 1200, 800, '2026-07-28T12:00:00Z');
		INSERT INTO current_published_placements
			(event_id, publication_id, published_moment_id, media_item_id, position)
		VALUES (?, ?, ?, ?, 0)
	`, fixture.moment, fixture.event, fixture.media,
		fixture.event, fixture.media, fixture.moment, now, fixture.moment, snapshot,
		snapshot, fixture.people["alex"], fixture.access["alex"],
		snapshot, fixture.people["blair"], fixture.access["blair"],
		fixture.moment, fixture.publication, fixture.moment, snapshot,
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

func interactionHTTP(t *testing.T, fixture interactionFixture) (*echo.Echo, map[string]string) {
	t.Helper()
	authorizer := setup.New(fixture.db, nil, config.SecurityConfig{Secret: "interaction-route-integration-secret"})
	csrf := make(map[string]string, len(fixture.credentials))
	for name, credential := range fixture.credentials {
		session, err := authorizer.Session(context.Background(), credential)
		require.NoError(t, err)
		csrf[name] = session.CSRFToken
	}
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(fixture.comments, authorizer))
	favorites.RegisterRoutes(e, favorites.NewHandler(fixture.favorites, authorizer))
	library.RegisterRoutes(e, library.NewHandler(library.New(fixture.db, interactionMediaSource{}), authorizer))
	return e, csrf
}

func serveInteraction(t *testing.T, e *echo.Echo, method, path, credential, csrf, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if credential != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl), method+" "+path)
	return response
}

func serveInteractionMedia(t *testing.T, e *echo.Echo, path, credential string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: credential})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl), path)
	return response
}

func waitForBlockedQueries(t *testing.T, db *bun.DB, pattern string, minimum int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	lastWaiting := 0
	for {
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE ?`, pattern).Scan(context.Background(), &lastWaiting))
		if lastWaiting >= minimum {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("queries did not reach the controlled lock; pattern %q, wanted %d, last waiting count %d", pattern, minimum, lastWaiting)
		}
	}
}

func waitForInteractionAdvisoryLock(t *testing.T, db *bun.DB, mode string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	lastWaiting := 0
	for {
		require.NoError(t, db.NewRaw(`WITH expected_lock AS (
			SELECT hashtextextended(?, 0) AS key
		)
		SELECT count(*) FROM pg_locks, expected_lock
		WHERE locktype = 'advisory'
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND classid::bigint = ((expected_lock.key >> 32) & 4294967295::bigint)
		  AND objid::bigint = (expected_lock.key & 4294967295::bigint)
		  AND objsubid = 1 AND mode = ? AND NOT granted`, placementlock.Key, mode).Scan(context.Background(), &lastWaiting))
		if lastWaiting > 0 {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("transaction did not wait for the %s interaction placement lock; last waiting count %d", mode, lastWaiting)
		}
	}
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
	unsubscribed, err := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media, PageRequest{Limit: 1})
	require.NoError(t, err)
	assert.False(t, unsubscribed.CanMute)

	alexComment, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: " First "})
	require.NoError(t, err)
	assert.True(t, alexComment.AuthoredByMe)
	blairComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Second"})
	require.NoError(t, err)

	listed, err := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.NoError(t, err)
	require.Len(t, listed.Comments, 2)
	assert.True(t, listed.CanMute)
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
	_, err = fixture.comments.Create(ctx, fixture.actors["casey"], fixture.media, uuid.New(), BodyRequest{Body: "guessed"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestConcurrentAndStaleCommentMutationsCannotOverwriteNewerState(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	created, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Original"})
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

func TestImmediateEmailHandoffDoesNotAdvanceFirstPartyCommentActivity(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	var handedAccess, handedComment uuid.UUID
	fixture.comments.SetImmediateHandoff(func(_ context.Context, _ bun.Tx, accessID, commentID uuid.UUID) error {
		handedAccess, handedComment = accessID, commentID
		return nil
	})

	created, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Separate delivery timing"})
	require.NoError(t, err)
	assert.Equal(t, fixture.access["curator"], handedAccess)
	assert.Equal(t, uuid.MustParse(created.ID), handedComment)
	var preservesDelay bool
	require.NoError(t, fixture.db.NewRaw(`SELECT bool_and(available_at = created_at + interval '15 minutes')
		FROM outbox_events WHERE kind = ? AND aggregate_id IN (
			SELECT id::text FROM comment_activity_items WHERE comment_id = ?
		)`, CommentJobKind, created.ID).Scan(ctx, &preservesDelay))
	assert.True(t, preservesDelay, "first-party Comment activity keeps its established delay")
}

func TestImmediateEmailHandoffFailureRollsBackCommentActivity(t *testing.T) {
	fixture := newInteractionFixture(t)
	failure := errors.New("email batch unavailable")
	fixture.comments.SetImmediateHandoff(func(context.Context, bun.Tx, uuid.UUID, uuid.UUID) error { return failure })
	commentID := uuid.New()

	_, err := fixture.comments.Create(context.Background(), fixture.actors["alex"], fixture.media, commentID, BodyRequest{Body: "Rollback together"})
	require.ErrorIs(t, err, failure)
	var comments int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments WHERE id = ?`, commentID).Scan(context.Background(), &comments))
	assert.Zero(t, comments)
}

func TestCommentSubscriptionsMuteSelfSuppressionAndNoBacklog(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	alexFirst, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Alex subscribes"})
	require.NoError(t, err)

	var activityCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, alexFirst.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount, "an author is never notified for their own Comment")

	blairFirst, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Blair activity"})
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
	mutedComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Muted activity"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ? AND recipient_access_generation_id = ?`, mutedComment.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
	assert.Zero(t, activityCount)

	require.NoError(t, fixture.comments.SetMuted(ctx, fixture.actors["alex"], fixture.media, false))
	fixture.removeGrant(t, "alex")
	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	lostAccessComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "While access is lost"})
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
	_, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Subscribe"})
	require.NoError(t, err)
	notificationComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Notify"})
	require.NoError(t, err)
	var curatorPayloadText string
	require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ? AND activity.recipient_access_generation_id = ?`,
		CommentJobKind, notificationComment.ID, fixture.access["curator"]).Scan(ctx, &curatorPayloadText))
	fixture.comments.SetHandoff(nil)
	require.ErrorIs(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(curatorPayloadText)}), ErrHandoffNotConfigured)

	handoffFailure := errors.New("handoff unavailable")
	fixture.comments.SetHandoff(func(context.Context, bun.Tx, uuid.UUID, uuid.UUID) error { return handoffFailure })
	require.ErrorIs(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(curatorPayloadText)}), handoffFailure)
	var terminal bool
	require.NoError(t, fixture.db.NewRaw(`SELECT dispatched_at IS NOT NULL OR suppressed_at IS NOT NULL
		FROM comment_activity_items WHERE comment_id = ? AND recipient_access_generation_id = ?`,
		notificationComment.ID, fixture.access["curator"]).Scan(ctx, &terminal))
	assert.False(t, terminal, "a failed handoff remains retryable")

	var deliveredAccess, deliveredComment uuid.UUID
	activityService := activity.New(fixture.db)
	fixture.comments.SetHandoff(func(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
		deliveredAccess, deliveredComment = accessID, commentID
		return activityService.RecordComment(ctx, tx, accessID, commentID)
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

	withdrawnComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Suppress after Withdrawal"})
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
	revokedComment, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Suppress after Revocation"})
	require.NoError(t, err)
	require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ? AND activity.recipient_access_generation_id = ?`,
		CommentJobKind, revokedComment.ID, fixture.access["alex"]).Scan(ctx, &payloadText))

	handoffCalls := 0
	fixture.comments.SetHandoff(func(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
		handoffCalls++
		return activityService.RecordComment(ctx, tx, accessID, commentID)
	})
	_, err = recipients.New(fixture.db, nil, "", nil).RevokeAccess(ctx,
		setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
		fixture.people["alex"], fixture.access["alex"])
	require.NoError(t, err)
	require.NoError(t, fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(payloadText)}))
	assert.Zero(t, handoffCalls, "Revocation suppresses queued Comment activity before handoff")
	var dispatched, revokedSuppressed bool
	require.NoError(t, fixture.db.NewRaw(`SELECT dispatched_at IS NOT NULL, suppressed_at IS NOT NULL
		FROM comment_activity_items WHERE comment_id = ? AND recipient_access_generation_id = ?`,
		revokedComment.ID, fixture.access["alex"]).Scan(ctx, &dispatched, &revokedSuppressed))
	assert.False(t, dispatched)
	assert.True(t, revokedSuppressed)
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interaction_activity_items
		WHERE kind = 'comment' AND comment_id = ? AND recipient_access_generation_id = ?`,
		revokedComment.ID, fixture.access["alex"]).Scan(ctx, &integrated))
	assert.Zero(t, integrated, "Revocation creates no handed-off activity item")

	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media)
	require.ErrorIs(t, err, ErrNotFound)
	var persisted int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments WHERE media_item_id = ?`, fixture.media).Scan(ctx, &persisted))
	assert.Equal(t, 4, persisted)
}

func TestCommentListsPaginateDeterministicallyAndCreationIsIdempotent(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	fixture.comments.now = func() time.Time { return createdAt }

	key := uuid.New()
	first, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, key, BodyRequest{Body: "Retry safely"})
	require.NoError(t, err)
	retried, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, key, BodyRequest{Body: "Retry safely"})
	require.NoError(t, err)
	assert.Equal(t, first.ID, retried.ID)
	_, err = fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, key, BodyRequest{Body: "Different request"})
	require.ErrorIs(t, err, ErrIdempotencyConflict)

	for _, body := range []string{"Second", "Third"} {
		_, err = fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: body})
		require.NoError(t, err)
	}
	var commentCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments WHERE media_item_id = ?`, fixture.media).Scan(ctx, &commentCount))
	assert.Equal(t, 3, commentCount, "a replay creates no duplicate Comment")

	var threadIDs []string
	cursor := ""
	for {
		page, pageErr := fixture.comments.List(ctx, fixture.actors["alex"], fixture.media, PageRequest{Cursor: cursor, Limit: 1})
		require.NoError(t, pageErr)
		require.Len(t, page.Comments, 1)
		threadIDs = append(threadIDs, page.Comments[0].ID)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	expectedThreadIDs := append([]string(nil), threadIDs...)
	sort.Strings(expectedThreadIDs)
	assert.Equal(t, expectedThreadIDs, threadIDs)
	assert.Len(t, threadIDs, 3)
	_, err = fixture.comments.List(ctx, fixture.actors["alex"], fixture.media, PageRequest{Cursor: "not-a-cursor", Limit: 1})
	require.ErrorIs(t, err, ErrInvalidCursor)

	curatorPage, err := fixture.comments.CuratorList(ctx, fixture.actors["curator"], PageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, curatorPage.Comments, 2)
	require.NotNil(t, curatorPage.NextCursor)
	older, err := fixture.comments.CuratorList(ctx, fixture.actors["curator"], PageRequest{Cursor: *curatorPage.NextCursor, Limit: 2})
	require.NoError(t, err)
	require.Len(t, older.Comments, 1)
	assert.Nil(t, older.NextCursor)
	_, err = fixture.comments.CuratorList(ctx, fixture.actors["blair"], PageRequest{})
	require.ErrorIs(t, err, ErrNotCurator)

	commentID := uuid.MustParse(first.ID)
	for index := 0; index < 3; index++ {
		_, err = fixture.db.NewRaw(`INSERT INTO comment_moderation_history
			(comment_id, actor_person_id, prior_state, prior_body, reason, created_at)
			VALUES (?, ?, 'active', 'Retry safely', ?, ?)`, commentID, fixture.people["curator"], fmt.Sprintf("reason-%d", index), createdAt).Exec(ctx)
		require.NoError(t, err)
	}
	history, err := fixture.comments.ModerationHistory(ctx, fixture.actors["curator"], commentID, PageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, history.History, 2)
	require.NotNil(t, history.NextCursor)
	remainingHistory, err := fixture.comments.ModerationHistory(ctx, fixture.actors["curator"], commentID, PageRequest{Cursor: *history.NextCursor, Limit: 2})
	require.NoError(t, err)
	require.Len(t, remainingHistory.History, 1)
	assert.Nil(t, remainingHistory.NextCursor)
}

func TestCuratorFavoriteListPaginatesWithoutCrossRecipientDisclosure(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	updatedAt := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	mediaIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, mediaID := range mediaIDs {
		_, err := fixture.db.NewRaw(`INSERT INTO media_items
			(id, immich_asset_id, media_type, availability, first_seen_at, last_seen_at)
			VALUES (?, gen_random_uuid(), 'image', 'current', ?, ?);
			INSERT INTO favorites (recipient_person_id, media_item_id, is_current, created_at, updated_at)
			VALUES (?, ?, true, ?, ?)`, mediaID, updatedAt, updatedAt, fixture.people["alex"], mediaID, updatedAt, updatedAt).Exec(ctx)
		require.NoError(t, err)
	}
	_, err := fixture.db.NewRaw(`INSERT INTO favorites
		(recipient_person_id, media_item_id, is_current, created_at, updated_at)
		VALUES (?, ?, true, ?, ?)`, fixture.people["blair"], mediaIDs[0], updatedAt, updatedAt).Exec(ctx)
	require.NoError(t, err)

	var listed []string
	cursor := ""
	for {
		page, pageErr := fixture.favorites.CuratorList(ctx, fixture.actors["curator"], fixture.people["alex"], favorites.PageRequest{Cursor: cursor, Limit: 1})
		require.NoError(t, pageErr)
		require.Len(t, page.MediaItemIDs, 1)
		listed = append(listed, page.MediaItemIDs[0])
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	assert.Len(t, listed, 3)
	assert.NotContains(t, listed, fixture.media.String(), "only the selected Recipient's current Favorites are returned")
	_, err = fixture.favorites.CuratorList(ctx, fixture.actors["curator"], fixture.people["blair"], favorites.PageRequest{Cursor: cursor, Limit: 1})
	require.ErrorIs(t, err, favorites.ErrInvalidCursor, "a cursor is bound to one Recipient")
}

func TestFavoritesRemainPrivateAndPersistAcrossAccessLoss(t *testing.T) {
	changes := []struct {
		name   string
		change func(context.Context, interactionFixture) error
	}{
		{
			name: "Withdrawal",
			change: func(ctx context.Context, fixture interactionFixture) error {
				_, err := events.New(fixture.db).Withdraw(ctx,
					setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
					events.WithdrawRequest{TargetKind: events.WithdrawalTargetMedia, TargetID: fixture.media.String(), Reason: "Favorite persistence test"})
				return err
			},
		},
		{
			name: "Suspension",
			change: func(ctx context.Context, fixture interactionFixture) error {
				_, err := recipients.New(fixture.db, nil, "", nil).Suspend(ctx,
					setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
					fixture.people["alex"], fixture.access["alex"])
				return err
			},
		},
		{
			name: "Revocation",
			change: func(ctx context.Context, fixture interactionFixture) error {
				_, err := recipients.New(fixture.db, nil, "", nil).RevokeAccess(ctx,
					setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
					fixture.people["alex"], fixture.access["alex"])
				return err
			},
		},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			fixture := newInteractionFixture(t)
			ctx := context.Background()
			state, err := fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, true)
			require.NoError(t, err)
			assert.True(t, state.Favorite)

			other, err := fixture.favorites.Get(ctx, fixture.actors["blair"], fixture.media)
			require.NoError(t, err)
			assert.False(t, other.Favorite, "another Recipient never receives Alex's Favorite state")
			_, err = fixture.favorites.CuratorList(ctx, fixture.actors["blair"], fixture.people["alex"])
			require.ErrorIs(t, err, favorites.ErrNotCurator)

			require.NoError(t, change.change(ctx, fixture))
			_, err = fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
			require.ErrorIs(t, err, favorites.ErrNotFound, "the Recipient cannot browse an inaccessible Favorite")
			_, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
			require.ErrorIs(t, err, favorites.ErrNotFound, "access loss cannot erase the retained Favorite")

			var retained bool
			require.NoError(t, fixture.db.NewRaw(`SELECT is_current FROM favorites
				WHERE recipient_person_id = ? AND media_item_id = ?`, fixture.people["alex"], fixture.media).Scan(ctx, &retained))
			assert.True(t, retained)
			curatorView, err := fixture.favorites.CuratorList(ctx, fixture.actors["curator"], fixture.people["alex"])
			require.NoError(t, err)
			assert.Equal(t, []string{fixture.media.String()}, curatorView.MediaItemIDs,
				"only the Curator retains cross-Recipient visibility after access loss")
		})
	}

	t.Run("temporary access restoration retains state and transition activity", func(t *testing.T) {
		fixture := newInteractionFixture(t)
		ctx := context.Background()
		_, err := fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, true)
		require.NoError(t, err)
		fixture.removeGrant(t, "alex")
		_, err = fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
		require.ErrorIs(t, err, favorites.ErrNotFound)
		fixture.grant(t, "alex")
		state, err := fixture.favorites.Get(ctx, fixture.actors["alex"], fixture.media)
		require.NoError(t, err)
		assert.True(t, state.Favorite)
		_, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
		require.NoError(t, err)
		_, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
		require.NoError(t, err)
		var actions []string
		require.NoError(t, fixture.db.NewRaw(`SELECT action FROM interaction_activity_items
			WHERE kind = 'favorite' AND favorite_recipient_person_id = ? AND media_item_id = ? ORDER BY id`, fixture.people["alex"], fixture.media).Scan(ctx, &actions))
		assert.Equal(t, []string{"favorite_added", "favorite_removed"}, actions,
			"Favorite activity records only state changes in the shared interaction feed")
	})
}

func TestInteractionMutationsSerializeBeforeWithdrawalAndRevocation(t *testing.T) {
	t.Run("Comment edit before Withdrawal", func(t *testing.T) {
		fixture := newInteractionFixture(t)
		ctx := context.Background()
		created, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Original"})
		require.NoError(t, err)

		blocker, err := fixture.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = blocker.Rollback() }()
		_, err = blocker.NewRaw(`SELECT id FROM comments WHERE id = ? FOR UPDATE`, created.ID).Exec(ctx)
		require.NoError(t, err)

		edited := make(chan error, 1)
		go func() {
			_, editErr := fixture.comments.Edit(ctx, fixture.actors["alex"], uuid.MustParse(created.ID), created.Version, BodyRequest{Body: "Committed before Withdrawal"})
			edited <- editErr
		}()
		waitForBlockedQueries(t, fixture.db, `%UPDATE comments SET body%`, 1)

		withdrawn := make(chan error, 1)
		go func() {
			_, withdrawErr := events.New(fixture.db).Withdraw(ctx,
				setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
				events.WithdrawRequest{TargetKind: events.WithdrawalTargetMedia, TargetID: fixture.media.String(), Reason: "Race test"})
			withdrawn <- withdrawErr
		}()
		waitForInteractionAdvisoryLock(t, fixture.db, "ExclusiveLock")

		require.NoError(t, blocker.Commit())
		require.NoError(t, <-edited)
		require.NoError(t, <-withdrawn)
		_, err = fixture.comments.Edit(ctx, fixture.actors["alex"], uuid.MustParse(created.ID), created.Version+1, BodyRequest{Body: "Too late"})
		require.ErrorIs(t, err, ErrNotFound)
		var body string
		require.NoError(t, fixture.db.NewRaw(`SELECT body FROM comments WHERE id = ?`, created.ID).Scan(ctx, &body))
		assert.Equal(t, "Committed before Withdrawal", body)
	})

	t.Run("Favorite transition before Revocation", func(t *testing.T) {
		fixture := newInteractionFixture(t)
		ctx := context.Background()
		_, err := fixture.db.NewRaw(`INSERT INTO favorites
			(recipient_person_id, media_item_id, is_current, created_at, updated_at)
			VALUES (?, ?, false, now(), now())`, fixture.people["alex"], fixture.media).Exec(ctx)
		require.NoError(t, err)

		blocker, err := fixture.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = blocker.Rollback() }()
		_, err = blocker.NewRaw(`SELECT recipient_person_id FROM favorites
			WHERE recipient_person_id = ? AND media_item_id = ? FOR UPDATE`, fixture.people["alex"], fixture.media).Exec(ctx)
		require.NoError(t, err)

		favorited := make(chan error, 1)
		go func() {
			_, favoriteErr := fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, true)
			favorited <- favoriteErr
		}()
		waitForBlockedQueries(t, fixture.db, `%INSERT INTO favorites%`, 1)

		revoked := make(chan error, 1)
		go func() {
			_, revokeErr := recipients.New(fixture.db, nil, "", nil).RevokeAccess(ctx,
				setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
				fixture.people["alex"], fixture.access["alex"])
			revoked <- revokeErr
		}()
		waitForBlockedQueries(t, fixture.db, `%SELECT id FROM people WHERE id IN%FOR NO KEY UPDATE%`, 1)

		require.NoError(t, blocker.Commit())
		require.NoError(t, <-favorited)
		require.NoError(t, <-revoked)
		_, err = fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, false)
		require.ErrorIs(t, err, favorites.ErrNotFound)
		var retained bool
		require.NoError(t, fixture.db.NewRaw(`SELECT is_current FROM favorites
			WHERE recipient_person_id = ? AND media_item_id = ?`, fixture.people["alex"], fixture.media).Scan(ctx, &retained))
		assert.True(t, retained)
	})
}

func TestCommentActivityRecordingSerializesWithEligibilityChanges(t *testing.T) {
	for _, conflict := range []string{"mute", "delete", "moderate", "Withdrawal", "Revocation"} {
		t.Run(conflict, func(t *testing.T) {
			fixture := newInteractionFixture(t)
			ctx := context.Background()
			_, err := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, uuid.New(), BodyRequest{Body: "Subscribe"})
			require.NoError(t, err)
			created, err := fixture.comments.Create(ctx, fixture.actors["blair"], fixture.media, uuid.New(), BodyRequest{Body: "Pending activity"})
			require.NoError(t, err)
			var payloadText string
			require.NoError(t, fixture.db.NewRaw(`SELECT outbox.payload::text FROM outbox_events AS outbox
				JOIN comment_activity_items AS pending ON outbox.aggregate_id = pending.id::text
				WHERE outbox.kind = ? AND pending.comment_id = ? AND pending.recipient_access_generation_id = ?`,
				CommentJobKind, created.ID, fixture.access["alex"]).Scan(ctx, &payloadText))

			handoffStarted := make(chan struct{})
			releaseHandoff := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(releaseHandoff) })
			activityService := activity.New(fixture.db)
			fixture.comments.SetHandoff(func(ctx context.Context, tx bun.Tx, accessID, commentID uuid.UUID) error {
				close(handoffStarted)
				<-releaseHandoff
				return activityService.RecordComment(ctx, tx, accessID, commentID)
			})
			handled := make(chan error, 1)
			go func() {
				handled <- fixture.comments.HandleCommentJob(ctx, worker.Job{Payload: json.RawMessage(payloadText)})
			}()
			<-handoffStarted

			changed := make(chan error, 1)
			switch conflict {
			case "mute":
				go func() { changed <- fixture.comments.SetMuted(ctx, fixture.actors["alex"], fixture.media, true) }()
				waitForBlockedQueries(t, fixture.db, `%UPDATE comment_subscriptions SET muted%`, 1)
			case "delete":
				go func() {
					changed <- fixture.comments.Delete(ctx, fixture.actors["blair"], uuid.MustParse(created.ID), created.Version)
				}()
				waitForBlockedQueries(t, fixture.db, `%UPDATE comments SET state = 'deleted'%`, 1)
			case "moderate":
				go func() {
					changed <- fixture.comments.Moderate(ctx, fixture.actors["curator"], uuid.MustParse(created.ID), created.Version,
						ModerateRequest{Reason: "Race test"})
				}()
				waitForBlockedQueries(t, fixture.db, `%SELECT state, body, version FROM comments%FOR UPDATE%`, 1)
			case "Withdrawal":
				go func() {
					_, changeErr := events.New(fixture.db).Withdraw(ctx,
						setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
						events.WithdrawRequest{TargetKind: events.WithdrawalTargetMedia, TargetID: fixture.media.String(), Reason: "Race test"})
					changed <- changeErr
				}()
				waitForInteractionAdvisoryLock(t, fixture.db, "ExclusiveLock")
			case "Revocation":
				go func() {
					_, changeErr := recipients.New(fixture.db, nil, "", nil).RevokeAccess(ctx,
						setup.CuratorSession{PersonID: fixture.people["curator"], SessionID: fixture.actors["curator"].SessionID},
						fixture.people["alex"], fixture.access["alex"])
					changed <- changeErr
				}()
				waitForBlockedQueries(t, fixture.db, `%SELECT id FROM people WHERE id IN%FOR NO KEY UPDATE%`, 1)
			}

			releaseOnce.Do(func() { close(releaseHandoff) })
			require.NoError(t, <-handled)
			require.NoError(t, <-changed)
			var activityCount int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM interaction_activity_items
				WHERE kind = 'comment' AND comment_id = ? AND recipient_access_generation_id = ?`,
				created.ID, fixture.access["alex"]).Scan(ctx, &activityCount))
			assert.Equal(t, 1, activityCount, "activity commits before the conflicting eligibility change")
		})
	}
}

func TestInteractionHTTPRoutesUsePersistedSessionsAndPrivateContracts(t *testing.T) {
	fixture := newInteractionFixture(t)
	e, csrf := interactionHTTP(t, fixture)
	mediaPath := fixture.media.String()
	alexCredential := fixture.credentials["alex"]
	blairCredential := fixture.credentials["blair"]
	curatorCredential := fixture.credentials["curator"]
	caseyCredential := fixture.credentials["casey"]

	response := serveInteraction(t, e, http.MethodGet, "/api/me/media/"+mediaPath+"/thumbnail", curatorCredential, "", "", nil)
	assert.Equal(t, http.StatusNotFound, response.Code, "the Recipient media route still follows Audience access")
	response = serveInteraction(t, e, http.MethodGet, "/api/curator/media/"+mediaPath+"/thumbnail", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusNotFound, response.Code, "the moderation representation is Curator-only")
	response = serveInteractionMedia(t, e, "/api/curator/media/"+mediaPath+"/thumbnail", curatorCredential)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "thumbnail", response.Body.String())
	assert.Equal(t, "private, no-cache", response.Header().Get(echo.HeaderCacheControl))
	response = serveInteraction(t, e, http.MethodGet, "/api/curator/media/"+mediaPath, curatorCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl))
	var mediaContext library.CuratorMedia
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &mediaContext))
	assert.Equal(t, "shared-photo.jpg", mediaContext.Filename)
	assert.Equal(t, []string{"Shared Event"}, mediaContext.EventTitles)
	assert.Equal(t, "/api/curator/media/"+mediaPath+"/preview", mediaContext.PreviewURL)

	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath, "", "", "", nil)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/not-a-uuid", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusNotFound, response.Code)

	alexKey := uuid.NewString()
	createHeaders := map[string]string{"Idempotency-Key": alexKey}
	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, "", `{"body":" First HTTP Comment "}`, createHeaders)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, csrf["alex"], `{"body":"First HTTP Comment"}`, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, csrf["alex"], `{}`, map[string]string{"Idempotency-Key": uuid.NewString()})
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)

	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, csrf["alex"], `{"body":" First HTTP Comment "}`, createHeaders)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var alexComment Comment
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &alexComment))
	assert.Equal(t, mediaPath, alexComment.MediaItemID)
	assert.Equal(t, "First HTTP Comment", alexComment.Body)
	assert.Equal(t, int64(1), alexComment.Version)
	assert.True(t, alexComment.AuthoredByMe)
	assert.True(t, alexComment.CanEdit)
	assert.True(t, alexComment.CanDelete)

	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, csrf["alex"], `{"body":"First HTTP Comment"}`, createHeaders)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var replayed Comment
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &replayed))
	assert.Equal(t, alexComment.ID, replayed.ID)
	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, alexCredential, csrf["alex"], `{"body":"Different body"}`, createHeaders)
	assert.Equal(t, http.StatusConflict, response.Code)

	blairKey := uuid.NewString()
	response = serveInteraction(t, e, http.MethodPost, "/api/comments/media/"+mediaPath, blairCredential, csrf["blair"], `{"body":"Second HTTP Comment"}`, map[string]string{"Idempotency-Key": blairKey})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var blairComment Comment
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &blairComment))

	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath+"?limit=1", alexCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var firstPage ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Comments, 1)
	assert.True(t, firstPage.CanMute)
	require.NotNil(t, firstPage.NextCursor)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath+"?limit=1&cursor="+*firstPage.NextCursor, alexCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var secondPage ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &secondPage))
	require.Len(t, secondPage.Comments, 1)
	assert.Nil(t, secondPage.NextCursor)
	assert.NotEqual(t, firstPage.Comments[0].ID, secondPage.Comments[0].ID)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath+"?limit=1&cursor=invalid", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)

	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath, caseyCredential, "", "", nil)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "Comments for this Media are unavailable.")
	assert.NotContains(t, response.Body.String(), alexComment.ID)
	assert.NotContains(t, response.Body.String(), alexComment.Body)
	guessedCommentID := uuid.NewString()
	response = serveInteraction(t, e, http.MethodPatch, "/api/comments/"+guessedCommentID, alexCredential, csrf["alex"], `{"body":"Guess"}`, map[string]string{"If-Match": "1"})
	assert.Equal(t, http.StatusNotFound, response.Code)
	guessedCommentResponse := response.Body.String()
	assert.Contains(t, guessedCommentResponse, "Comments for this Media are unavailable.")
	assert.NotContains(t, guessedCommentResponse, guessedCommentID)
	response = serveInteraction(t, e, http.MethodPatch, "/api/comments/"+alexComment.ID, caseyCredential, csrf["casey"], `{"body":"Guess"}`, map[string]string{"If-Match": "1"})
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, guessedCommentResponse, response.Body.String(), "guessed and inaccessible Comment identifiers are indistinguishable")

	response = serveInteraction(t, e, http.MethodPatch, "/api/comments/"+alexComment.ID, alexCredential, csrf["alex"], `{"body":"Edited over HTTP"}`, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	response = serveInteraction(t, e, http.MethodPatch, "/api/comments/"+alexComment.ID, alexCredential, csrf["alex"], `{"body":"Edited over HTTP"}`, map[string]string{"If-Match": "1"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var edited Comment
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &edited))
	assert.Equal(t, "Edited over HTTP", edited.Body)
	assert.Equal(t, int64(2), edited.Version)
	response = serveInteraction(t, e, http.MethodPatch, "/api/comments/"+alexComment.ID, alexCredential, csrf["alex"], `{"body":"Stale edit"}`, map[string]string{"If-Match": "1"})
	assert.Equal(t, http.StatusConflict, response.Code)

	response = serveInteraction(t, e, http.MethodPut, "/api/comments/media/"+mediaPath+"/mute", alexCredential, csrf["alex"], `{}`, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "muted")
	assert.Contains(t, response.Body.String(), "is required")
	response = serveInteraction(t, e, http.MethodPut, "/api/comments/media/"+mediaPath+"/mute", alexCredential, csrf["alex"], `{"muted":null}`, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "muted")
	assert.Contains(t, response.Body.String(), "is required")
	response = serveInteraction(t, e, http.MethodPut, "/api/comments/media/"+mediaPath+"/mute", alexCredential, csrf["alex"], `{"muted":true}`, nil)
	assert.Equal(t, http.StatusNoContent, response.Code)
	response = serveInteraction(t, e, http.MethodPut, "/api/comments/media/"+mediaPath+"/mute", alexCredential, csrf["alex"], `{"muted":false}`, nil)
	assert.Equal(t, http.StatusNoContent, response.Code)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/curator?limit=1", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/curator?limit=1", curatorCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var curatorPage CuratorListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &curatorPage))
	require.Len(t, curatorPage.Comments, 1)
	require.NotNil(t, curatorPage.NextCursor)

	response = serveInteraction(t, e, http.MethodPost, "/api/comments/"+blairComment.ID+"/moderate", curatorCredential, csrf["curator"], `{"reason":"Privacy review"}`, map[string]string{"If-Match": "1"})
	assert.Equal(t, http.StatusNoContent, response.Code)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/"+blairComment.ID+"/moderation-history?limit=1", curatorCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var history HistoryResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &history))
	require.Len(t, history.History, 1)
	assert.Equal(t, "Second HTTP Comment", history.History[0].PriorBody)
	assert.Equal(t, "Privacy review", history.History[0].Reason)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/"+blairComment.ID+"/moderation-history", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusForbidden, response.Code)

	response = serveInteraction(t, e, http.MethodGet, "/api/favorites/"+mediaPath, alexCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var favoriteState favorites.State
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &favoriteState))
	assert.False(t, favoriteState.Favorite)
	response = serveInteraction(t, e, http.MethodPut, "/api/favorites/"+mediaPath, alexCredential, "", "", nil)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = serveInteraction(t, e, http.MethodPut, "/api/favorites/"+mediaPath, alexCredential, csrf["alex"], "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &favoriteState))
	assert.Equal(t, mediaPath, favoriteState.MediaItemID)
	assert.True(t, favoriteState.Favorite)
	response = serveInteraction(t, e, http.MethodGet, "/api/favorites/"+mediaPath, caseyCredential, "", "", nil)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "This Media is unavailable.")
	assert.NotContains(t, response.Body.String(), mediaPath)

	otherMedia := uuid.New()
	_, err := fixture.db.NewRaw(`INSERT INTO media_items
		(id, immich_asset_id, media_type, availability, first_seen_at, last_seen_at)
		VALUES (?, gen_random_uuid(), 'image', 'current', now(), now());
		INSERT INTO favorites (recipient_person_id, media_item_id, is_current, created_at, updated_at)
		VALUES (?, ?, true, now(), now())`, otherMedia, fixture.people["alex"], otherMedia).Exec(context.Background())
	require.NoError(t, err)
	favoriteListPath := "/api/favorites/curator/recipients/" + fixture.people["alex"].String()
	response = serveInteraction(t, e, http.MethodGet, favoriteListPath+"?limit=1", alexCredential, "", "", nil)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = serveInteraction(t, e, http.MethodGet, favoriteListPath+"?limit=1", curatorCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var favoritePage favorites.CuratorListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &favoritePage))
	assert.Equal(t, fixture.people["alex"].String(), favoritePage.RecipientPersonID)
	require.Len(t, favoritePage.MediaItemIDs, 1)
	require.NotNil(t, favoritePage.NextCursor)
	response = serveInteraction(t, e, http.MethodGet, favoriteListPath+"?limit=1&cursor="+*favoritePage.NextCursor, curatorCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &favoritePage))
	require.Len(t, favoritePage.MediaItemIDs, 1)
	assert.Nil(t, favoritePage.NextCursor)

	response = serveInteraction(t, e, http.MethodDelete, "/api/favorites/"+mediaPath, alexCredential, csrf["alex"], "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &favoriteState))
	assert.False(t, favoriteState.Favorite)
	response = serveInteraction(t, e, http.MethodDelete, "/api/comments/"+alexComment.ID, alexCredential, csrf["alex"], "", map[string]string{"If-Match": "2"})
	assert.Equal(t, http.StatusNoContent, response.Code)
	response = serveInteraction(t, e, http.MethodGet, "/api/comments/media/"+mediaPath+"?limit=100", alexCredential, "", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var finalThread ListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &finalThread))
	deletedFound := false
	for _, comment := range finalThread.Comments {
		if comment.ID == alexComment.ID {
			deletedFound = true
			assert.Equal(t, "deleted", comment.State)
			assert.Empty(t, comment.Body)
			assert.Equal(t, int64(3), comment.Version)
		}
	}
	assert.True(t, deletedFound, "the deleted Comment remains in the route response")
}

func TestConcurrentIdenticalCommentCreatesReturnOneCommentAndOneEffectSet(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	key := uuid.New()
	blocker, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.NewRaw(`SELECT id FROM system_settings WHERE id = 1 FOR UPDATE`).Exec(ctx)
	require.NoError(t, err)

	type result struct {
		comment Comment
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			comment, createErr := fixture.comments.Create(ctx, fixture.actors["alex"], fixture.media, key, BodyRequest{Body: "One simultaneous Comment"})
			results <- result{comment: comment, err: createErr}
		}()
	}
	close(start)
	waitForBlockedQueries(t, fixture.db, `%SELECT id FROM system_settings WHERE id = 1 FOR SHARE%`, 2)
	require.NoError(t, blocker.Commit())
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Equal(t, first.comment.ID, second.comment.ID)
	assert.Equal(t, "One simultaneous Comment", first.comment.Body)

	var commentsCount, subscriptionsCount, activitiesCount, outboxCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comments
		WHERE author_access_generation_id = ? AND idempotency_key = ?`, fixture.access["alex"], key).Scan(ctx, &commentsCount))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_subscriptions
		WHERE media_item_id = ? AND recipient_access_generation_id = ?`, fixture.media, fixture.access["alex"]).Scan(ctx, &subscriptionsCount))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM comment_activity_items
		WHERE comment_id = ?`, first.comment.ID).Scan(ctx, &activitiesCount))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events AS outbox
		JOIN comment_activity_items AS activity ON outbox.aggregate_id = activity.id::text
		WHERE outbox.kind = ? AND activity.comment_id = ?`, CommentJobKind, first.comment.ID).Scan(ctx, &outboxCount))
	assert.Equal(t, 1, commentsCount)
	assert.Equal(t, 1, subscriptionsCount)
	assert.Equal(t, 1, activitiesCount)
	assert.Equal(t, 1, outboxCount)
}

func TestConcurrentIdenticalFavoriteRequestsRecordOneTransition(t *testing.T) {
	fixture := newInteractionFixture(t)
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`INSERT INTO favorites
		(recipient_person_id, media_item_id, is_current, created_at, updated_at)
		VALUES (?, ?, false, now(), now())`, fixture.people["alex"], fixture.media).Exec(ctx)
	require.NoError(t, err)

	run := func(favorite bool, queryPattern string) {
		blocker, beginErr := fixture.db.BeginTx(ctx, nil)
		require.NoError(t, beginErr)
		defer func() { _ = blocker.Rollback() }()
		_, lockErr := blocker.NewRaw(`SELECT recipient_person_id FROM favorites
			WHERE recipient_person_id = ? AND media_item_id = ? FOR UPDATE`, fixture.people["alex"], fixture.media).Exec(ctx)
		require.NoError(t, lockErr)

		start := make(chan struct{})
		results := make(chan error, 2)
		for index := 0; index < 2; index++ {
			go func() {
				<-start
				_, setErr := fixture.favorites.Set(ctx, fixture.actors["alex"], fixture.media, favorite)
				results <- setErr
			}()
		}
		close(start)
		waitForBlockedQueries(t, fixture.db, queryPattern, 2)
		require.NoError(t, blocker.Commit())
		require.NoError(t, <-results)
		require.NoError(t, <-results)
	}

	run(true, `%INSERT INTO favorites%`)
	run(false, `%UPDATE favorites SET is_current = false%`)
	var actions []string
	require.NoError(t, fixture.db.NewRaw(`SELECT action FROM interaction_activity_items
		WHERE kind = 'favorite' AND favorite_recipient_person_id = ? AND media_item_id = ? ORDER BY id`,
		fixture.people["alex"], fixture.media).Scan(ctx, &actions))
	assert.Equal(t, []string{"favorite_added", "favorite_removed"}, actions)
}
