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
	"github.com/robinjoseph08/memento/internal/placementlock"
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

type withdrawalMediaCall struct {
	representation string
	assetID        uuid.UUID
	request        immich.MediaRequest
}

type withdrawalMediaSource struct {
	mu             sync.Mutex
	calls          []withdrawalMediaCall
	openingStarted chan struct{}
	releaseOpening <-chan struct{}
	openingOnce    sync.Once
}

func (source *withdrawalMediaSource) Thumbnail(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return source.media(ctx, assetID, "thumbnail", request)
}

func (source *withdrawalMediaSource) Preview(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return source.media(ctx, assetID, "preview", request)
}

func (source *withdrawalMediaSource) Video(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return source.media(ctx, assetID, "video", request)
}

func (source *withdrawalMediaSource) Original(ctx context.Context, assetID uuid.UUID, request immich.MediaRequest) (immich.MediaResponse, error) {
	return source.media(ctx, assetID, "original", request)
}

func (source *withdrawalMediaSource) media(ctx context.Context, assetID uuid.UUID, representation string, request immich.MediaRequest) (immich.MediaResponse, error) {
	source.mu.Lock()
	source.calls = append(source.calls, withdrawalMediaCall{
		representation: representation, assetID: assetID, request: request,
	})
	source.mu.Unlock()
	if source.openingStarted != nil {
		source.openingOnce.Do(func() { close(source.openingStarted) })
	}
	if source.releaseOpening != nil {
		select {
		case <-source.releaseOpening:
		case <-ctx.Done():
			return immich.MediaResponse{}, ctx.Err()
		}
	}
	return immich.MediaResponse{
		Body: io.NopCloser(bytes.NewBufferString(representation)), ContentType: "application/octet-stream",
		ContentLength: int64(len(representation)),
	}, nil
}

func (source *withdrawalMediaSource) snapshotCalls() []withdrawalMediaCall {
	source.mu.Lock()
	defer source.mu.Unlock()
	return append([]withdrawalMediaCall(nil), source.calls...)
}

func (source *withdrawalMediaSource) callCount() int {
	return len(source.snapshotCalls())
}

func withdrawalRecipientHTTP(fixture publicationFixture, actor setup.SessionActor, source *withdrawalMediaSource) *echo.Echo {
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

func waitForAdvisoryLockWaiter(t *testing.T, fixture publicationFixture, lockKey, mode string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	lastWaiting := 0
	for {
		require.NoError(t, fixture.db.NewRaw(`WITH expected_lock AS (
			SELECT hashtextextended(?, 0) AS key
		)
		SELECT count(*) FROM pg_locks, expected_lock
		WHERE locktype = 'advisory'
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND classid::bigint = ((expected_lock.key >> 32) & 4294967295::bigint)
		  AND objid::bigint = (expected_lock.key & 4294967295::bigint)
		  AND objsubid = 1
		  AND mode = ? AND NOT granted`, lockKey, mode).Scan(context.Background(), &lastWaiting))
		if lastWaiting > 0 {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("transaction did not wait for the %s advisory lock with key %q; last waiting lock count: %d", mode, lockKey, lastWaiting)
		}
	}
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

func publishReusedMediaInSecondEvent(t *testing.T, fixture publicationFixture) (uuid.UUID, uuid.UUID, PublicationResponse) {
	t.Helper()
	ctx := context.Background()
	secondEvent, secondMoment, secondSnapshot := uuid.New(), uuid.New(), uuid.New()
	_, err := fixture.db.NewRaw(`
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
	publication, err := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, fixture.request())
	require.NoError(t, err)
	return secondEvent, secondMoment, publication
}

func reviewMomentForFreshPublication(t *testing.T, fixture publicationFixture, eventID, momentID uuid.UUID, priorPublicationID string) {
	t.Helper()
	snapshotID := uuid.New()
	_, err := fixture.db.NewRaw(`
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		SELECT ?, entry.recipient_person_id, entry.recipient_access_generation_id
		FROM audience_entries AS entry
		JOIN published_moments AS moment ON moment.id = entry.published_moment_id
		WHERE moment.publication_id = ? AND moment.draft_moment_id = ?;
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		UPDATE draft_moments SET audience_complete = true WHERE id = ?;
		UPDATE events SET final_review_complete = true WHERE id = ?
	`, snapshotID, momentID, fixture.actor.PersonID, fixture.service.now(), snapshotID,
		priorPublicationID, momentID, momentID, snapshotID, momentID, eventID).Exec(context.Background())
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

func TestAncestorWithdrawalHidesAndRejectsFullyCoveredNestedTargets(t *testing.T) {
	for _, test := range []struct {
		name      string
		kind      WithdrawalTargetKind
		target    func(publicationFixture) uuid.UUID
		nested    []func(publicationFixture) WithdrawRequest
		hidden    []func(publicationFixture) string
		remaining int
	}{
		{
			name: "Event", kind: WithdrawalTargetEvent,
			target: func(f publicationFixture) uuid.UUID { return f.event },
			nested: []func(publicationFixture) WithdrawRequest{
				func(f publicationFixture) WithdrawRequest {
					return WithdrawRequest{TargetKind: WithdrawalTargetMoment, TargetID: f.moments[0].String(), Reason: "Nested Moment"}
				},
				func(f publicationFixture) WithdrawRequest {
					return WithdrawRequest{TargetKind: WithdrawalTargetMedia, TargetID: f.media[0].String(), Reason: "Nested Media"}
				},
			},
			hidden: []func(publicationFixture) string{
				func(f publicationFixture) string { return "event:" + f.event.String() },
				func(f publicationFixture) string { return "moment:" + f.moments[0].String() },
				func(f publicationFixture) string { return "media:" + f.media[0].String() },
			},
		},
		{
			name: "Moment", kind: WithdrawalTargetMoment,
			target: func(f publicationFixture) uuid.UUID { return f.moments[0] },
			nested: []func(publicationFixture) WithdrawRequest{
				func(f publicationFixture) WithdrawRequest {
					return WithdrawRequest{TargetKind: WithdrawalTargetMedia, TargetID: f.media[0].String(), Reason: "Nested Media"}
				},
			},
			hidden: []func(publicationFixture) string{
				func(f publicationFixture) string { return "moment:" + f.moments[0].String() },
				func(f publicationFixture) string { return "media:" + f.media[0].String() },
			},
			remaining: 5,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Ancestor privacy request",
			})
			require.NoError(t, err)

			event, err := fixture.service.GetEvent(ctx, fixture.event)
			require.NoError(t, err)
			targets := make(map[string]struct{}, len(event.WithdrawalTargets))
			for _, target := range event.WithdrawalTargets {
				targets[string(target.TargetKind)+":"+target.TargetID] = struct{}{}
			}
			for _, hidden := range test.hidden {
				assert.NotContains(t, targets, hidden(fixture))
			}
			assert.Len(t, targets, test.remaining)

			tables := []string{"system_settings", "events", "draft_moments", "current_audience_snapshots", "content_withdrawals", "publication_audit_events"}
			prior := snapshotWithdrawalState(t, fixture, tables)
			for _, nested := range test.nested {
				_, err = fixture.service.Withdraw(ctx, fixture.actor, nested(fixture))
				assert.ErrorIs(t, err, ErrAlreadyWithdrawn)
				assert.Equal(t, prior, snapshotWithdrawalState(t, fixture, tables), "nested rejection must not write durable history or invalidate reviews")
			}
		})
	}
}

func TestMomentWithdrawalKeepsReusedMediaAvailableWhenAnotherPlacementIsVisible(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	secondEvent, _, _ := publishReusedMediaInSecondEvent(t, fixture)

	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetMoment, TargetID: fixture.moments[0].String(), Reason: "Withdraw one placement",
	})
	require.NoError(t, err)
	event, err := fixture.service.GetEvent(ctx, fixture.event)
	require.NoError(t, err)
	targets := make(map[string]struct{}, len(event.WithdrawalTargets))
	for _, target := range event.WithdrawalTargets {
		targets[string(target.TargetKind)+":"+target.TargetID] = struct{}{}
	}
	assert.NotContains(t, targets, "moment:"+fixture.moments[0].String())
	assert.Contains(t, targets, "media:"+fixture.media[0].String(), "reused Media remains withdrawable while another placement is visible")

	withdrawal, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetMedia, TargetID: fixture.media[0].String(), Reason: "Withdraw the remaining placement",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, withdrawal.AffectedEventCount)
	_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), secondEvent)
	assert.ErrorIs(t, err, ErrNoPublication)
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
			_, err := fixture.db.NewRaw(`UPDATE media_items SET media_type = 'video' WHERE id = ?`, fixture.media[0]).Exec(ctx)
			require.NoError(t, err)
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
			mediaSource := &withdrawalMediaSource{}
			recipientHTTP := withdrawalRecipientHTTP(fixture, recipientActor, mediaSource)
			var routes []string
			for _, route := range recipientHTTP.Routes() {
				if route.Method != echo.RouteNotFound {
					routes = append(routes, route.Method+" "+route.Path)
				}
			}
			assert.ElementsMatch(t, []string{
				"GET /api/me/photos", "GET /api/me/favorites", "GET /api/me/events",
				"GET /api/me/events/:id", "GET /api/me/new-for-you",
				"POST /api/me/new-for-you/:publication_id/seen",
				"GET /api/me/media/:id/thumbnail", "GET /api/me/media/:id/preview",
				"GET /api/me/media/:id/video", "GET /api/me/media/:id/original",
			}, routes, "the matrix must be updated whenever an implemented Recipient content surface changes")

			for _, path := range []string{
				"/api/me/photos", "/api/me/favorites", "/api/me/events",
				"/api/me/events/" + fixture.event.String(), "/api/me/new-for-you",
			} {
				assert.Equal(t, http.StatusOK, draftRequest(recipientHTTP, http.MethodGet, path, "").Code, "pre-open %s", path)
			}
			for _, representation := range []string{"thumbnail", "preview", "video", "original"} {
				path := "/api/me/media/" + fixture.media[0].String() + "/" + representation
				assert.Equal(t, http.StatusOK, draftRequest(recipientHTTP, http.MethodGet, path, "").Code, "pre-open %s", path)
			}
			require.Equal(t, 4, mediaSource.callCount())

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

			for _, representation := range []string{"thumbnail", "preview", "video", "original"} {
				opened := draftRequest(recipientHTTP, http.MethodGet,
					"/api/me/media/"+fixture.media[0].String()+"/"+representation, "")
				guessed := draftRequest(recipientHTTP, http.MethodGet,
					"/api/me/media/"+uuid.NewString()+"/"+representation, "")
				assert.Equal(t, http.StatusNotFound, opened.Code, representation)
				assert.Equal(t, guessed.Code, opened.Code, representation)
				assert.Equal(t, guessed.Body.String(), opened.Body.String(), representation)
			}
			assert.Equal(t, 4, mediaSource.callCount(), "denied and guessed representations must not reach Immich")
		})
	}
}

func TestRepresentationHandoffRevalidatesWithdrawalAfterSlowUpstreamOpening(t *testing.T) {
	type representationResult struct {
		response immich.MediaResponse
		err      error
	}
	representations := []struct {
		name string
		load func(context.Context, *library.Service, setup.SessionActor, uuid.UUID) (immich.MediaResponse, error)
	}{
		{name: "thumbnail", load: func(ctx context.Context, service *library.Service, actor setup.SessionActor, mediaID uuid.UUID) (immich.MediaResponse, error) {
			return service.Thumbnail(ctx, actor, mediaID, immich.MediaRequest{})
		}},
		{name: "preview", load: func(ctx context.Context, service *library.Service, actor setup.SessionActor, mediaID uuid.UUID) (immich.MediaResponse, error) {
			return service.Preview(ctx, actor, mediaID, immich.MediaRequest{})
		}},
		{name: "video", load: func(ctx context.Context, service *library.Service, actor setup.SessionActor, mediaID uuid.UUID) (immich.MediaResponse, error) {
			return service.Video(ctx, actor, mediaID, immich.MediaRequest{})
		}},
		{name: "original", load: func(ctx context.Context, service *library.Service, actor setup.SessionActor, mediaID uuid.UUID) (immich.MediaResponse, error) {
			return service.Original(ctx, actor, mediaID, immich.MediaRequest{})
		}},
	}
	prepare := func(t *testing.T) (publicationFixture, setup.SessionActor) {
		t.Helper()
		fixture := newPublicationFixture(t)
		ctx := context.Background()
		_, err := fixture.db.NewRaw(`UPDATE media_items SET media_type = 'video' WHERE id = ?`, fixture.media[0]).Exec(ctx)
		require.NoError(t, err)
		_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
		require.NoError(t, err)
		recipientActor := fixture.actorFor("shared")
		createWithdrawalRecipientSession(t, fixture, recipientActor)
		_, err = fixture.db.NewRaw(`
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
			SELECT gen_random_uuid(), id, immich_asset_id, now() FROM media_items WHERE id = ?
		`, fixture.media[0]).Exec(ctx)
		require.NoError(t, err)
		return fixture, recipientActor
	}

	for _, representation := range representations {
		t.Run(representation.name+" opening first", func(t *testing.T) {
			fixture, recipientActor := prepare(t)
			ctx := context.Background()
			openingStarted := make(chan struct{})
			releaseOpening := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(releaseOpening) })
			source := &withdrawalMediaSource{
				openingStarted: openingStarted,
				releaseOpening: releaseOpening,
			}
			service := library.New(fixture.db, source)
			opened := make(chan representationResult, 1)
			go func() {
				response, openErr := representation.load(ctx, service, recipientActor, fixture.media[0])
				opened <- representationResult{response: response, err: openErr}
			}()
			select {
			case <-openingStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not start opening")
			}

			_, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
				TargetKind: WithdrawalTargetMedia,
				TargetID:   fixture.media[0].String(),
				Reason:     "Commit during upstream opening",
			})
			require.NoError(t, err, "slow upstream opening must not block Withdrawal")
			releaseOnce.Do(func() { close(releaseOpening) })

			select {
			case result := <-opened:
				assert.ErrorIs(t, result.err, library.ErrNotFound)
				assert.Nil(t, result.response.Body)
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not revalidate after Withdrawal committed")
			}
			assert.Equal(t, 1, source.callCount(), "the unreturned upstream body was opened then denied")
		})

		t.Run(representation.name+" Withdrawal first", func(t *testing.T) {
			fixture, recipientActor := prepare(t)
			ctx := context.Background()
			withdrawalLocked := make(chan struct{})
			releaseWithdrawal := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(releaseWithdrawal) })
			fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
				if step == WithdrawalStepLocked {
					close(withdrawalLocked)
					<-releaseWithdrawal
				}
				return nil
			}
			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: WithdrawalTargetMedia,
					TargetID:   fixture.media[0].String(),
					Reason:     "Commit before stream opening",
				})
				withdrawn <- withdrawErr
			}()
			select {
			case <-withdrawalLocked:
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not acquire its placement snapshot")
			}

			openingStarted := make(chan struct{})
			releaseOpening := make(chan struct{})
			var openingOnce sync.Once
			defer openingOnce.Do(func() { close(releaseOpening) })
			source := &withdrawalMediaSource{openingStarted: openingStarted, releaseOpening: releaseOpening}
			service := library.New(fixture.db, source)
			opened := make(chan representationResult, 1)
			go func() {
				response, openErr := representation.load(ctx, service, recipientActor, fixture.media[0])
				opened <- representationResult{response: response, err: openErr}
			}()
			select {
			case <-openingStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not begin upstream opening")
			}
			assert.Equal(t, 1, source.callCount())
			releaseOnce.Do(func() { close(releaseWithdrawal) })

			select {
			case withdrawErr := <-withdrawn:
				require.NoError(t, withdrawErr)
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not commit after release")
			}
			openingOnce.Do(func() { close(releaseOpening) })
			select {
			case result := <-opened:
				assert.ErrorIs(t, result.err, library.ErrNotFound)
			case <-time.After(5 * time.Second):
				t.Fatal("representation did not revalidate after Withdrawal committed")
			}
			fixture.service.failWithdrawalStep = nil
			assert.Equal(t, 1, source.callCount(), "no response is handed off after Withdrawal commits")
		})
	}
}

func TestSlowRepresentationOpeningsDoNotStarveWithdrawalAtMinimumPoolSize(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	recipientActor := fixture.actorFor("shared")
	createWithdrawalRecipientSession(t, fixture, recipientActor)
	_, err = fixture.db.NewRaw(`
		INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
		SELECT gen_random_uuid(), id, immich_asset_id, now() FROM media_items WHERE id = ?
	`, fixture.media[0]).Exec(ctx)
	require.NoError(t, err)
	fixture.db.SetMaxOpenConns(2)
	fixture.db.SetMaxIdleConns(2)

	releaseOpening := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseOpening) })
	results := make(chan error, 2)
	for range 2 {
		openingStarted := make(chan struct{})
		source := &withdrawalMediaSource{openingStarted: openingStarted, releaseOpening: releaseOpening}
		service := library.New(fixture.db, source)
		go func() {
			response, openErr := service.Thumbnail(ctx, recipientActor, fixture.media[0], immich.MediaRequest{})
			if response.Body != nil {
				_ = response.Body.Close()
			}
			results <- openErr
		}()
		select {
		case <-openingStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("slow upstream opening did not start")
		}
	}

	withdrawCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = fixture.service.Withdraw(withdrawCtx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetMedia, TargetID: fixture.media[0].String(),
		Reason: "Withdrawal must retain a database connection",
	})
	require.NoError(t, err, "two slow requests must not retain the two supported pooled connections")
	releaseOnce.Do(func() { close(releaseOpening) })
	for range 2 {
		select {
		case openErr := <-results:
			assert.ErrorIs(t, openErr, library.ErrNotFound)
		case <-time.After(5 * time.Second):
			t.Fatal("representation did not revalidate the committed Withdrawal")
		}
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

			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Concurrent removal",
				})
				withdrawn <- withdrawErr
			}()
			waitForAdvisoryLockWaiter(t, fixture, placementlock.Key, "ExclusiveLock")
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

func TestConcurrentMediaPlacementGrowthWaitsForWithdrawalSnapshot(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	_, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
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

	withdrawalLocked := make(chan struct{})
	releaseWithdrawal := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseWithdrawal) })
	fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
		if step == WithdrawalStepLocked {
			close(withdrawalLocked)
			<-releaseWithdrawal
		}
		return nil
	}
	type withdrawalResult struct {
		withdrawal Withdrawal
		err        error
	}
	withdrawn := make(chan withdrawalResult, 1)
	go func() {
		withdrawal, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
			TargetKind: WithdrawalTargetMedia, TargetID: fixture.media[0].String(), Reason: "Stable placement snapshot",
		})
		withdrawn <- withdrawalResult{withdrawal: withdrawal, err: withdrawErr}
	}()
	select {
	case <-withdrawalLocked:
	case <-time.After(5 * time.Second):
		t.Fatal("Withdrawal did not finish locked placement revalidation")
	}

	published := make(chan error, 1)
	go func() {
		_, publishErr := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, fixture.request())
		published <- publishErr
	}()

	waitForAdvisoryLockWaiter(t, fixture, placementlock.Key, "ShareLock")

	releaseOnce.Do(func() { close(releaseWithdrawal) })
	var result withdrawalResult
	select {
	case result = <-withdrawn:
		require.NoError(t, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("Withdrawal did not commit after release")
	}
	select {
	case publishErr := <-published:
		assert.ErrorIs(t, publishErr, ErrPublicationNotReady, "the waiting Publication must observe Withdrawal review invalidation")
	case <-time.After(5 * time.Second):
		t.Fatal("Publication did not resume after Withdrawal committed")
	}
	fixture.service.failWithdrawalStep = nil

	var secondCurrentPlacements int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_published_placements WHERE event_id = ?`, secondEvent).Scan(ctx, &secondCurrentPlacements))
	assert.Zero(t, secondCurrentPlacements, "the rejected concurrent Publication must not mutate current placements")
	freshSnapshot := uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, ?, 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		UPDATE draft_moments SET audience_complete = true WHERE id = ?
	`, freshSnapshot, secondMoment, fixture.actor.PersonID, fixture.service.now(), freshSnapshot,
		fixture.people["shared"], fixture.access["shared"], secondMoment, freshSnapshot, secondMoment).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, fixture.request())
	require.NoError(t, err)

	assert.Equal(t, 1, result.withdrawal.AffectedEventCount)
	var placementEvents, firstAudits, secondAudits int
	var active bool
	require.NoError(t, fixture.db.NewRaw(`SELECT
		(SELECT count(DISTINCT event_id) FROM current_published_placements WHERE media_item_id = ?),
		(SELECT count(*) FROM publication_audit_events WHERE event_id = ? AND action = 'content_withdrawn'),
		(SELECT count(*) FROM publication_audit_events WHERE event_id = ? AND action = 'content_withdrawn'),
		(SELECT restored_at IS NULL FROM content_withdrawals
		 WHERE target_kind = 'media' AND target_id = ?)
	`, fixture.media[0], fixture.event, secondEvent, fixture.media[0]).Scan(ctx,
		&placementEvents, &firstAudits, &secondAudits, &active))
	assert.Equal(t, 2, placementEvents, "the waiting Publication may add the placement after Withdrawal commits")
	assert.Equal(t, 1, firstAudits)
	assert.Zero(t, secondAudits, "Withdrawal audit scope must match its locked placement snapshot")
	assert.True(t, active, "a later placement must remain covered by the active Media Withdrawal")
	_, err = fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), secondEvent)
	assert.ErrorIs(t, err, ErrNoPublication)
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

func TestWithdrawalAndRunningOptionalDeliveryHaveOneExternalHandoffOrder(t *testing.T) {
	jobFor := func(fixture publicationFixture, publication PublicationResponse) worker.Job {
		return worker.Job{Payload: []byte(`{"event_id":"` + fixture.event.String() + `","publication_id":"` + publication.ID + `","notify_recipients":true}`)}
	}
	for _, test := range withdrawalTestCases() {
		t.Run(test.name+" handoff first", func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			handoffStarted := make(chan struct{})
			releaseHandoff := make(chan struct{})
			fixture.service.SetPublicationHandoff(func(context.Context, uuid.UUID, uuid.UUID) error {
				close(handoffStarted)
				<-releaseHandoff
				return nil
			})
			handled := make(chan error, 1)
			go func() { handled <- fixture.service.HandlePublicationJob(ctx, jobFor(fixture, publication)) }()
			select {
			case <-handoffStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("optional delivery did not reach its external handoff")
			}

			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Stop running delivery",
				})
				withdrawn <- withdrawErr
			}()
			waitForAdvisoryLockWaiter(t, fixture, placementlock.Key, "ExclusiveLock")
			select {
			case err := <-withdrawn:
				t.Fatalf("Withdrawal committed during external handoff: %v", err)
			default:
			}
			close(releaseHandoff)
			select {
			case err := <-handled:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("optional delivery did not finish")
			}
			select {
			case err := <-withdrawn:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not commit after external handoff")
			}
		})

		t.Run(test.name+" Withdrawal first", func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
			withdrawalLocked := make(chan struct{})
			releaseWithdrawal := make(chan struct{})
			fixture.service.failWithdrawalStep = func(step WithdrawalStep) error {
				if step == WithdrawalStepLocked {
					close(withdrawalLocked)
					<-releaseWithdrawal
				}
				return nil
			}
			handoffStarted := make(chan struct{}, 1)
			fixture.service.SetPublicationHandoff(func(context.Context, uuid.UUID, uuid.UUID) error {
				handoffStarted <- struct{}{}
				return nil
			})
			withdrawn := make(chan error, 1)
			go func() {
				_, withdrawErr := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
					TargetKind: test.kind, TargetID: test.target(fixture).String(), Reason: "Commit before delivery",
				})
				withdrawn <- withdrawErr
			}()
			select {
			case <-withdrawalLocked:
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not acquire its authorization lock")
			}
			handled := make(chan error, 1)
			go func() { handled <- fixture.service.HandlePublicationJob(ctx, jobFor(fixture, publication)) }()
			waitForAdvisoryLockWaiter(t, fixture, placementlock.Key, "ShareLock")
			select {
			case <-handoffStarted:
				t.Fatal("external send began before Withdrawal committed")
			default:
			}
			close(releaseWithdrawal)
			select {
			case err := <-withdrawn:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("Withdrawal did not commit")
			}
			select {
			case err := <-handled:
				require.EqualError(t, err, "publication_withdrawn")
			case <-time.After(5 * time.Second):
				t.Fatal("optional delivery did not revalidate after Withdrawal")
			}
			select {
			case <-handoffStarted:
				t.Fatal("external send began after Withdrawal committed")
			default:
			}
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

func TestMediaRestoresWhenTheFinalFreshPublicationRemovesItsStalePlacement(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	secondEvent, secondMoment, second := publishReusedMediaInSecondEvent(t, fixture)

	_, err = fixture.db.NewRaw(`
		DELETE FROM draft_media_placements WHERE event_id = ? AND media_item_id = ?;
		UPDATE draft_moments SET cover_media_item_id = NULL WHERE id = ?
	`, secondEvent, fixture.media[0], secondMoment).Exec(ctx)
	require.NoError(t, err)
	withdrawal, err := fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: WithdrawalTargetMedia, TargetID: fixture.media[0].String(), Reason: "Review every current placement",
	})
	require.NoError(t, err)
	var secondAudienceComplete bool
	require.NoError(t, fixture.db.NewRaw(`SELECT audience_complete FROM draft_moments WHERE id = ?`, secondMoment).Scan(ctx, &secondAudienceComplete))
	assert.False(t, secondAudienceComplete, "Withdrawal must invalidate the published placement review after Media was staged out")

	reviewMomentForFreshPublication(t, fixture, fixture.event, fixture.moments[0], first.ID)
	freshRetained, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var active bool
	require.NoError(t, fixture.db.NewRaw(`SELECT restored_at IS NULL FROM content_withdrawals WHERE id = ?`, withdrawal.ID).Scan(ctx, &active))
	assert.True(t, active, "the stale second placement must continue to hold Withdrawal active")

	_, err = fixture.db.NewRaw(`UPDATE events SET final_review_complete = true WHERE id = ?`, secondEvent).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, PublishEventRequest{Version: 8})
	assert.ErrorIs(t, err, ErrPublicationNotReady, "staging Media removal before Withdrawal must not preserve the older Audience review")

	reviewMomentForFreshPublication(t, fixture, secondEvent, secondMoment, second.ID)
	finalPublication, err := fixture.service.PublishEvent(ctx, fixture.actor, secondEvent, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	var restoredBy uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT restored_by_publication_id, restored_at IS NULL
		FROM content_withdrawals WHERE id = ?`, withdrawal.ID).Scan(ctx, &restoredBy, &active))
	assert.False(t, active)
	assert.Equal(t, uuid.MustParse(finalPublication.ID), restoredBy,
		"the final superseding Publication restores Media even when it removes its own placement")

	view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, freshRetained.ID, view.PublicationID)
	require.Len(t, view.Media, 1)
	assert.Equal(t, fixture.media[0].String(), view.Media[0].ID)
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
