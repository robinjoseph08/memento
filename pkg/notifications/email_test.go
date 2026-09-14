package notifications_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

const publicURL = "https://memento.example.test"

// mailModule wires a recording mailer and a direct queue so tests drive
// delivery execution themselves, the way the worker would.
func mailModule(t *testing.T, db *bun.DB, content notifications.VisibleContent) (*notifications.Module, *notifications.Recorder, *queue) {
	t.Helper()
	recorder := &notifications.Recorder{}
	jobs := &queue{}
	module := notifications.New(db, recorder, jobs.enqueue, content, nil)
	module.PublicURL = publicURL
	return module, recorder, jobs
}

func approveOne(t *testing.T, module *notifications.Module, note string) notifications.PersonResult {
	t.Helper()
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	approval, err := module.ApproveUpdates(t.Context(), approveAll(preview, note, nil))
	require.NoError(t, err)
	require.Len(t, approval.People, 1)
	require.Equal(t, notifications.ResultNotified, approval.People[0].Status)
	return approval.People[0]
}

func storedNotification(t *testing.T, db *bun.DB, id string) models.UpdateNotification {
	t.Helper()
	var row models.UpdateNotification
	require.NoError(t, db.NewSelect().Model(&row).Where("id = ?", id).Scan(t.Context()))
	return row
}

func TestUpdateEmailIsRecheckedAgainstEligibilityBeforeSending(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for name, scenario := range map[string]struct {
		options  personOptions
		change   func(ctx context.Context, db *bun.DB, person models.Person) error
		queued   bool
		status   string
		contains string
	}{
		"subscribed":                       {options: personOptions{email: "alex@example.test", emailUpdates: true}, queued: true, status: "delivered"},
		"no destination selected":          {options: personOptions{}, queued: false},
		"destination without subscription": {options: personOptions{email: "alex@example.test"}, queued: false},
		"unsubscribed before sending": {options: personOptions{email: "alex@example.test", emailUpdates: true}, queued: true, status: "skipped", contains: "turned off update emails", change: func(ctx context.Context, db *bun.DB, person models.Person) error {
			_, err := db.NewUpdate().Model((*models.Person)(nil)).Set("email_updates = false").Where("id = ?", person.ID).Exec(ctx)
			return err
		}},
		"destination unlinked before sending": {options: personOptions{email: "alex@example.test", emailUpdates: true}, queued: true, status: "skipped", contains: "no email selected", change: func(ctx context.Context, db *bun.DB, person models.Person) error {
			_, err := db.NewUpdate().Model((*models.Identity)(nil)).Set("unlinked_at = ?", time.Now().UTC()).Where("person_id = ?", person.ID).Exec(ctx)
			return err
		}},
		"deactivated before sending": {options: personOptions{email: "alex@example.test", emailUpdates: true}, queued: true, status: "skipped", contains: "deactivated", change: func(ctx context.Context, db *bun.DB, person models.Person) error {
			_, err := db.NewUpdate().Model((*models.Person)(nil)).Set("deactivated_at = ?", time.Now().UTC()).Where("id = ?", person.ID).Exec(ctx)
			return err
		}},
		"promoted before sending": {options: personOptions{email: "alex@example.test", emailUpdates: true}, queued: true, status: "skipped", contains: "Curators", change: func(ctx context.Context, db *bun.DB, person models.Person) error {
			_, err := db.NewUpdate().Model((*models.Person)(nil)).Set("is_curator = true").Where("id = ?", person.ID).Exec(ctx)
			return err
		}},
		"malformed destination": {options: personOptions{email: "not an address", emailUpdates: true}, queued: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
			module, recorder, jobs := mailModule(t, db, content)
			album := seedAlbum(t, db, "Coast "+name, "coast-01.jpg", "coast-02.mp4")
			options := scenario.options
			if strings.Contains(options.email, "@") {
				options.email = strings.ReplaceAll(name, " ", "-") + "@example.test"
			}
			person := seedPerson(t, db, "Alex", options)
			content.set(person.ID.String(), album.Entries...)
			result := approveOne(t, module, "")
			stored := storedNotification(t, db, result.NotificationID)
			if !scenario.queued {
				assert.Nil(t, result.Delivery, "an ineligible destination gets the update in app only")
				assert.Empty(t, result.Email)
				assert.Nil(t, stored.DeliveryID)
				assert.Zero(t, jobs.count())
				return
			}
			require.NotNil(t, result.Delivery)
			assert.Equal(t, "queued", result.Delivery.Status)
			assert.Equal(t, options.email, result.Email)
			require.NotNil(t, stored.DeliveryID)
			assert.Equal(t, result.Delivery.ID, stored.DeliveryID.String())
			assert.Equal(t, 1, jobs.count(), "the email commits with the approval")
			if scenario.change != nil {
				require.NoError(t, scenario.change(t.Context(), db, person))
			}
			require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
			delivery := status(t, db, module, result.Delivery.ID)
			assert.Equal(t, scenario.status, delivery.Status)
			assert.Contains(t, delivery.Message, scenario.contains)
			if scenario.status == "delivered" {
				require.Len(t, recorder.Sent(), 1)
				assert.Equal(t, options.email, recorder.Sent()[0].To)
				assert.Equal(t, notifications.KindUpdate, recorder.Sent()[0].Kind)
			} else {
				assert.Empty(t, recorder.Sent(), "a skipped recipient is never emailed")
			}
			// Canonical in-app history is untouched by delivery outcomes.
			after := storedNotification(t, db, result.NotificationID)
			assert.Equal(t, stored.Payload, after.Payload)
			assert.ElementsMatch(t, entryIDs(album.Entries...), announced(t, module, person.ID.String()))
			list, err := module.ListNotifications(t.Context(), person.ID.String())
			require.NoError(t, err)
			assert.Len(t, list.Notifications, 1)
		})
	}
}

func TestUpdateEmailMirrorsTheStillVisibleApprovedSubset(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg", "surf-lesson.mp4", "sunset.mp4", "coast-05.jpg")
	family := seedAlbum(t, db, "Family", "family-01.jpg", "family-02.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	// The fifth Coast item is allowed only after approval.
	content.set(alex.ID.String(), append(coast.Entries[:4], family.Entries...)...)
	result := approveOne(t, module, "Enjoy the new photos!")
	require.NotNil(t, result.Delivery)
	stored := storedNotification(t, db, result.NotificationID)

	// After approval: one approved video is revoked, a new photo is allowed,
	// the Album is renamed, and the remaining video gets a new title.
	renamed := []notifications.VisibleEntry{}
	for _, entry := range append(append([]notifications.VisibleEntry{}, coast.Entries[0], coast.Entries[1], coast.Entries[3], coast.Entries[4]), family.Entries...) {
		entry.AlbumTitle = strings.Replace(entry.AlbumTitle, "Coast", "Renamed Coast", 1)
		if entry.Title == "sunset" {
			entry.Title = "Golden hour"
		}
		renamed = append(renamed, entry)
	}
	content.set(alex.ID.String(), renamed...)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "delivered", status(t, db, module, result.Delivery.ID).Status)
	require.Len(t, recorder.Sent(), 1)
	sent := recorder.Sent()[0]
	assert.Equal(t, "New photos and videos in 2 albums on Memento", sent.Subject)
	assert.Contains(t, sent.Body, "Hi Alex,")
	assert.Contains(t, sent.Body, "Coast (new album)\n2 photos and 1 video\nVideos: sunset\n", "counts follow the still-visible approved subset with frozen titles")
	assert.Contains(t, sent.Body, "Family (new album)\n2 photos\n")
	assert.NotContains(t, sent.Body, "Renamed Coast", "a later Album rename never enters the email")
	assert.NotContains(t, sent.Body, "surf-lesson", "a revoked video is left out")
	assert.NotContains(t, sent.Body, "Golden hour", "a later title edit never enters the email")
	assert.NotContains(t, sent.Body, "coast-0", "photos are counted, never listed")
	assert.Contains(t, sent.Body, "A note from your Curator:\nEnjoy the new photos!\n")
	assert.Contains(t, sent.Body, "See them on Memento:\n"+publicURL+"/albums\n", "several Albums open the ordinary Album list")
	assert.NotContains(t, sent.Body, "/albums/"+coast.ID, "no per-Album link list")
	assert.Regexp(t, `\n`+regexp.QuoteMeta(publicURL)+`/unsubscribe\?token=[A-Za-z0-9_-]{43}\n`, sent.Body)
	// Filtering never rewrites the immutable summary or the announcements.
	after := storedNotification(t, db, result.NotificationID)
	assert.Equal(t, stored.Payload, after.Payload)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Equal(t, []notifications.NotificationAlbum{
		{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 2, VideoCount: 2, VideoTitles: []string{"surf-lesson", "sunset"}},
		{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 2, VideoCount: 0, VideoTitles: []string{}},
	}, list.Notifications[0].Albums)
	assert.ElementsMatch(t, entryIDs(append(coast.Entries[:4], family.Entries...)...), announced(t, module, alex.ID.String()))

	// A single remaining Album opens directly; a fully revoked update is skipped.
	sam := seedPerson(t, db, "Sam", personOptions{email: "sam@example.test", emailUpdates: true})
	content.set(sam.ID.String(), coast.Entries[2])
	content.set(alex.ID.String())
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	approval, err := module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	samResult := approval.People[0]
	require.NotNil(t, samResult.Delivery)
	require.NoError(t, module.Execute(t.Context(), samResult.Delivery.ID, false))
	require.Len(t, recorder.Sent(), 2)
	assert.Equal(t, "Coast was shared with you on Memento", recorder.Sent()[1].Subject)
	assert.Contains(t, recorder.Sent()[1].Body, "Coast (new album)\n1 video\nVideos: surf-lesson\n")
	assert.Contains(t, recorder.Sent()[1].Body, "See them on Memento:\n"+publicURL+"/albums/"+coast.ID+"/photos\n")

	// More content in an announced Album is an update, and the email says so.
	content.set(sam.ID.String(), coast.Entries[2], coast.Entries[0])
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	approval, err = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), approval.People[0].Delivery.ID, false))
	require.Len(t, recorder.Sent(), 3)
	assert.Equal(t, "New photos in Coast on Memento", recorder.Sent()[2].Subject)
	assert.Contains(t, recorder.Sent()[2].Body, "Coast (updated)\n1 photo\n")
	assert.NotContains(t, recorder.Sent()[2].Body, "Videos:")

	// An update whose only content is revoked before sending is skipped.
	content.set(sam.ID.String(), coast.Entries[2], coast.Entries[0], coast.Entries[3])
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	approval, err = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	revoked := approval.People[0]
	content.set(sam.ID.String(), coast.Entries[2], coast.Entries[0])
	require.NoError(t, module.Execute(t.Context(), revoked.Delivery.ID, false))
	delivery := status(t, db, module, revoked.Delivery.ID)
	assert.Equal(t, "skipped", delivery.Status)
	assert.Contains(t, delivery.Message, "any more")
	assert.Len(t, recorder.Sent(), 3)
	assert.ElementsMatch(t, entryIDs(coast.Entries[0], coast.Entries[2], coast.Entries[3]), announced(t, module, sam.ID.String()), "announcements stay announced")
}

func TestSkippedUpdateEmailCanBeSentAfterResubscribing(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries...)
	result := approveOne(t, module, "")
	_, err := db.NewUpdate().Model((*models.Person)(nil)).Set("email_updates = false").Where("id = ?", alex.ID).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "skipped", status(t, db, module, result.Delivery.ID).Status)
	open, err := module.OpenDeliveries(t.Context())
	require.NoError(t, err)
	assert.Empty(t, open, "a skipped email needs no Curator")
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("email_updates = true").Where("id = ?", alex.ID).Exec(t.Context())
	require.NoError(t, err)
	retried, err := module.RetryDelivery(t.Context(), result.Delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "queued", retried.Status)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "delivered", status(t, db, module, result.Delivery.ID).Status)
	assert.Len(t, recorder.Sent(), 1)
}

func TestUnsubscribeLinkChangesNothingUntilConfirmed(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, jobs := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries[0])
	first := approveOne(t, module, "")
	require.NoError(t, module.Execute(t.Context(), first.Delivery.ID, false))
	require.Len(t, recorder.Sent(), 1)
	link := regexp.MustCompile(regexp.QuoteMeta(publicURL) + `/unsubscribe\?token=([A-Za-z0-9_-]+)`).FindStringSubmatch(recorder.Sent()[0].Body)
	require.Len(t, link, 2)
	token := link[1]

	// Loading the link, as a mail scanner would, reports and changes nothing.
	for range 2 {
		state, err := module.UnsubscribeStatus(t.Context(), token)
		require.NoError(t, err)
		assert.Equal(t, notifications.UnsubscribeStatus{DisplayName: "Alex", Email: "alex@example.test", Subscribed: true}, state)
	}
	var person models.Person
	require.NoError(t, db.NewSelect().Model(&person).Where("id = ?", alex.ID).Scan(t.Context()))
	assert.True(t, person.EmailUpdates)
	_, err := module.UnsubscribeStatus(t.Context(), strings.Repeat("x", 43))
	require.ErrorIs(t, err, errcodes.NotFound("Link"))
	_, err = module.UnsubscribeStatus(t.Context(), "short")
	require.ErrorIs(t, err, errcodes.NotFound("Link"))

	// Confirming stops update email only: the destination stays selected for
	// transactional mail, and the in-app notification keeps arriving.
	state, err := module.Unsubscribe(t.Context(), token)
	require.NoError(t, err)
	assert.False(t, state.Subscribed)
	require.NoError(t, db.NewSelect().Model(&person).Where("id = ?", alex.ID).Scan(t.Context()))
	assert.False(t, person.EmailUpdates)
	assert.NotNil(t, person.UpdateIdentityID)
	state, err = module.Unsubscribe(t.Context(), token)
	require.NoError(t, err, "confirming again is harmless")
	assert.False(t, state.Subscribed)
	state, err = module.UnsubscribeStatus(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, "alex@example.test", state.Email)
	assert.False(t, state.Subscribed)

	content.set(alex.ID.String(), coast.Entries...)
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.False(t, preview.People[0].EmailEligible)
	second := approveOne(t, module, "")
	assert.Nil(t, second.Delivery)
	assert.Equal(t, 1, jobs.count())
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Len(t, list.Notifications, 2)
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.Enqueue(ctx, tx, notifications.Message{Kind: "invitation", To: "alex@example.test", Subject: "Invitation", Body: "Hi"})
		return err
	}), "transactional mail ignores the update-email preference")
}

func TestOpenDeliveriesAndDeliberateRetry(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, jobs := mailModule(t, db, content)
	failure := &notifications.DeliveryError{Outcome: notifications.OutcomePermanent, Summary: "The mail server replied 550."}
	recorder.AfterSend = func(context.Context, notifications.Message) error { return failure }
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries...)
	result := approveOne(t, module, "")
	invitation := enqueue(t, db, module, invitation)
	open, err := module.OpenDeliveries(t.Context())
	require.NoError(t, err)
	require.Len(t, open, 2)
	for _, work := range open {
		assert.Equal(t, "queued", work.Status)
	}
	states, err := module.DeliveryStates(t.Context(), []string{result.Delivery.ID, invitation.ID, "not-a-uuid"})
	require.NoError(t, err)
	assert.Len(t, states, 2)

	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	require.NoError(t, module.Execute(t.Context(), invitation.ID, false))
	open, err = module.OpenDeliveries(t.Context())
	require.NoError(t, err)
	require.Len(t, open, 2)
	byKind := map[string]notifications.DeliveryWork{}
	for _, work := range open {
		byKind[work.Kind] = work
	}
	assert.Equal(t, "failed", byKind[notifications.KindUpdate].Status)
	assert.Equal(t, alex.ID.String(), byKind[notifications.KindUpdate].PersonID)
	assert.Equal(t, "Alex", byKind[notifications.KindUpdate].PersonName)
	assert.Equal(t, "alex@example.test", byKind[notifications.KindUpdate].Recipient)
	assert.Equal(t, "failed", byKind["invitation"].Status)
	_, err = module.RetryDelivery(t.Context(), invitation.ID)
	require.ErrorIs(t, err, notifications.ErrInvitationRetry)
	_, err = module.RetryDelivery(t.Context(), "nope")
	require.ErrorIs(t, err, errcodes.NotFound("Delivery"))

	recorder.AfterSend = nil
	retried, err := module.RetryDelivery(t.Context(), result.Delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "queued", retried.Status)
	assert.Equal(t, 3, jobs.count())
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "delivered", status(t, db, module, result.Delivery.ID).Status)
	open, err = module.OpenDeliveries(t.Context())
	require.NoError(t, err)
	require.Len(t, open, 1, "a delivered email leaves the open list")
	assert.Equal(t, "invitation", open[0].Kind)
	_, err = module.RetryDelivery(t.Context(), result.Delivery.ID)
	require.ErrorIs(t, err, notifications.ErrAlreadyDelivered)
}

type brokenContent struct{ err error }

func (c brokenContent) VisibleEntries(context.Context, bun.IDB, string) ([]notifications.VisibleEntry, error) {
	return nil, c.err
}

func TestUpdateEmailThatCannotBePreparedFailsOnTheLastAttempt(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries...)
	result := approveOne(t, module, "")
	// The content query breaks after approval, so the claim itself fails.
	broken := notifications.New(db, recorder, (&queue{}).enqueue, brokenContent{errors.New("controlled content failure")}, nil)
	require.Error(t, broken.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "queued", status(t, db, module, result.Delivery.ID).Status, "an earlier attempt leaves the record for River's retry")
	require.Error(t, broken.Execute(t.Context(), result.Delivery.ID, true))
	failed := status(t, db, module, result.Delivery.ID)
	assert.Equal(t, "failed", failed.Status, "the last attempt surfaces the problem for a deliberate retry")
	assert.Contains(t, failed.Message, "could not prepare")
	assert.Empty(t, recorder.Sent())
	// Once the cause is fixed, the Curator's retry delivers as usual.
	_, err := module.RetryDelivery(t.Context(), result.Delivery.ID)
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.Equal(t, "delivered", status(t, db, module, result.Delivery.ID).Status)
}
