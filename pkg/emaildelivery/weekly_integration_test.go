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

	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (fixture immediateFixture) useWeekly(t *testing.T, day, localTime, timezone string) weeklySchedule {
	t.Helper()
	schedule, err := parseWeeklySchedule(day, localTime, timezone)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`UPDATE notification_preferences
		SET email_preference = 'weekly', weekly_day = ?, weekly_local_time = ?, weekly_timezone = ?
		WHERE recipient_access_generation_id = ?`, day, localTime, timezone, fixture.access["alex"]).Exec(context.Background())
	require.NoError(t, err)
	return schedule
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
		var problems int
		require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM delivery_problems WHERE notification_batch_id = ?`, batchID).
			Scan(context.Background(), &problems))
		assert.Equal(t, 1, problems)
	})
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
