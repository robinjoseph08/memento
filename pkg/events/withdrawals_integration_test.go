//go:build integration

package events

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
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

func withdrawalRecipientHTTP(fixture publicationFixture, actor setup.SessionActor) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	library.RegisterRoutes(e, library.NewHandler(library.New(fixture.db, nil), withdrawalRecipientAuthorizer{
		actor: actor,
	}))
	return e
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
			recipientHTTP := withdrawalRecipientHTTP(fixture, recipientActor)
			openedPath := "/api/me/events/" + fixture.event.String()
			require.Equal(t, http.StatusOK, draftRequest(recipientHTTP, http.MethodGet, openedPath, "").Code)
			_, err = fixture.db.NewRaw(`INSERT INTO favorites (recipient_person_id, media_item_id) VALUES (?, ?)`, fixture.people["shared"], fixture.media[0]).Exec(ctx)
			require.NoError(t, err)
			var outboxBefore int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(ctx, &outboxBefore))

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

			var incomplete, snapshots, publications, audienceEntries, favorites, outboxAfter, pendingOutbox int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_moments WHERE event_id = ? AND NOT audience_complete`, fixture.event).Scan(ctx, &incomplete))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_audience_snapshots AS current JOIN draft_moments AS moment ON moment.id = current.target_id WHERE current.target_kind = 'moment' AND moment.event_id = ?`, fixture.event).Scan(ctx, &snapshots))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publications WHERE event_id = ?`, fixture.event).Scan(ctx, &publications))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_entries AS audience JOIN published_moments AS moment ON moment.id = audience.published_moment_id WHERE moment.publication_id = ?`, publication.ID).Scan(ctx, &audienceEntries))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM favorites WHERE recipient_person_id = ? AND media_item_id = ?`, fixture.people["shared"], fixture.media[0]).Scan(ctx, &favorites))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(ctx, &outboxAfter))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE delivered_at IS NULL`).Scan(ctx, &pendingOutbox))
			assert.Equal(t, test.invalidatedReview, incomplete)
			assert.Equal(t, len(fixture.moments)-test.invalidatedReview, snapshots)
			assert.Equal(t, 1, publications)
			assert.Equal(t, 3, audienceEntries)
			assert.Equal(t, 1, favorites)
			assert.Equal(t, outboxBefore, outboxAfter, "Withdrawal must preserve optional delivery history")
			assert.Zero(t, pendingOutbox, "Withdrawal must retire pending optional delivery")
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
