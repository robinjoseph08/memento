package notifications_test

import (
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDismissalSilentlyBaselinesOnlySelectedChanges(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	mailer := &notifications.Recorder{}
	jobs := &queue{}
	module := notifications.New(db, mailer, jobs.enqueue, content, nil)
	coast := seedAlbum(t, db, "Coast", "first.jpg", "second.mp4", "later.jpg")
	family := seedAlbum(t, db, "Family", "family.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	sam := seedPerson(t, db, "Sam", personOptions{})
	content.set(alex.ID.String(), coast.Entries[0], coast.Entries[1], family.Entries[0])
	content.set(sam.ID.String(), coast.Entries[0])
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	assert.True(t, preview.EmailConfigured)
	assert.True(t, preview.People[0].EmailEligible)

	request := notifications.DismissRequest{People: approveAll(preview, "", map[string][]string{alex.ID.String(): {family.ID}}).People[:1]}
	allExcluded := request.People[0]
	allExcluded.ExcludedAlbumIDs = []string{coast.ID, family.ID}
	result, err := module.DismissUpdates(t.Context(), notifications.DismissRequest{People: []notifications.ApprovePerson{allExcluded}})
	require.NoError(t, err)
	require.Len(t, result.People, 1)
	assert.Equal(t, notifications.ResultSkipped, result.People[0].Status)
	assert.Empty(t, announced(t, module, alex.ID.String()))

	result, err = module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, result.People, 1)
	assert.Equal(t, notifications.PersonResult{
		PersonID: alex.ID.String(), DisplayName: "Alex", Status: notifications.ResultDismissed,
		AlbumCount: 1, PhotoCount: 1, VideoCount: 1,
	}, result.People[0])
	assert.ElementsMatch(t, entryIDs(coast.Entries[:2]...), announced(t, module, alex.ID.String()))
	assert.Empty(t, announced(t, module, sam.ID.String()), "an omitted Person stays pending")
	baseline, err := module.Announced(t.Context(), db, alex.ID.String())
	require.NoError(t, err)
	assert.Equal(t, notifications.Baseline{Albums: 1, Entries: 2}, baseline)
	var associations []models.AnnouncedEntry
	require.NoError(t, db.NewSelect().Model(&associations).Scan(t.Context()))
	require.Len(t, associations, 2)
	for _, association := range associations {
		assert.Nil(t, association.NotificationID)
	}

	retried, err := module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, retried.People, 1)
	assert.Equal(t, notifications.ResultSkipped, retried.People[0].Status)
	assert.ElementsMatch(t, entryIDs(coast.Entries[:2]...), announced(t, module, alex.ID.String()))
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[0].Albums)
	assert.Equal(t, sam.ID.String(), preview.People[1].PersonID)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[1].Albums)

	// Removing and restoring dismissed access leaves only the excluded Album pending.
	content.set(alex.ID.String())
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, sam.ID.String(), preview.People[0].PersonID)
	content.set(alex.ID.String(), coast.Entries[0], coast.Entries[1], family.Entries[0])
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[0].Albums)

	content.set(alex.ID.String(), append(coast.Entries, family.Entries...)...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	assert.Equal(t, []notifications.NotificationAlbum{
		{ID: coast.ID, Title: "Coast", Status: notifications.AlbumUpdated, PhotoCount: 1},
		{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 1},
	}, preview.People[0].Albums)

	for _, person := range []models.Person{alex, sam} {
		list, err := module.ListNotifications(t.Context(), person.ID.String())
		require.NoError(t, err)
		assert.Empty(t, list.Notifications)
		assert.Zero(t, list.Unread)
	}
	count, err := db.NewSelect().Model((*models.UpdateNotification)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
	count, err = db.NewSelect().Model((*models.MailDelivery)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
	count, err = db.NewSelect().Model((*models.UnsubscribeToken)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
	assert.Zero(t, jobs.count(), "dismissal never enqueues delivery work")
	assert.Empty(t, mailer.Sent())
}

func TestDismissalSkipsChangedContentAndEligibility(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "first.jpg", "later.jpg")
	people := map[string]models.Person{}
	for _, name := range []string{"Grew", "Promoted", "Deactivated", "Not onboarded", "Revoked", "Wrong token", "Steady"} {
		person := seedPerson(t, db, name, personOptions{})
		people[name] = person
		content.set(person.ID.String(), coast.Entries[0])
	}
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, len(people))
	request := notifications.DismissRequest{People: approveAll(preview, "", nil).People}
	for i := range request.People {
		if request.People[i].PersonID == people["Wrong token"].ID.String() {
			request.People[i].ReviewToken = "not-the-reviewed-token"
		}
	}
	content.set(people["Grew"].ID.String(), coast.Entries...)
	content.set(people["Revoked"].ID.String())
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("is_curator = true").Where("id = ?", people["Promoted"].ID).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("deactivated_at = ?", time.Now().UTC()).Where("id = ?", people["Deactivated"].ID).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("onboarding_completed_at = NULL").Where("id = ?", people["Not onboarded"].ID).Exec(t.Context())
	require.NoError(t, err)

	result, err := module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, result.People, len(people))
	for _, outcome := range result.People {
		if outcome.DisplayName == "Steady" {
			assert.Equal(t, notifications.ResultDismissed, outcome.Status)
			assert.Equal(t, entryIDs(coast.Entries[0]), announced(t, module, outcome.PersonID))
		} else {
			assert.Equal(t, notifications.ResultSkipped, outcome.Status, outcome.DisplayName)
			assert.NotEmpty(t, outcome.Message, outcome.DisplayName)
			assert.Empty(t, announced(t, module, outcome.PersonID), outcome.DisplayName)
		}
	}

	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2, "fresh previews recover grown content and a bad token")
	request = notifications.DismissRequest{People: approveAll(preview, "", nil).People}
	result, err = module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	for _, outcome := range result.People {
		assert.Equal(t, notifications.ResultDismissed, outcome.Status)
	}
	assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, people["Grew"].ID.String()))
	result, err = module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	for _, outcome := range result.People {
		assert.Equal(t, notifications.ResultSkipped, outcome.Status, "a fully consumed row is idempotent too")
	}
}

func TestOverlappingDismissalAndApprovalConsumeChangesOnce(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "first.jpg", "second.mp4")
	alex := seedPerson(t, db, "Alex", personOptions{})
	sam := seedPerson(t, db, "Sam", personOptions{})
	for _, person := range []models.Person{alex, sam} {
		content.set(person.ID.String(), coast.Entries...)
	}
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	approval := approveAll(preview, "", nil)
	dismissal := notifications.DismissRequest{People: []notifications.ApprovePerson{approval.People[1], approval.People[0]}}
	results := make([]notifications.Approval, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		results[0], errs[0] = module.DismissUpdates(t.Context(), dismissal)
	})
	wg.Go(func() {
		<-start
		results[1], errs[1] = module.ApproveUpdates(t.Context(), approval)
	})
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err)
		require.Len(t, results[i].People, 2)
	}
	for _, person := range []models.Person{alex, sam} {
		statuses := []string{}
		notified := 0
		for _, result := range results {
			for _, outcome := range result.People {
				if outcome.PersonID == person.ID.String() {
					statuses = append(statuses, outcome.Status)
					if outcome.Status == notifications.ResultNotified {
						notified++
					}
				}
			}
		}
		if notified == 1 {
			assert.ElementsMatch(t, []string{notifications.ResultNotified, notifications.ResultSkipped}, statuses)
		} else {
			assert.ElementsMatch(t, []string{notifications.ResultDismissed, notifications.ResultSkipped}, statuses)
		}
		list, err := module.ListNotifications(t.Context(), person.ID.String())
		require.NoError(t, err)
		assert.Len(t, list.Notifications, notified)
		assert.Equal(t, notified, list.Unread)
		assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, person.ID.String()))
		var associations []models.AnnouncedEntry
		require.NoError(t, db.NewSelect().Model(&associations).Where("person_id = ?", person.ID).Scan(t.Context()))
		require.Len(t, associations, len(coast.Entries))
		for _, association := range associations {
			if notified == 1 {
				require.NotNil(t, association.NotificationID)
				assert.Equal(t, list.Notifications[0].ID, association.NotificationID.String())
			} else {
				assert.Nil(t, association.NotificationID)
			}
		}
	}
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People)
}

func TestDismissalRejectsEmptyAndDuplicatePeople(t *testing.T) {
	t.Parallel()
	personID := models.NewUUIDv7().String()
	for name, people := range map[string][]notifications.ApprovePerson{
		"missing":   nil,
		"empty":     {},
		"duplicate": {{PersonID: personID, ReviewToken: "token"}, {PersonID: personID, ReviewToken: "token"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := notifications.New(nil, nil, nil, nil, nil)
			result, err := module.DismissUpdates(t.Context(), notifications.DismissRequest{People: people})
			require.ErrorAs(t, err, new(*errcodes.FieldError))
			assert.Empty(t, result.People)
		})
	}
}
