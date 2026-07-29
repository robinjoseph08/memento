//go:build integration

package emaildelivery

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func (fixture immediateFixture) useWeekly(t *testing.T, day, localTime, timezone string) weeklySchedule {
	t.Helper()
	schedule, err := parseWeeklySchedule(day, localTime, timezone)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE notification_preferences
		SET email_preference = 'weekly', weekly_day = ?, weekly_local_time = ?, weekly_timezone = ?,
		    weekly_schedule_overridden = true
		WHERE recipient_access_generation_id = ?`, day, localTime, timezone, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	return schedule
}

func (fixture immediateFixture) leasedRequiredJob(t *testing.T, deliveryID int64) worker.Job {
	t.Helper()
	payload := []byte(`{"delivery_id":` + fmt.Sprint(deliveryID) + `}`)
	var id int64
	require.NoError(t, fixture.db.NewRaw(`INSERT INTO jobs
		(kind, payload, status, lease_owner, lease_expires_at)
		VALUES (?, ?::jsonb, 'running', 'required-test', clock_timestamp() + interval '1 hour') RETURNING id`,
		JobKind, string(payload)).Scan(context.Background(), &id))
	return worker.Job{ID: id, Kind: JobKind, Payload: payload, LeaseOwner: "required-test"}
}

func (fixture immediateFixture) leasedWeeklyJob(t *testing.T, batchID int64) worker.Job {
	t.Helper()
	payload := []byte(`{"batch_id":` + fmt.Sprint(batchID) + `}`)
	var id int64
	require.NoError(t, fixture.db.NewRaw(`INSERT INTO jobs
		(kind, payload, status, lease_owner, lease_expires_at)
		VALUES (?, ?::jsonb, 'running', 'weekly-test', clock_timestamp() + interval '1 hour') RETURNING id`,
		WeeklyJobKind, string(payload)).Scan(context.Background(), &id))
	return worker.Job{ID: id, Kind: WeeklyJobKind, Payload: payload, LeaseOwner: "weekly-test"}
}

func TestPlatformTimezoneSchedulesRecipientsWithoutPersonalOverrides(t *testing.T) {
	fixture := newImmediateFixture(t)
	_, err := fixture.db.NewRaw(`UPDATE notification_preferences
		SET email_preference = 'weekly' WHERE recipient_access_generation_id = ?`, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.UpdatePlatformWeeklyTimezone(context.Background(), "America/New_York")
	require.NoError(t, err)
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))

	var batchID int64
	var closesAt time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT id, closes_at FROM notification_batches`).
		Scan(context.Background(), &batchID, &closesAt))
	schedule, err := parseWeeklySchedule("sunday", "09:00", "America/New_York")
	require.NoError(t, err)
	assert.Equal(t, schedule.Next(fixture.base), closesAt)

	_, err = fixture.service.UpdatePlatformWeeklyTimezone(context.Background(), "Europe/London")
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))
	require.Len(t, fixture.sender.sent(), 1, "already queued activity remains on its original schedule")

	commentID := fixture.addComment(t, fixture.base.Add(time.Minute), "Uses the new household timezone")
	fixture.queueComment(t, commentID)
	var nextDue time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT closes_at FROM notification_batches WHERE status = 'pending'`).
		Scan(context.Background(), &nextDue))
	london, err := parseWeeklySchedule("sunday", "09:00", "Europe/London")
	require.NoError(t, err)
	assert.Equal(t, london.Next(fixture.base), nextDue)
}

func TestDelayedWeeklyHandoffUsesTheNextFutureSchedule(t *testing.T) {
	fixture := newImmediateFixture(t)
	schedule := fixture.useWeekly(t, "sunday", "09:00", "UTC")
	firstBoundary := schedule.Next(fixture.base)
	fixture.service.now = func() time.Time { return firstBoundary.Add(time.Minute) }
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))

	var closesAt time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT closes_at FROM notification_batches`).Scan(context.Background(), &closesAt))
	assert.Equal(t, schedule.Next(firstBoundary.Add(time.Minute)), closesAt)
	assert.True(t, closesAt.After(fixture.service.now()))
}

func TestWeeklyDigestUsesLocalBoundaryAndThreeSafeAuthorizedPreviews(t *testing.T) {
	fixture := newImmediateFixture(t)
	schedule := fixture.useWeekly(t, "sunday", "09:00", "America/New_York")
	fixture.addLargePublicationActivity(t, 4)
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))

	var batchID int64
	var cadence string
	var closesAt time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT id, cadence, closes_at FROM notification_batches`).
		Scan(context.Background(), &batchID, &cadence, &closesAt))
	assert.Equal(t, "weekly", cadence)
	assert.Equal(t, schedule.Next(fixture.base), closesAt)
	var jobKind string
	require.NoError(t, fixture.db.NewRaw(`SELECT kind FROM outbox_events WHERE aggregate_kind = 'notification_batch'`).
		Scan(context.Background(), &jobKind))
	assert.Equal(t, WeeklyJobKind, jobKind)

	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	message := messages[0]
	assert.Equal(t, "Your weekly Memento digest", message.Subject)
	require.Len(t, message.EmbeddedImages, 3)
	assert.Nil(t, message.Embedded)
	assert.NotContains(t, strings.ToLower(message.Body), "immich")
	assert.NotContains(t, message.Body, "/api/media/")
	for _, preview := range message.EmbeddedImages {
		assert.NotContains(t, string(preview.Data), "private-gps-metadata")
		decoded, err := jpeg.Decode(bytes.NewReader(preview.Data))
		require.NoError(t, err)
		assert.LessOrEqual(t, decoded.Bounds().Dx(), maxPreviewPixels)
		assert.LessOrEqual(t, decoded.Bounds().Dy(), maxPreviewPixels)
	}
}

func TestSignedPreferenceFlowUpdatesScheduleOnceWithoutChangingIdentity(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))

	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	parsed, err := url.Parse(messages[0].UnsubscribeURL)
	require.NoError(t, err)
	token := parsed.Query().Get("token")
	require.NotEmpty(t, token)
	require.NoError(t, fixture.service.UpdatePreferenceToken(context.Background(), token, PreferenceUpdate{
		EmailPreference: "weekly", WeeklyDay: "wednesday", WeeklyLocalTime: "18:45", WeeklyTimezone: "Europe/London",
	}))
	assert.ErrorIs(t, fixture.service.UpdatePreferenceToken(context.Background(), token, PreferenceUpdate{
		EmailPreference: "none", WeeklyDay: "wednesday", WeeklyLocalTime: "18:45", WeeklyTimezone: "Europe/London",
	}), ErrUnsubscribeToken)

	preference, err := fixture.service.PreferenceFor(context.Background(), fixture.access["alex"])
	require.NoError(t, err)
	assert.Equal(t, Preference{EmailPreference: "weekly", WeeklyDay: "wednesday", WeeklyLocalTime: "18:45", WeeklyTimezone: "Europe/London"}, preference)
	var email string
	require.NoError(t, fixture.db.NewRaw(`SELECT email FROM recipient_emails
		WHERE recipient_access_generation_id = ? AND is_current`, fixture.access["alex"]).Scan(context.Background(), &email))
	assert.Equal(t, "alex@example.com", email)
}

func TestWeeklyDigestRetriesTemporaryFailuresAndDisablesOnlyOptionalEmailOnRejection(t *testing.T) {
	t.Run("temporary", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.useWeekly(t, "sunday", "09:00", "UTC")
		fixture.sender.results = []error{&smtp.DeliveryError{Diagnostic: "smtp_unavailable", Temporary: true}}
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
		err := fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID))
		require.Error(t, err)
		var status string
		var attempts int
		require.NoError(t, fixture.db.NewRaw(`SELECT status, attempts FROM notification_batches WHERE id = ?`, batchID).
			Scan(context.Background(), &status, &attempts))
		assert.Equal(t, "pending", status)
		assert.Equal(t, 1, attempts)
	})

	t.Run("permanent recipient rejection", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.useWeekly(t, "sunday", "09:00", "UTC")
		fixture.sender.results = []error{&smtp.DeliveryError{Diagnostic: "recipient_rejected", Temporary: false}}
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
		err := fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "recipient_rejected")
		preference, preferenceErr := fixture.service.PreferenceFor(context.Background(), fixture.access["alex"])
		require.NoError(t, preferenceErr)
		assert.Equal(t, "none", preference.EmailPreference)

		var requiredID int64
		require.NoError(t, fixture.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			requiredID, _, err = fixture.service.QueueRequired(ctx, tx, RequiredMessage{
				Kind: "required_test", Recipient: "alex@example.com", Subject: "Required security email", Body: "Required email remains available.",
			})
			return err
		}))
		require.NoError(t, fixture.service.Handle(context.Background(), fixture.leasedRequiredJob(t, requiredID)))
		messages := fixture.sender.sent()
		require.Len(t, messages, 2)
		assert.Equal(t, "Required security email", messages[1].Subject)

		var problems int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM delivery_problems WHERE notification_batch_id = ?`, batchID).
			Scan(context.Background(), &problems))
		assert.Equal(t, 1, problems)
	})

	t.Run("non-recipient permanent failure", func(t *testing.T) {
		fixture := newImmediateFixture(t)
		fixture.useWeekly(t, "sunday", "09:00", "UTC")
		fixture.sender.results = []error{&smtp.DeliveryError{Diagnostic: "tls_verification_failed", Temporary: false}}
		fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
		require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
		var batchID int64
		require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
		err := fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID))
		require.Error(t, err)
		preference, preferenceErr := fixture.service.PreferenceFor(context.Background(), fixture.access["alex"])
		require.NoError(t, preferenceErr)
		assert.Equal(t, "weekly", preference.EmailPreference, "infrastructure failures cannot unsubscribe a Recipient")
	})
}

func TestWeeklyDigestStopsAtTheTwentyFourHourRetryWindow(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.service.cfg.RetryWindow = 24 * time.Hour
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
	_, err := fixture.db.NewRaw(`UPDATE notification_batches
		SET window_started_at = clock_timestamp() - interval '8 days',
		    closes_at = clock_timestamp() - interval '24 hours 1 second'
		WHERE id = ?`, batchID).Exec(context.Background())
	require.NoError(t, err)

	err = fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry_window_exhausted")
	assert.Empty(t, fixture.sender.sent())
}

func TestWeeklyPreviewMediaStayLockedThroughSMTPAcceptance(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addLargePublicationActivity(t, maxEmailPublicationMedia+10)
	blocking := &blockingImmediateSender{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-blocking.release:
		default:
			close(blocking.release)
		}
	})
	fixture.service.sender = blocking
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	delivered := make(chan error, 1)
	go func() {
		delivered <- fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID))
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("weekly sender was not reached")
	}
	requested := fixture.preview.requests()
	require.NotEmpty(t, requested)
	var previewMediaID uuid.UUID
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM media_items WHERE immich_asset_id = ?`, requested[0]).
		Scan(context.Background(), &previewMediaID))
	relinked := make(chan error, 1)
	go func() {
		_, err := fixture.db.NewRaw(`UPDATE media_items SET immich_asset_id = ? WHERE id = ?`, uuid.New(), previewMediaID).
			Exec(context.Background())
		relinked <- err
	}()
	waitForEmailBatchLock(t, fixture.db, `%UPDATE media_items SET immich_asset_id%`)

	close(blocking.release)
	require.NoError(t, <-delivered)
	require.NoError(t, <-relinked)
	require.Len(t, blocking.acceptedMessages(), 1)
}

func TestWeeklyPreviewIsDroppedWhenMediaRelinksBeforeSMTP(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
	newAssetID := uuid.New()
	fixture.service.beforeOptionalSend = func() {
		_, err := fixture.db.NewRaw(`UPDATE media_items SET immich_asset_id = ? WHERE id = ?`, newAssetID, fixture.media[0]).Exec(context.Background())
		require.NoError(t, err)
	}

	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))
	messages := fixture.sender.sent()
	require.Len(t, messages, 1)
	assert.Empty(t, messages[0].EmbeddedImages, "bytes loaded from an earlier backing cannot survive final authorization")
}

func TestWeeklyRetryBatchSendsNewActivityAtTheNextSchedule(t *testing.T) {
	fixture := newImmediateFixture(t)
	schedule := fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var firstBatchID int64
	var firstDue time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT id, closes_at FROM notification_batches`).
		Scan(context.Background(), &firstBatchID, &firstDue))
	_, err := fixture.db.NewRaw(`UPDATE notification_batches SET attempts = 1 WHERE id = ?`, firstBatchID).Exec(context.Background())
	require.NoError(t, err)
	commentID := fixture.addComment(t, fixture.base.Add(time.Minute), "After attempted digest")
	fixture.queueComment(t, commentID)

	var due []time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT closes_at FROM notification_batches ORDER BY closes_at`).Scan(context.Background(), &due))
	require.Len(t, due, 2)
	assert.Equal(t, firstDue, due[0])
	assert.Equal(t, schedule.Next(firstDue), due[1])
}

func TestAcceptedWeeklySMTPDoesNotClaimAsynchronousBounceDetection(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))
	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))

	preference, err := fixture.service.PreferenceFor(context.Background(), fixture.access["alex"])
	require.NoError(t, err)
	assert.Equal(t, "weekly", preference.EmailPreference)
	var problems int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM delivery_problems WHERE notification_batch_id = ?`, batchID).
		Scan(context.Background(), &problems))
	assert.Zero(t, problems, "generic SMTP records only the synchronous acceptance result and has no later bounce detector")
}

func TestRestoringWeeklyEmailDoesNotRevivePendingActivity(t *testing.T) {
	fixture := newImmediateFixture(t)
	fixture.useWeekly(t, "sunday", "09:00", "UTC")
	fixture.addPublicationActivity(t, fixture.base, fixture.media[0])
	require.NoError(t, fixture.service.QueuePublication(context.Background(), fixture.event, fixture.publication))
	var batchID int64
	require.NoError(t, fixture.db.NewRaw(`SELECT id FROM notification_batches`).Scan(context.Background(), &batchID))

	_, err := fixture.service.UpdatePreference(context.Background(), fixture.access["alex"], PreferenceUpdate{
		EmailPreference: "none", WeeklyDay: "sunday", WeeklyLocalTime: "09:00", WeeklyTimezone: "UTC",
	})
	require.NoError(t, err)
	_, err = fixture.service.UpdatePreference(context.Background(), fixture.access["alex"], PreferenceUpdate{
		EmailPreference: "weekly", WeeklyDay: "sunday", WeeklyLocalTime: "09:00", WeeklyTimezone: "UTC",
	})
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleWeekly(context.Background(), fixture.leasedWeeklyJob(t, batchID)))

	assert.Empty(t, fixture.sender.sent(), "restoring delivery cannot revive activity from an earlier preference version")
	var status string
	require.NoError(t, fixture.db.NewRaw(`SELECT status FROM notification_batches WHERE id = ?`, batchID).
		Scan(context.Background(), &status))
	assert.Equal(t, "suppressed", status)
}
