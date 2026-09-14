package notifications_test

import (
	"context"
	"sync"
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

// mutableContent stands in for Publishing so tests can change what each
// Person sees between a preview and its approval.
type mutableContent struct {
	mu      sync.Mutex
	visible map[string][]notifications.VisibleEntry
}

func (c *mutableContent) VisibleEntries(_ context.Context, _ bun.IDB, personID string) ([]notifications.VisibleEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notifications.VisibleEntry(nil), c.visible[personID]...), nil
}

func (c *mutableContent) set(personID string, entries ...notifications.VisibleEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.visible[personID] = entries
}

type seededAlbum struct {
	ID      string
	Title   string
	Entries []notifications.VisibleEntry
}

// seedAlbum stores a complete Album whose entries satisfy every foreign key
// an announcement needs. Filenames ending in .mp4 become videos.
func seedAlbum(t *testing.T, db *bun.DB, title string, filenames ...string) seededAlbum {
	t.Helper()
	now := time.Now().UTC()
	album := models.Album{ID: models.NewUUIDv7(), SourceID: "source-" + title, Title: title, ImportStatus: "complete", ImportUpdatedAt: now, CreatedAt: now}
	_, err := db.NewInsert().Model(&album).Exec(t.Context())
	require.NoError(t, err)
	moment := models.Moment{ID: models.NewUUIDv7(), AlbumID: album.ID, CaptureDate: "2026-01-01"}
	items := []models.MediaItem{}
	entries := []models.AlbumEntry{}
	result := seededAlbum{ID: album.ID.String(), Title: title}
	for i, filename := range filenames {
		kind := "IMAGE"
		if len(filename) > 4 && filename[len(filename)-4:] == ".mp4" {
			kind = "VIDEO"
		}
		item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: title + "-" + filename, Checksum: filename, Filename: filename, Kind: kind, CapturedAt: now.Add(time.Duration(i) * time.Minute), SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "1"}
		entry := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: item.ID, MomentID: &moment.ID}
		if i == 0 {
			moment.CoverEntryID = entry.ID
		}
		items = append(items, item)
		entries = append(entries, entry)
		result.Entries = append(result.Entries, notifications.VisibleEntry{AlbumID: album.ID.String(), AlbumTitle: title, EntryID: entry.ID.String(), Kind: kind})
	}
	_, err = db.NewInsert().Model(&items).Exec(t.Context())
	require.NoError(t, err)
	// Membership and cover references are deferred, so both sides land in one transaction.
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&moment).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&entries).Exec(ctx)
		return err
	}))
	return result
}

type personOptions struct {
	curator, deactivated, notOnboarded bool
	email                              string
	emailUpdates                       bool
}

func seedPerson(t *testing.T, db *bun.DB, name string, options personOptions) models.Person {
	t.Helper()
	now := time.Now().UTC()
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: name, IsCurator: options.curator, CreatedAt: now}
	if !options.notOnboarded {
		person.OnboardingCompletedAt = &now
	}
	if options.deactivated {
		person.DeactivatedAt = &now
	}
	_, err := db.NewInsert().Model(&person).Exec(t.Context())
	require.NoError(t, err)
	if options.email != "" {
		identity := models.Identity{ID: models.NewUUIDv7(), PersonID: person.ID, Provider: "fake", Subject: options.email, Email: options.email, CreatedAt: now}
		_, err = db.NewInsert().Model(&identity).Exec(t.Context())
		require.NoError(t, err)
		_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("update_identity_id = ?", identity.ID).Set("email_updates = ?", options.emailUpdates).Where("id = ?", person.ID).Exec(t.Context())
		require.NoError(t, err)
	}
	return person
}

func announced(t *testing.T, module *notifications.Module, personID string) []string {
	t.Helper()
	ids, err := module.AnnouncedEntryIDs(t.Context(), personID)
	require.NoError(t, err)
	return ids
}

func entryIDs(entries ...notifications.VisibleEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.EntryID)
	}
	return ids
}

func approveAll(preview notifications.Preview, note string, exclude map[string][]string) notifications.ApproveRequest {
	request := notifications.ApproveRequest{Note: note}
	for _, row := range preview.People {
		request.People = append(request.People, notifications.ApprovePerson{PersonID: row.PersonID, ReviewToken: row.ReviewToken, ExcludedAlbumIDs: exclude[row.PersonID]})
	}
	return request
}

func TestPreviewGroupsPeopleAndApprovalAnnouncesExactlyWhatWasReviewed(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg", "coast-03.mp4")
	family := seedAlbum(t, db, "Family", "family-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	sam := seedPerson(t, db, "Sam", personOptions{email: "sam@example.test"})
	pat := seedPerson(t, db, "Pat", personOptions{})
	curator := seedPerson(t, db, "Curator", personOptions{curator: true, email: "curator@example.test", emailUpdates: true})
	newcomer := seedPerson(t, db, "Newcomer", personOptions{notOnboarded: true})
	gone := seedPerson(t, db, "Gone", personOptions{deactivated: true})
	content.set(alex.ID.String(), append(coast.Entries, family.Entries...)...)
	content.set(sam.ID.String(), coast.Entries[0])
	content.set(curator.ID.String(), append(coast.Entries, family.Entries...)...)
	content.set(newcomer.ID.String(), coast.Entries...)
	content.set(gone.ID.String(), coast.Entries...)

	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2, "Pat has nothing to announce; Curators, deactivated, and not yet onboarded people never appear")
	assert.Equal(t, "Alex", preview.People[0].DisplayName)
	assert.Equal(t, "Sam", preview.People[1].DisplayName)
	assert.True(t, preview.People[0].EmailEligible)
	assert.Equal(t, "alex@example.test", preview.People[0].UpdateEmail)
	assert.False(t, preview.People[1].EmailEligible, "an email without opting in is not eligible")
	assert.Equal(t, []notifications.NotificationAlbum{
		{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 2, VideoCount: 1},
		{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 1, VideoCount: 0},
	}, preview.People[0].Albums)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[1].Albums)
	assert.NotEqual(t, preview.People[0].ReviewToken, preview.People[1].ReviewToken)
	_ = pat

	approval, err := module.ApproveUpdates(t.Context(), approveAll(preview, "  Enjoy the new photos!  ", map[string][]string{alex.ID.String(): {family.ID}}))
	require.NoError(t, err)
	require.Len(t, approval.People, 2)
	assert.Equal(t, notifications.ResultNotified, approval.People[0].Status)
	assert.Equal(t, 1, approval.People[0].AlbumCount, "the excluded Album update is not announced")
	assert.Equal(t, 2, approval.People[0].PhotoCount)
	assert.Equal(t, 1, approval.People[0].VideoCount)
	assert.Equal(t, notifications.ResultNotified, approval.People[1].Status)
	assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, alex.ID.String()))
	assert.Equal(t, entryIDs(coast.Entries[0]), announced(t, module, sam.ID.String()))
	baseline, err := module.Announced(t.Context(), db, alex.ID.String())
	require.NoError(t, err)
	assert.Equal(t, notifications.Baseline{Albums: 1, Entries: 3}, baseline)

	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	require.Len(t, list.Notifications, 1)
	assert.Equal(t, 1, list.Unread)
	assert.Equal(t, approval.People[0].NotificationID, list.Notifications[0].ID)
	assert.Equal(t, "Enjoy the new photos!", list.Notifications[0].Note)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 2, VideoCount: 1}}, list.Notifications[0].Albums)

	// Submitting the same approval again announces nothing more: Sam's row is
	// spent, and Alex's row no longer matches because Coast has been sent.
	repeated, err := module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultSkipped, repeated.People[0].Status)
	assert.Contains(t, repeated.People[0].Message, "changed since this preview")
	assert.Equal(t, notifications.ResultSkipped, repeated.People[1].Status)
	assert.Contains(t, repeated.People[1].Message, "already sent")
	list, err = module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Len(t, list.Notifications, 1)

	// The excluded Album is still new next time; an announced Album with new
	// media is an update.
	content.set(sam.ID.String(), coast.Entries[0], coast.Entries[2])
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 2)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: family.ID, Title: "Family", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[0].Albums)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumUpdated, VideoCount: 1}}, preview.People[1].Albums)

	_, err = module.ApproveUpdates(t.Context(), notifications.ApproveRequest{})
	require.ErrorAs(t, err, new(*errcodes.FieldError))
	_, err = module.ApproveUpdates(t.Context(), notifications.ApproveRequest{People: []notifications.ApprovePerson{{PersonID: alex.ID.String(), ReviewToken: "x"}, {PersonID: alex.ID.String(), ReviewToken: "x"}}})
	require.ErrorAs(t, err, new(*errcodes.FieldError), "a person listed twice is rejected before anything is announced")
}

func TestApprovalSkipsPeopleWhoseContentOrEligibilityChanged(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg")
	grew := seedPerson(t, db, "Grew", personOptions{})
	promoted := seedPerson(t, db, "Promoted", personOptions{})
	deactivated := seedPerson(t, db, "Deactivated", personOptions{})
	revoked := seedPerson(t, db, "Revoked", personOptions{})
	steady := seedPerson(t, db, "Steady", personOptions{})
	for _, person := range []models.Person{grew, promoted, deactivated, revoked, steady} {
		content.set(person.ID.String(), coast.Entries[0])
	}
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 5)

	// Between preview and approval: one Person gains content, one is promoted,
	// one is deactivated, and one loses the only content the row showed.
	content.set(grew.ID.String(), coast.Entries...)
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("is_curator = true").Where("id = ?", promoted.ID).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("deactivated_at = ?", time.Now().UTC()).Where("id = ?", deactivated.ID).Exec(t.Context())
	require.NoError(t, err)
	content.set(revoked.ID.String())

	approval, err := module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	outcomes := map[string]notifications.PersonResult{}
	for _, outcome := range approval.People {
		outcomes[outcome.DisplayName] = outcome
	}
	assert.Equal(t, notifications.ResultSkipped, outcomes["Grew"].Status)
	assert.Contains(t, outcomes["Grew"].Message, "changed since this preview")
	assert.Equal(t, notifications.ResultSkipped, outcomes["Promoted"].Status)
	assert.Contains(t, outcomes["Promoted"].Message, "Curators")
	assert.Equal(t, notifications.ResultSkipped, outcomes["Deactivated"].Status)
	assert.Equal(t, notifications.ResultSkipped, outcomes["Revoked"].Status)
	assert.Equal(t, notifications.ResultNotified, outcomes["Steady"].Status)
	for _, person := range []models.Person{grew, promoted, deactivated, revoked} {
		assert.Empty(t, announced(t, module, person.ID.String()), person.DisplayName)
		list, err := module.ListNotifications(t.Context(), person.ID.String())
		require.NoError(t, err)
		assert.Empty(t, list.Notifications, person.DisplayName)
	}
	assert.Equal(t, entryIDs(coast.Entries[0]), announced(t, module, steady.ID.String()))

	// A fresh preview reviews the grown row; the token it carries then approves.
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, "Grew", preview.People[0].DisplayName)
	approval, err = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultNotified, approval.People[0].Status)
	assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, grew.ID.String()))
}

func TestIndependentOverlappingPreviewsNeverAnnounceContentTwice(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	sam := seedPerson(t, db, "Sam", personOptions{})
	content.set(alex.ID.String(), coast.Entries...)
	content.set(sam.ID.String(), coast.Entries[0])
	first, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	second, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Equal(t, first, second, "two Curators reviewing the same state see the same rows")

	// Both Curators submit at once. Row locks serialize them, so exactly one
	// approval announces each Person and the other reports nothing new.
	var wg sync.WaitGroup
	results := make([]notifications.Approval, 2)
	errs := make([]error, 2)
	for i, preview := range []notifications.Preview{first, second} {
		wg.Go(func() {
			results[i], errs[i] = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
		})
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	for _, person := range []models.Person{alex, sam} {
		notified := 0
		for _, approval := range results {
			for _, outcome := range approval.People {
				if outcome.PersonID == person.ID.String() && outcome.Status == notifications.ResultNotified {
					notified++
				}
			}
		}
		assert.Equal(t, 1, notified, person.DisplayName)
		list, err := module.ListNotifications(t.Context(), person.ID.String())
		require.NoError(t, err)
		assert.Len(t, list.Notifications, 1, person.DisplayName)
	}
	assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, alex.ID.String()))
	assert.Equal(t, entryIDs(coast.Entries[0]), announced(t, module, sam.ID.String()))
	var duplicates int
	require.NoError(t, db.NewSelect().Model((*models.AnnouncedEntry)(nil)).ColumnExpr("count(*)").Where("notification_id IS NULL").Scan(t.Context(), &duplicates))
	assert.Zero(t, duplicates, "every approved association names its notification")
}

func TestBaselineAndRevocationKeepAnnouncedContentAnnounced(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.jpg", "coast-03.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	content.set(alex.ID.String(), coast.Entries[0], coast.Entries[1])
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.RecordBaseline(ctx, tx, alex.ID.String())
		return err
	}))
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People, "content visible at Onboarding is never a later update")

	content.set(alex.ID.String(), coast.Entries...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumUpdated, PhotoCount: 1}}, preview.People[0].Albums)
	_, err = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)

	// Revoking and restoring access does not make old media new again.
	content.set(alex.ID.String())
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People)
	content.set(alex.ID.String(), coast.Entries...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People)
	assert.ElementsMatch(t, entryIDs(coast.Entries...), announced(t, module, alex.ID.String()))
}

func TestNotificationsStayReadableAfterDeletionAndReadStateIsIndependent(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	module := notifications.New(db, nil, nil, content, func() time.Time { return clock })
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg", "coast-02.mp4")
	family := seedAlbum(t, db, "Family", "family-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	sam := seedPerson(t, db, "Sam", personOptions{})
	content.set(alex.ID.String(), coast.Entries...)
	content.set(sam.ID.String(), coast.Entries[0])
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	_, err = module.ApproveUpdates(t.Context(), approveAll(preview, "First batch", nil))
	require.NoError(t, err)
	clock = clock.Add(time.Hour)
	content.set(alex.ID.String(), append(coast.Entries, family.Entries...)...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	_, err = module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)

	// Rename, then permanently delete the referenced Album. The stored
	// summary keeps the reviewed title and counts.
	_, err = db.NewUpdate().Model((*models.Album)(nil)).Set("title = ?", "Renamed").Where("id = ?", coast.ID).Exec(t.Context())
	require.NoError(t, err)
	_, err = db.NewDelete().Model((*models.Album)(nil)).Where("id = ?", coast.ID).Exec(t.Context())
	require.NoError(t, err)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	require.Len(t, list.Notifications, 2)
	assert.Equal(t, 2, list.Unread)
	assert.Equal(t, "Family", list.Notifications[0].Albums[0].Title, "newest first")
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 1, VideoCount: 1}}, list.Notifications[1].Albums)
	assert.Equal(t, "First batch", list.Notifications[1].Note)
	assert.Equal(t, entryIDs(family.Entries...), announced(t, module, alex.ID.String()), "deleted Album Entries leave the baseline with their Album")

	// Reading is per notification and per Person. Marking twice keeps the first time.
	read, err := module.MarkRead(t.Context(), alex.ID.String(), list.Notifications[1].ID)
	require.NoError(t, err)
	require.NotNil(t, read.ReadAt)
	firstRead := *read.ReadAt
	clock = clock.Add(time.Hour)
	read, err = module.MarkRead(t.Context(), alex.ID.String(), list.Notifications[1].ID)
	require.NoError(t, err)
	assert.Equal(t, firstRead, *read.ReadAt)
	_, err = module.MarkRead(t.Context(), sam.ID.String(), list.Notifications[1].ID)
	require.ErrorIs(t, err, errcodes.NotFound("Notification"), "another Person's notification is invisible")
	_, err = module.MarkRead(t.Context(), alex.ID.String(), "nope")
	require.Error(t, err)
	list, err = module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, list.Unread)
	samList, err := module.ListNotifications(t.Context(), sam.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, samList.Unread, "Alex reading changes nothing for Sam")
	list, err = module.MarkAllRead(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 0, list.Unread)
	assert.Equal(t, firstRead, *list.Notifications[1].ReadAt)
	require.NotNil(t, list.Notifications[0].ReadAt)
	samList, err = module.ListNotifications(t.Context(), sam.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, samList.Unread)
}
