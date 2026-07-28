//go:build integration

package events

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithdrawalImmediatelyDeniesOnlyThePublishedTargetAndPreservesHistory(t *testing.T) {
	for _, test := range []struct {
		name               string
		kind               string
		target             func(publicationFixture) uuid.UUID
		expectedRecipients int
		invalidatedReview  int
		sharedDenied       bool
		hiddenDenied       bool
	}{
		{name: "Event", kind: "event", target: func(f publicationFixture) uuid.UUID { return f.event }, expectedRecipients: 2, invalidatedReview: 3, sharedDenied: true, hiddenDenied: true},
		{name: "Moment", kind: "moment", target: func(f publicationFixture) uuid.UUID { return f.moments[0] }, expectedRecipients: 1, invalidatedReview: 1, sharedDenied: true},
		{name: "Media", kind: "media", target: func(f publicationFixture) uuid.UUID { return f.media[0] }, expectedRecipients: 1, invalidatedReview: 1, sharedDenied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			ctx := context.Background()
			publication, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
			require.NoError(t, err)
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

			var incomplete, snapshots, publications, audienceEntries, favorites, outboxAfter int
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_moments WHERE event_id = ? AND NOT audience_complete`, fixture.event).Scan(ctx, &incomplete))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM current_audience_snapshots AS current JOIN draft_moments AS moment ON moment.id = current.target_id WHERE current.target_kind = 'moment' AND moment.event_id = ?`, fixture.event).Scan(ctx, &snapshots))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publications WHERE event_id = ?`, fixture.event).Scan(ctx, &publications))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM audience_entries AS audience JOIN published_moments AS moment ON moment.id = audience.published_moment_id WHERE moment.publication_id = ?`, publication.ID).Scan(ctx, &audienceEntries))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM favorites WHERE recipient_person_id = ? AND media_item_id = ?`, fixture.people["shared"], fixture.media[0]).Scan(ctx, &favorites))
			require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(ctx, &outboxAfter))
			assert.Equal(t, test.invalidatedReview, incomplete)
			assert.Equal(t, len(fixture.moments)-test.invalidatedReview, snapshots)
			assert.Equal(t, 1, publications)
			assert.Equal(t, 3, audienceEntries)
			assert.Equal(t, 1, favorites)
			assert.Equal(t, outboxBefore, outboxAfter, "Withdrawal must not enqueue external notification work")

			var auditAction, auditReason string
			require.NoError(t, fixture.db.NewRaw(`SELECT action, metadata->>'reason' FROM publication_audit_events WHERE event_id = ? AND action = 'content_withdrawn'`, fixture.event).Scan(ctx, &auditAction, &auditReason))
			assert.Equal(t, "content_withdrawn", auditAction)
			assert.Equal(t, "Requested by the family", auditReason)
		})
	}
}

func TestWithdrawalCannotBeToggledAndRestoresOnlyThroughFreshReviewedPublication(t *testing.T) {
	fixture := newPublicationFixture(t)
	ctx := context.Background()
	first, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, fixture.request())
	require.NoError(t, err)
	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "moment", TargetID: fixture.moments[0].String(), Reason: "Review access",
	})
	require.NoError(t, err)

	_, err = fixture.service.Withdraw(ctx, fixture.actor, WithdrawRequest{
		TargetKind: "moment", TargetID: fixture.moments[0].String(), Reason: "Try a toggle",
	})
	assert.ErrorIs(t, err, ErrAlreadyWithdrawn)
	_, err = fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	assert.ErrorIs(t, err, ErrPublicationNotReady, "the older Audience must not silently restore access")

	freshSnapshot := uuid.New()
	_, err = fixture.db.NewRaw(`
		INSERT INTO audience_snapshots (id, target_kind, target_id, approved_by_person_id, approved_at, label)
		VALUES (?, 'moment', ?, ?, now(), 'Shared');
		INSERT INTO audience_snapshot_entries (snapshot_id, recipient_person_id, recipient_access_generation_id)
		VALUES (?, ?, ?);
		INSERT INTO current_audience_snapshots (target_kind, target_id, snapshot_id)
		VALUES ('moment', ?, ?);
		UPDATE draft_moments SET audience_complete = true WHERE id = ?;
		UPDATE events SET final_review_complete = true WHERE id = ?
	`, freshSnapshot, fixture.moments[0], fixture.actor.PersonID,
		freshSnapshot, fixture.people["shared"], fixture.access["shared"],
		fixture.moments[0], freshSnapshot, fixture.moments[0], fixture.event).Exec(ctx)
	require.NoError(t, err)

	second, err := fixture.service.PublishEvent(ctx, fixture.actor, fixture.event, PublishEventRequest{Version: 8})
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID)
	view, err := fixture.service.RecipientEvent(ctx, fixture.actorFor("shared"), fixture.event)
	require.NoError(t, err)
	assert.Equal(t, second.ID, view.PublicationID)

	var restoredPublication uuid.UUID
	var restoredAt *string
	require.NoError(t, fixture.db.NewRaw(`SELECT restored_by_publication_id, restored_at::text FROM content_withdrawals WHERE target_kind = 'moment' AND target_id = ?`, fixture.moments[0]).Scan(ctx, &restoredPublication, &restoredAt))
	assert.Equal(t, uuid.MustParse(second.ID), restoredPublication)
	assert.NotNil(t, restoredAt)
	var restoredAudit int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM publication_audit_events WHERE event_id = ? AND action = 'content_restored_by_publication'`, fixture.event).Scan(ctx, &restoredAudit))
	assert.Equal(t, 1, restoredAudit)
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
