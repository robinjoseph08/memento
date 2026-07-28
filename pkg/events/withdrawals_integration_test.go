//go:build integration

package events

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type withdrawalRecipientAuthorizer struct {
	actor setup.SessionActor
}

func (authorizer withdrawalRecipientAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return authorizer.actor, nil
}

type withdrawalThumbnailSource struct {
	mu     sync.Mutex
	assets []uuid.UUID
}

func (source *withdrawalThumbnailSource) Thumbnail(_ context.Context, assetID uuid.UUID) (immich.MediaResponse, error) {
	source.mu.Lock()
	source.assets = append(source.assets, assetID)
	source.mu.Unlock()
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewBufferString("thumbnail")), ContentType: "image/webp", ContentLength: 9,
	}, nil
}

func (source *withdrawalThumbnailSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.assets)
}

func withdrawalRecipientHTTP(fixture publicationFixture, actor setup.SessionActor, source *withdrawalThumbnailSource) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	service := library.New(fixture.db, nil)
	if source != nil {
		service = library.New(fixture.db, source)
	}
	library.RegisterRoutes(e, library.NewHandler(service, withdrawalRecipientAuthorizer{actor: actor}))
	return e
}

type withdrawalTestCase struct {
	name    string
	kind    WithdrawalTargetKind
	target  func(publicationFixture) uuid.UUID
	reviews []int
}

func withdrawalTestCases() []withdrawalTestCase {
	return []withdrawalTestCase{
		{name: "Event", kind: WithdrawalTargetEvent, target: func(f publicationFixture) uuid.UUID { return f.event }, reviews: []int{0, 1, 2}},
		{name: "Moment", kind: WithdrawalTargetMoment, target: func(f publicationFixture) uuid.UUID { return f.moments[0] }, reviews: []int{0}},
		{name: "Media", kind: WithdrawalTargetMedia, target: func(f publicationFixture) uuid.UUID { return f.media[0] }, reviews: []int{0}},
	}
}

func createWithdrawalRecipientSession(t *testing.T, fixture publicationFixture, actor setup.SessionActor) {
	t.Helper()
	_, err := fixture.db.NewRaw(`INSERT INTO sessions (
		id, credential_hash, person_id, recipient_access_generation_id,
		security_epoch, session_type, idle_expires_at
	) SELECT ?, decode(repeat('43', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
	  FROM system_settings WHERE id = 1`, actor.SessionID, actor.PersonID, actor.AccessID).Exec(context.Background())
	require.NoError(t, err)
}

func snapshotWithdrawalState(t *testing.T, fixture publicationFixture, tables []string) map[string]string {
	t.Helper()
	state := make(map[string]string, len(tables))
	for _, table := range tables {
		var serialized string
		require.NoError(t, fixture.db.NewRaw(fmt.Sprintf(`
			SELECT COALESCE(
				jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text),
				'[]'::jsonb
			)::text FROM %s AS row_value
		`, table)).Scan(context.Background(), &serialized), table)
		state[table] = serialized
	}
	return state
}

func snapshotWithdrawalRows(t *testing.T, fixture publicationFixture, query string, args ...any) string {
	t.Helper()
	var serialized string
	require.NoError(t, fixture.db.NewRaw(`
		SELECT COALESCE(
			jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text),
			'[]'::jsonb
		)::text FROM (`+query+`) AS row_value
	`, args...).Scan(context.Background(), &serialized))
	return serialized
}

func reviewForFreshPublication(t *testing.T, fixture publicationFixture, priorPublicationID string, reviews []int) {
	t.Helper()
	ctx := context.Background()
	for _, index := range reviews {
		freshSnapshot := uuid.New()
		_, err := fixture.db.NewRaw(`
			INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
			VALUES (?, 'moment', ?, ?, now(), 'Shared');
			INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
			SELECT ?, entry.recipient_person_id, entry.recipient_access_generation_id
			FROM audience_entries AS entry
			JOIN published_moments AS moment ON moment.id = entry.published_moment_id
			WHERE moment.publication_id = ? AND moment.draft_moment_id = ?;
			INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
			VALUES ('moment', ?, ?);
			UPDATE draft_moments SET audience_complete = true WHERE id = ?
		`, freshSnapshot, fixture.moments[index], fixture.actor.PersonID,
			freshSnapshot, priorPublicationID, fixture.moments[index], fixture.moments[index],
			freshSnapshot, fixture.moments[index]).Exec(ctx)
		require.NoError(t, err)
	}
	_, err := fixture.db.NewRaw(`UPDATE events SET final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx)
	require.NoError(t, err)
}

func TestWithdrawalImmediatelyDeniesOnlyThePublishedTargetAndPreservesHistory(t *testing.T) {
	for _, test := range []struct {
		name               string
		kind               WithdrawalTargetKind
		target             func(publicationFixture) uuid.UUID
		expectedRecipients int
		invalidatedReview  int
		sharedDenied       bool
		hiddenDenied       bool
		remainingCurrent   int
	}{
		{name: "Event", kind: "event", target: func(f publicationFixture) uuid.UUID { return f.event }, expectedRecipients: 2, invalidatedReview: 3, sharedDenied: true, hiddenDenied: true},
		{name: "Moment", kind: "moment", target: func(f publicationFixture) uuid.UUID { return f.moments[0] }, expectedRecipients: 1, invalidatedReview: 1, sharedDenied: true, remainingCurrent: 1},
		{name: "Media", kind: "media", target: func(f publicationFixture) uuid.UUID { return f.media[0] }, expectedRecipients: 1, invalidatedReview: 1, sharedDenied: true, remainingCurrent: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			recipientActor := fixture.actorFor("shared")
			_, err = fixture.db.NewRaw(`INSERT INTO sessions (
				id, credential_hash, person_id, recipient_access_generation_id,
				security_epoch, session_type, idle_expires_at
			) SELECT ?, decode(repeat('43', 32), 'hex'), ?, ?, security_epoch, 'trusted', now() + interval '1 hour'
			  FROM system_settings WHERE id = 1`, recipientActor.SessionID, recipientActor.PersonID,
				recipientActor.AccessID).Exec(ctx)
			require.NoError(t, err)
			recipientHTTP := withdrawalRecipientHTTP(fixture, recipientActor, nil)
			openedPath := "/api/me/events/" + fixture.event.String()
			require.Equal(t, http.StatusOK, draftRequest(recipientHTTP, http.MethodGet, openedPath, "").Code)
			_, err = fixture.db.NewRaw(`INSERT INTO favorites (recipient_person_id, media_item_id) VALUES (?, ?)`, fixture.people["shared"], fixture.media[0]).Exec(ctx)
			require.NoError(t, err)
			_, err = fixture.db.NewRaw(`
				INSERT INTO outbox_events (
					kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at
				) SELECT kind, aggregate_kind, aggregate_id, 99, payload, available_at, created_at
				  FROM outbox_events WHERE aggregate_id = ?;
				INSERT INTO outbox_events (
					kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at
				) VALUES ('unrelated_delivery', 'test', ?, 1, '{}'::jsonb, now(), now());
				INSERT INTO jobs (kind, payload) VALUES
					('publication_committed', ?::jsonb),
					('unrelated_job', '{}'::jsonb)
			`, fixture.event.String(), uuid.NewString(), fmt.Sprintf(
				`{"event_id":%q,"publication_id":%q}`,
				fixture.event.String(), publication.ID,
			)).Exec(ctx)
			require.NoError(t, err)

			preservedHistoryTables := []string{
				"publications", "published_event_revisions", "published_moments",
				"published_media_placements", "audience_entries",
				"current_published_placements", "favorites",
			}
			preservedHistoryBefore := snapshotWithdrawalState(t, fixture, preservedHistoryTables)
			var priorAuditMaxID int64
			require.NoError(t, fixture.db.NewRaw(`SELECT COALESCE(max(id), 0) FROM publication_audit_events`).Scan(ctx, &priorAuditMaxID))
			priorAuditBefore := snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM publication_audit_events WHERE id <= ?`, priorAuditMaxID)
			var outboxBefore, jobsBefore int
			require.NoError(t, fixture.db.NewRaw(`SELECT
				(SELECT count(*) FROM outbox_events), (SELECT count(*) FROM jobs)
			`).Scan(ctx, &outboxBefore, &jobsBefore))

			withdrawal, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Requested by the family",
			})
			require.NoError(t, err)
			assert.Equal(t, "Requested by the family", withdrawal.Reason)
			assert.Equal(t, test.expectedRecipients, withdrawal.AffectedRecipientCount)
			assert.NotZero(t, withdrawal.AffectedMediaCount)

			_, sharedErr := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
			_, hiddenErr := fixture.service.RecipientEvent(ctx, fixture.actorFor("hidden"), fixture.event)
			if test.sharedDenied {
				assert.ErrorIs(t, sharedErr, ErrNoPublication)
			} else {
				assert.NoError(t, sharedErr)
			}
			if test.hiddenDenied {
				assert.ErrorIs(t, hiddenErr, ErrNoPublication)
			} else {
				assert.NoError(t, hiddenErr)
			}
			openedResponse := draftRequest(recipientHTTP, http.MethodGet, openedPath, "")
			guessedResponse := draftRequest(recipientHTTP, http.MethodGet, "/api/me/events/"+uuid.NewString(), "")
			assert.Equal(t, http.StatusNotFound, openedResponse.Code, "a previously opened Recipient route must be denied")
			assert.Equal(t, guessedResponse.Code, openedResponse.Code, "withdrawn and guessed routes must be indistinguishable")
			assert.Equal(t, guessedResponse.Body.String(), openedResponse.Body.String())

			assert.Equal(t, preservedHistoryBefore,
				snapshotWithdrawalState(t, fixture, preservedHistoryTables),
				"Withdrawal must preserve immutable Publication history, current placements, and Favorites while Recipient projections change")
			assert.Equal(t, priorAuditBefore, snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM publication_audit_events WHERE id <= ?`, priorAuditMaxID),
				"Withdrawal must append to rather than alter prior audit history")

			var incomplete, snapshots int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_moments WHERE event_id = ? AND NOT audience_complete`, fixture.event).Scan(ctx, &incomplete))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_audience_snapshots AS current JOIN draft_moments AS moment ON moment.id = current.target_id WHERE current.target_kind = 'moment' AND moment.event_id = ?`, fixture.event).Scan(ctx, &snapshots))
			assert.Equal(t, test.invalidatedReview, incomplete)
			assert.Equal(t, len(fixture.moments)-test.invalidatedReview, snapshots)

			var outboxAfter, jobsAfter, deliverableOutbox, queuedPublicationJobs int
			var failedPublicationJobs, unrelatedOutbox, unrelatedJobs int
			require.NoError(t, fixture.db.NewRaw(`SELECT
				(SELECT count(*) FROM outbox_events),
				(SELECT count(*) FROM jobs),
				(SELECT count(*) FROM outbox_events
				 WHERE kind = 'publication_committed' AND aggregate_kind = 'event_publication'
				   AND aggregate_id = ? AND delivered_at IS NULL),
				(SELECT count(*) FROM jobs
				 WHERE kind = 'publication_committed' AND payload->>'event_id' = ?
				   AND status IN ('pending', 'running')),
				(SELECT count(*) FROM jobs
				 WHERE kind = 'publication_committed' AND payload->>'event_id' = ?
				   AND status = 'failed' AND last_safe_error = 'publication_withdrawn'),
				(SELECT count(*) FROM outbox_events
				 WHERE kind = 'unrelated_delivery' AND delivered_at IS NULL),
				(SELECT count(*) FROM jobs
				 WHERE kind = 'unrelated_job' AND status = 'pending')
			`, fixture.event.String(), fixture.event.String(), fixture.event.String()).Scan(ctx,
				&outboxAfter, &jobsAfter, &deliverableOutbox, &queuedPublicationJobs,
				&failedPublicationJobs, &unrelatedOutbox, &unrelatedJobs))
			assert.Equal(t, outboxBefore, outboxAfter, "Withdrawal must not create or erase outbox history")
			assert.Equal(t, jobsBefore, jobsAfter, "Withdrawal must not enqueue a direct job")
			assert.Zero(t, deliverableOutbox, "every queued Publication outbox record for the target must be retired")
			assert.Zero(t, queuedPublicationJobs, "every directly queued Publication job for the target must be retired")
			assert.Equal(t, 1, failedPublicationJobs)
			assert.Equal(t, 1, unrelatedOutbox, "unrelated outbox delivery must remain claimable")
			assert.Equal(t, 1, unrelatedJobs, "unrelated queued work must remain pending")
			for _, table := range []string{"current_audience_entitlements", "current_recipient_event_covers", "published_search_documents", "new_for_you_entries", "publication_activity_items"} {
				var count int
				require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM `+table).Scan(ctx, &count), table)
				assert.Equal(t, test.remainingCurrent, count, table)
			}

			var auditAction, auditReason string
			require.NoError(t, fixture.db.NewRaw(`SELECT action, metadata->>'reason' FROM publication_audit_events WHERE event_id = ? AND action = 'content_withdrawn'`, fixture.event).Scan(ctx, &auditAction, &auditReason))
			assert.Equal(t, "content_withdrawn", auditAction)
			assert.Equal(t, "Requested by the family", auditReason)
			event, err := fixture.service.GetEvent(ctx, fixture.event)
			require.NoError(t, err)
			require.Len(t, event.Withdrawals, 1)
			assert.Equal(t, test.kind, event.Withdrawals[0].TargetKind)
			assert.Equal(t, test.target(fixture).String(), event.Withdrawals[0].TargetID)
			assert.Equal(t, "Requested by the family", event.Withdrawals[0].Reason)
		})
	}
}

func TestPartialWithdrawalPreservesEventProjectionsForRecipientsWithAnotherEntitlement(t *testing.T) {
	for _, test := range []struct {
		name string
		kind WithdrawalTargetKind
	}{
		{name: "Moment", kind: WithdrawalTargetMoment},
		{name: "Media", kind: WithdrawalTargetMedia},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.db.NewRaw(`
				INSERT INTO audience_snapshot_entries (
					snapshot_id, recipient_person_id, recipient_access_generation_id
				)
				SELECT snapshot_id, ?, ? FROM current_audience_snapshots
				WHERE target_kind = 'moment' AND target_id = ?
			`, fixture.people["shared"], fixture.access["shared"], fixture.moments[1]).Exec(ctx)
			require.NoError(t, err)
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)

			newForYouBefore := snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM new_for_you_entries
				 WHERE recipient_access_generation_id = ? AND publication_id = ?`,
				fixture.access["shared"], publication.ID)
			activityBefore := snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM publication_activity_items
				 WHERE recipient_access_generation_id = ? AND publication_id = ?`,
				fixture.access["shared"], publication.ID)

			targetID := fixture.media[0]
			if test.kind == WithdrawalTargetMoment {
				targetID = fixture.moments[0]
			}
			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: targetID.String(), Reason: "Preserve remaining access",
			})
			require.NoError(t, err)

			assert.Equal(t, newForYouBefore, snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM new_for_you_entries
				 WHERE recipient_access_generation_id = ? AND publication_id = ?`,
				fixture.access["shared"], publication.ID))
			assert.Equal(t, activityBefore, snapshotWithdrawalRows(t, fixture,
				`SELECT * FROM publication_activity_items
				 WHERE recipient_access_generation_id = ? AND publication_id = ?`,
				fixture.access["shared"], publication.ID))
			view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
			require.NoError(t, err)
			require.Len(t, view.Media, 1)
			assert.Equal(t, fixture.media[1].String(), view.Media[0].ID)
		})
	}
}

func TestEveryImplementedRecipientContentRouteEnforcesEveryWithdrawalKind(t *testing.T) {
	for _, test := range withdrawalTestCases() {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			recipientActor := fixture.actorFor("shared")
			createWithdrawalRecipientSession(t, fixture, recipientActor)
			_, err = fixture.db.NewRaw(`
				INSERT INTO favorites (recipient_person_id, media_item_id) VALUES (?, ?);
				INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
				SELECT gen_random_uuid(), id, immich_asset_id, now() FROM media_items WHERE id = ?
			`, fixture.people["shared"], fixture.media[0], fixture.media[0]).Exec(ctx)
			require.NoError(t, err)
			thumbnail := &withdrawalThumbnailSource{}
			recipientHTTP := withdrawalRecipientHTTP(fixture, recipientActor, thumbnail)
			var routes []string
			for _, route := range recipientHTTP.Routes() {
				if route.Method != echo.RouteNotFound {
					routes = append(routes, route.Method+" "+route.Path)
				}
			}
			assert.ElementsMatch(t, []string{
				"GET /api/me/photos", "GET /api/me/favorites", "GET /api/me/events",
				"GET /api/me/events/:id", "GET /api/me/new-for-you",
				"POST /api/me/new-for-you/:publication_id/seen", "GET /api/me/media/:id/thumbnail",
			}, routes, "the matrix must be updated whenever an implemented Recipient content surface changes")

			for _, path := range []string{
				"/api/me/photos", "/api/me/favorites", "/api/me/events",
				"/api/me/events/" + fixture.event.String(), "/api/me/new-for-you",
				"/api/me/media/" + fixture.media[0].String() + "/thumbnail",
			} {
				assert.Equal(t, http.StatusOK, draftRequest(recipientHTTP, http.MethodGet, path, "").Code, "pre-open %s", path)
			}
			require.Equal(t, 1, thumbnail.callCount())

			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Route matrix",
			})
			require.NoError(t, err)

			for _, path := range []string{
				"/api/me/photos", "/api/me/favorites", "/api/me/events", "/api/me/new-for-you",
			} {
				response := draftRequest(recipientHTTP, http.MethodGet, path, "")
				assert.Equal(t, http.StatusOK, response.Code, path)
				assert.NotContains(t, response.Body.String(), fixture.event.String(), path)
				assert.NotContains(t, response.Body.String(), fixture.media[0].String(), path)
				assert.NotContains(t, response.Body.String(), publication.ID, path)
			}

			openedEvent := draftRequest(recipientHTTP, http.MethodGet, "/api/me/events/"+fixture.event.String(), "")
			guessedEvent := draftRequest(recipientHTTP, http.MethodGet, "/api/me/events/"+uuid.NewString(), "")
			assert.Equal(t, http.StatusNotFound, openedEvent.Code)
			assert.Equal(t, guessedEvent.Code, openedEvent.Code)
			assert.Equal(t, guessedEvent.Body.String(), openedEvent.Body.String())

			openedSeen := draftRequest(recipientHTTP, http.MethodPost, "/api/me/new-for-you/"+publication.ID+"/seen", "")
			guessedSeen := draftRequest(recipientHTTP, http.MethodPost, "/api/me/new-for-you/"+uuid.NewString()+"/seen", "")
			assert.Equal(t, http.StatusNotFound, openedSeen.Code)
			assert.Equal(t, guessedSeen.Code, openedSeen.Code)
			assert.Equal(t, guessedSeen.Body.String(), openedSeen.Body.String())

			openedThumbnail := draftRequest(recipientHTTP, http.MethodGet, "/api/me/media/"+fixture.media[0].String()+"/thumbnail", "")
			guessedThumbnail := draftRequest(recipientHTTP, http.MethodGet, "/api/me/media/"+uuid.NewString()+"/thumbnail", "")
			assert.Equal(t, http.StatusNotFound, openedThumbnail.Code)
			assert.Equal(t, guessedThumbnail.Code, openedThumbnail.Code)
			assert.Equal(t, guessedThumbnail.Body.String(), openedThumbnail.Body.String())
			assert.Equal(t, 1, thumbnail.callCount(), "denied and guessed thumbnails must not reach Immich")
		})
	}
}

func TestFailureAtEveryWithdrawalBoundaryRollsBackEveryMutation(t *testing.T) {
	steps := []WithdrawalStep{
		WithdrawalStepTargeted, WithdrawalStepLocked, WithdrawalStepRecorded, WithdrawalStepProjections,
		WithdrawalStepActivity, WithdrawalStepDelivery, WithdrawalStepReviews, WithdrawalStepAudit,
	}
	tables := []string{
		"system_settings", "events", "draft_moments", "current_audience_snapshots",
		"content_withdrawals", "published_search_documents", "current_audience_entitlements",
		"current_recipient_event_covers", "new_for_you_entries", "publication_activity_items",
		"outbox_events", "jobs", "publication_audit_events",
	}
	injected := errors.New("injected Withdrawal failure")
	for _, test := range withdrawalTestCases() {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			dispatched, err := outbox.New(fixture.db).Dispatch(ctx, "withdrawal-rollback", time.Minute)
			require.NoError(t, err)
			require.True(t, dispatched)
			_, err = fixture.db.NewRaw(`INSERT INTO outbox_events (
				kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at
			) SELECT kind, aggregate_kind, aggregate_id, 99, payload, available_at, created_at
			  FROM outbox_events WHERE aggregate_id = ?`, fixture.event.String()).Exec(ctx)
			require.NoError(t, err)
			prior := snapshotWithdrawalState(t, fixture, tables)

			for _, failedStep := range steps {
				fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
					if step == failedStep {
						return injected
					}
					return nil
				}
				_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Rollback test",
				})
				assert.ErrorIs(t, err, injected, failedStep)
				assert.Equal(t, prior, snapshotWithdrawalState(t, fixture, tables), failedStep)
			}
			fixture.service.failWithdrawalStep = nil
		})
	}
}

func TestConcurrentRecipientReadSeesCompletePriorAccessUntilWithdrawalCommits(t *testing.T) {
	for _, test := range withdrawalTestCases() {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			reached := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
				if step == WithdrawalStepAudit {
					close(reached)
					<-release
				}
				return nil
			}
			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Atomic visibility",
				})
				withdrawn <- withdrawErr
			}()
			<-reached

			prior, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
			require.NoError(t, err)
			require.Len(t, prior.Media, 1, "uncommitted Withdrawal must not expose a partial projection")
			assert.Equal(t, fixture.media[0].String(), prior.Media[0].ID)

			releaseOnce.Do(func() { close(release) })
			require.NoError(t, <-withdrawn)
			fixture.service.failWithdrawalStep = nil
			_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
			assert.ErrorIs(t, err, ErrNoPublication, "committed Withdrawal must deny the same read")
		})
	}
}

func TestConcurrentPublicationRemovalIsRevalidatedAfterWithdrawalLocksTheEvent(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   WithdrawalTargetKind
		target func(publicationFixture) uuid.UUID
		stage  func(context.Context, publicationFixture) error
	}{
		{
			name: "Moment", kind: WithdrawalTargetMoment,
			target: func(f publicationFixture) uuid.UUID { return f.moments[0] },
			stage: func(ctx context.Context, f publicationFixture) error {
				_, err := f.db.NewRaw(`
					DELETE FROM draft_media_placements WHERE draft_moment_id = ?;
					DELETE FROM draft_moments WHERE id = ?;
					UPDATE events SET version = 8 WHERE id = ?
				`, f.moments[0], f.moments[0], f.event).Exec(ctx)
				return err
			},
		},
		{
			name: "Media", kind: WithdrawalTargetMedia,
			target: func(f publicationFixture) uuid.UUID { return f.media[0] },
			stage: func(ctx context.Context, f publicationFixture) error {
				_, err := f.db.NewRaw(`
					DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?;
					UPDATE events SET version = 8 WHERE id = ?
				`, f.event, f.media[0], f.event).Exec(ctx)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			require.NoError(t, test.stage(ctx, fixture))

			publicationReached := make(chan struct{})
			releasePublication := make(chan struct{})
			var releasePublicationOnce sync.Once
			defer releasePublicationOnce.Do(func() { close(releasePublication) })
			fixture.service.failPublicationStep = func(step PublicationStep) error {
				if step == PublicationStepPlacements {
					close(publicationReached)
					<-releasePublication
				}
				return nil
			}
			published := make(chan error, 1)
			go func() {
				_, publishErr := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
				published <- publishErr
			}()
			select {
			case <-publicationReached:
			case <-time.After(5 * time.Second):
				t.Fatal("Publication did not reach the placement boundary")
			}

			withdrawalTargeted := make(chan struct{})
			fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
				if step == WithdrawalStepTargeted {
					close(withdrawalTargeted)
				}
				return nil
			}
			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Concurrent removal",
				})
				withdrawn <- withdrawErr
			}()
			select {
			case <-withdrawalTargeted:
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not discover the previously published target")
			}
			releasePublicationOnce.Do(func() { close(releasePublication) })

			select {
			case publishErr := <-published:
				require.NoError(t, publishErr)
			case <-time.After(5 * time.Second):
				t.Fatal("Publication did not commit after release")
			}
			select {
			case withdrawErr := <-withdrawn:
				assert.ErrorIs(t, withdrawErr, ErrNotFound)
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not finish after Publication committed")
			}
			fixture.service.failPublicationStep = nil
			fixture.service.failWithdrawalStep = nil

			var withdrawals, withdrawalAudits int
			var version int64
			require.NoError(t, fixture.db.NewRaw(`SELECT
				(SELECT count(*) FROM content_withdrawals),
				(SELECT count(*) FROM publication_audit_events WHERE action = 'content_withdrawn'),
				(SELECT version FROM events WHERE id = ?)
			`, fixture.event).Scan(ctx, &withdrawals, &withdrawalAudits, &version))
			assert.Zero(t, withdrawals)
			assert.Zero(t, withdrawalAudits)
			assert.Equal(t, int64(8), version, "rejected Withdrawal must not alter the published draft")
		})
	}
}

func TestFailedFreshPublicationKeepsEveryWithdrawalActiveAndRollsBackRestoration(t *testing.T) {
	failedSteps := []PublicationStep{
		PublicationStepEntitlements, PublicationStepActivity, PublicationStepAudit, PublicationStepOutbox,
	}
	tables := []string{
		"system_settings", "events", "publications", "published_event_revisions", "published_moments",
		"published_media_placements", "audience_entries", "current_published_events",
		"current_published_placements", "current_audience_entitlements", "current_recipient_event_covers",
		"new_for_you_entries", "published_search_documents", "publication_activity_items",
		"publication_curator_activity_items", "publication_audit_events", "outbox_events", "content_withdrawals",
	}
	injected := errors.New("injected post-restoration Publication failure")
	for _, test := range withdrawalTestCases() {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			withdrawal, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Keep active on failure",
			})
			require.NoError(t, err)
			reviewForFreshPublication(t, fixture, first.ID, test.reviews)
			prior := snapshotWithdrawalState(t, fixture, tables)

			for _, failedStep := range failedSteps {
				fixture.service.failPublicationStep = func(step PublicationStep) error {
					if step == failedStep {
						return injected
					}
					return nil
				}
				_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
				assert.ErrorIs(t, err, injected, failedStep)
				assert.Equal(t, prior, snapshotWithdrawalState(t, fixture, tables), failedStep)
				var active bool
				require.NoError(t, fixture.db.NewRaw(`SELECT restored_at IS NULL AND restored_by_publication_id IS NULL
					FROM content_withdrawals WHERE id = ?`, withdrawal.ID).Scan(ctx, &active))
				assert.True(t, active, failedStep)
				var restorationAudits int
				require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_audit_events
					WHERE action = 'content_restored_by_publication'`).Scan(ctx, &restorationAudits))
				assert.Zero(t, restorationAudits, failedStep)
				_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
				assert.ErrorIs(t, err, ErrNoPublication, failedStep)
			}
			fixture.service.failPublicationStep = nil
		})
	}
}

func TestWithdrawalCannotBeToggledAndRestoresEveryTargetOnlyThroughFreshReviewedPublication(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    WithdrawalTargetKind
		target  func(publicationFixture) uuid.UUID
		reviews []int
	}{
		{name: "Event", kind: WithdrawalTargetEvent, target: func(f publicationFixture) uuid.UUID { return f.event }, reviews: []int{0, 1, 2}},
		{name: "Moment", kind: WithdrawalTargetMoment, target: func(f publicationFixture) uuid.UUID { return f.moments[0] }, reviews: []int{0}},
		{name: "Media", kind: WithdrawalTargetMedia, target: func(f publicationFixture) uuid.UUID { return f.media[0] }, reviews: []int{0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			targetID := test.target(fixture)
			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: targetID.String(), Reason: "Review access",
			})
			require.NoError(t, err)

			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: targetID.String(), Reason: "Try a toggle",
			})
			assert.ErrorIs(t, err, ErrAlreadyWithdrawn)
			_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
			assert.ErrorIs(t, err, ErrPublicationNotReady, "the older Audience must not silently restore access")

			for _, index := range test.reviews {
				freshSnapshot := uuid.New()
				_, err = fixture.db.NewRaw(`
					INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
					VALUES (?, 'moment', ?, ?, now(), 'Shared');
					INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
					SELECT ?, entry.recipient_person_id, entry.recipient_access_generation_id
					FROM audience_entries AS entry
					JOIN published_moments AS moment ON moment.id = entry.published_moment_id
					WHERE moment.publication_id = ? AND moment.draft_moment_id = ?;
					INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
					VALUES ('moment', ?, ?);
					UPDATE draft_moments SET audience_complete = true WHERE id = ?
				`, freshSnapshot, fixture.moments[index], fixture.actor.PersonID,
					freshSnapshot, first.ID, fixture.moments[index], fixture.moments[index],
					freshSnapshot, fixture.moments[index]).Exec(ctx)
				require.NoError(t, err)
			}
			_, err = fixture.db.NewRaw(`UPDATE events SET final_review_complete = true WHERE id = ?`, fixture.event).Exec(ctx)
			require.NoError(t, err)

			second, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
			require.NoError(t, err)
			assert.NotEqual(t, first.ID, second.ID)
			view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
			require.NoError(t, err)
			assert.Equal(t, second.ID, view.PublicationID)

			var restoredPublication uuid.UUID
			var restoredAt *string
			require.NoError(t, fixture.db.NewRaw(`SELECT restored_by_publication_id, restored_at::text FROM content_withdrawals WHERE target_kind = ? AND target_id = ?`, test.kind, targetID).Scan(ctx, &restoredPublication, &restoredAt))
			assert.Equal(t, uuid.MustParse(second.ID), restoredPublication)
			assert.NotNil(t, restoredAt)
			var restoredAudit int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE event_id = ? AND action = 'content_restored_by_publication'`, fixture.event).Scan(ctx, &restoredAudit))
			assert.Equal(t, 1, restoredAudit)
		})
	}
}

func TestWithdrawalInvalidatesAlreadyHandedOffOptionalDelivery(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	dispatched, err := outbox.New(fixture.db).Dispatch(ctx, "withdrawal-test", time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)

	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "media", TargetID: fixture.media[0].String(), Reason: "Stop queued delivery",
	})
	require.NoError(t, err)

	var status, diagnostic string
	require.NoError(t, fixture.db.NewRaw(`SELECT status, last_safe_error FROM jobs WHERE kind = 'publication_committed'`).Scan(ctx, &status, &diagnostic))
	assert.Equal(t, "failed", status)
	assert.Equal(t, "publication_withdrawn", diagnostic)
	err = fixture.service.HandlePublicationJob(ctx, worker.Job{Payload: []byte(`{"event_id":"` + fixture.event.String() + `","publication_id":"` + publication.ID + `"}`)})
	require.EqualError(t, err, "publication_withdrawn")
}

func TestMediaRestorationUsesTransactionalContentRevisions(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)

	secondEvent, secondMoment, secondSnapshot := uuid.New(), uuid.New(), uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO events (
			id, lifecycle, title, description, grouping_timezone, version,
			final_review_complete, created_at, updated_at
		) VALUES (?, 'draft', 'Second Event', '', 'UTC', 7, true, ?, ?);
		INSERT INTO draft_moments (
			id, event_id, position, proposed_day, grouping_timezone, source_days,
			title, cover_media_item_id, attendance_complete, audience_complete
		) VALUES (?, ?, 0, '2026-07-27', 'UTC', ARRAY['2026-07-27'::date], 'Shared Media', ?, true, true);
		INSERT INTO draft_media_placements (event_id, media_item_id, draft_moment_id, position, created_at)
		VALUES (?, ?, ?, 0, ?);
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?)
	`, secondEvent, fixture.service.now(), fixture.service.now(), secondMoment, secondEvent, fixture.media[0],
		secondEvent, fixture.media[0], secondMoment, fixture.service.now(), secondSnapshot, secondMoment,
		fixture.actor.PersonID, fixture.service.now(), secondSnapshot, fixture.people["shared"], fixture.access["shared"],
		secondMoment, secondSnapshot).Exec(ctx)
	require.NoError(t, err)
	second, err := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, fixture.request())
	require.NoError(t, err)
	assert.Equal(t, first.CommittedAt, second.CommittedAt, "the regression requires equal wall-clock timestamps")

	withdrawal, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "media", TargetID: fixture.media[0].String(), Reason: "Review every placement",
	})
	require.NoError(t, err)
	assert.Equal(t, first.CommittedAt, withdrawal.WithdrawnAt, "wall-clock equality must not imply freshness")

	review := func(eventID, momentID uuid.UUID) {
		snapshotID := uuid.New()
		_, reviewErr := fixture.db.NewRaw(`
			INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
			VALUES (?, 'moment', ?, ?, ?, 'Shared');
			INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
			VALUES (?, ?, ?);
			INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
			VALUES ('moment', ?, ?);
			UPDATE draft_moments SET audience_complete = true WHERE id = ?;
			UPDATE events SET final_review_complete = true WHERE id = ?
		`, snapshotID, momentID, fixture.actor.PersonID, fixture.service.now(), snapshotID,
			fixture.people["shared"], fixture.access["shared"], momentID, snapshotID, momentID, eventID).Exec(ctx)
		require.NoError(t, reviewErr)
	}
	review(fixture.event, fixture.moments[0])
	third, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var active bool
	require.NoError(t, fixture.db.NewRaw(`SELECT restored_at IS NULL FROM content_withdrawals WHERE id = ?`, withdrawal.ID).Scan(ctx, &active))
	assert.True(t, active, "one fresh placement Publication must not restore shared Media")
	_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	assert.ErrorIs(t, err, ErrNoPublication)

	review(secondEvent, secondMoment)
	fourth, err := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var revisions []int64
	require.NoError(t, fixture.db.NewRaw(`SELECT content_revision FROM publications WHERE id IN (?, ?, ?, ?) ORDER BY content_revision`, first.ID, second.ID, third.ID, fourth.ID).Scan(ctx, &revisions))
	require.Len(t, revisions, 4)
	assert.True(t, revisions[0] < revisions[1] && revisions[1] < revisions[2] && revisions[2] < revisions[3])
	require.NoError(t, fixture.db.NewRaw(`SELECT restored_at IS NULL FROM content_withdrawals WHERE id = ?`, withdrawal.ID).Scan(ctx, &active))
	assert.False(t, active)
	_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
}

func TestCuratorCanInspectWithdrawalHistoryAfterDraftIdentityRemoval(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "moment", TargetID: fixture.moments[0].String(), Reason: "Removed Moment",
	})
	require.NoError(t, err)
	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "media", TargetID: fixture.media[1].String(), Reason: "Removed Media",
	})
	require.NoError(t, err)

	_, err = fixture.db.NewRaw(`
		UPDATE draft_media_placements SET draft_moment_id = NULL WHERE draft_moment_id = ?;
		DELETE FROM draft_moments WHERE id = ?;
		DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?
	`, fixture.moments[0], fixture.moments[0], fixture.event, fixture.media[1]).Exec(ctx)
	require.NoError(t, err)

	event, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	require.Len(t, event.Withdrawals, 2)
	history := make(map[WithdrawalTargetKind]Withdrawal, len(event.Withdrawals))
	for _, item := range event.Withdrawals {
		history[item.TargetKind] = item
	}
	assert.Equal(t, fixture.moments[0].String(), history["moment"].TargetID)
	assert.Equal(t, "Removed Moment", history["moment"].Reason)
	assert.Equal(t, fixture.media[1].String(), history["media"].TargetID)
	assert.Equal(t, "Removed Media", history["media"].Reason)
}

func TestWithdrawalTargetsAndValidationUseTheCurrentPublicationInsteadOfTheStagedDraft(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   WithdrawalTargetKind
		target func(publicationFixture) uuid.UUID
		stage  func(context.Context, publicationFixture) error
	}{
		{
			name: "Moment removed from draft", kind: WithdrawalTargetMoment,
			target: func(f publicationFixture) uuid.UUID { return f.moments[0] },
			stage: func(ctx context.Context, f publicationFixture) error {
				_, err := f.db.NewRaw(`
					UPDATE draft_media_placements SET draft_moment_id = NULL WHERE draft_moment_id = ?;
					DELETE FROM draft_moments WHERE id = ?
				`, f.moments[0], f.moments[0]).Exec(ctx)
				return err
			},
		},
		{
			name: "Media removed from draft", kind: WithdrawalTargetMedia,
			target: func(f publicationFixture) uuid.UUID { return f.media[0] },
			stage: func(ctx context.Context, f publicationFixture) error {
				_, err := f.db.NewRaw(`DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?`, f.event, f.media[0]).Exec(ctx)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			require.NoError(t, test.stage(ctx, fixture))

			stagedMediaID := uuid.New()
			_, err = fixture.db.NewRaw(`
				INSERT INTO media_items (
					id, immich_asset_id, media_type, width, height, local_date_time,
					first_seen_at, last_seen_at
				) VALUES (?, ?, 'image', 1200, 800, '2026-07-30T10:00:00Z', now(), now());
				INSERT INTO draft_media_placements (
					event_id, media_item_id, draft_moment_id, position, created_at
				) VALUES (?, ?, ?, 99, now())
			`, stagedMediaID, uuid.New(), fixture.event, stagedMediaID, fixture.moments[1]).Exec(ctx)
			require.NoError(t, err)

			event, err := fixture.service.GetEvent(ctx, fixture.event)
			require.NoError(t, err)
			targets := make(map[string]WithdrawalTarget, len(event.WithdrawalTargets))
			for _, target := range event.WithdrawalTargets {
				targets[string(target.TargetKind)+":"+target.TargetID] = target
			}
			publishedKey := string(test.kind) + ":" + test.target(fixture).String()
			assert.Contains(t, targets, publishedKey, "a current Publication identity remains withdrawable after draft removal")
			assert.NotContains(t, targets, "media:"+stagedMediaID.String(), "staged unpublished Media must not be presented as published")
			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetMedia, TargetID: stagedMediaID.String(), Reason: "Not published yet",
			})
			assert.ErrorIs(t, err, ErrNotFound)

			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Withdraw published identity",
			})
			require.NoError(t, err)
			var finalReview bool
			require.NoError(t, fixture.db.NewRaw(`SELECT final_review_complete FROM events WHERE id = ?`, fixture.event).Scan(ctx, &finalReview))
			assert.False(t, finalReview)
		})
	}
}

func TestWithdrawalRejectsInvalidUnpublishedAndDuplicateTargets(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{TargetKind: "event", TargetID: fixture.event.String(), Reason: "Not published"})
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{TargetKind: "other", TargetID: fixture.event.String(), Reason: "Invalid"})
	assert.ErrorIs(t, err, ErrWithdrawalInvalid)
	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{TargetKind: "event", TargetID: fixture.event.String(), Reason: " "})
	assert.ErrorIs(t, err, ErrWithdrawalInvalid)
}
