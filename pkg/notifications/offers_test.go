package notifications_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func offered(album seededAlbum, photos, videos int) notifications.OfferedAlbum {
	return notifications.OfferedAlbum{AlbumID: album.ID, AlbumTitle: album.Title, PhotoCount: photos, VideoCount: videos}
}

func newToView(album seededAlbum, photos, videos int) notifications.NotificationAlbum {
	return notifications.NotificationAlbum{ID: album.ID, Title: album.Title, Status: notifications.AlbumOffered, PhotoCount: photos, VideoCount: videos}
}

func TestAlbumsNewToViewAreAnnouncedOnceBesideOwnAlbums(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	content.set(alex.ID.String(), coast.Entries...)
	content.offer(alex.ID.String(), offered(wedding, 40, 2), offered(party, 3, 0))

	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	row := preview.People[0]
	assert.Equal(t, []notifications.NotificationAlbum{{ID: coast.ID, Title: "Coast", Status: notifications.AlbumNew, PhotoCount: 1}}, row.Albums)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(wedding, 40, 2), newToView(party, 3, 0)}, row.OfferedAlbums, "newest first, as More albums lists them")

	// A Curator can leave one Album out of the section; it stays pending.
	request := approveAll(preview, "", nil)
	request.People[0].ExcludedOfferedAlbumIDs = []string{party.ID}
	approval, err := module.ApproveUpdates(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultNotified, approval.People[0].Status)
	assert.Equal(t, 1, approval.People[0].AlbumCount)
	assert.Equal(t, 1, approval.People[0].OfferedAlbumCount)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	require.Len(t, list.Notifications, 1)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(wedding, 40, 2)}, list.Notifications[0].OfferedAlbums)

	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Empty(t, preview.People[0].Albums)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(party, 3, 0)}, preview.People[0].OfferedAlbums)

	// Offering more of an announced Album, or offering it again after it was
	// gone, never makes it new to view again.
	content.offer(alex.ID.String(), offered(party, 3, 0), offered(wedding, 60, 2))
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(party, 3, 0)}, preview.People[0].OfferedAlbums)

	// The review covers which Albums are new to view.
	stale := approveAll(preview, "", nil)
	content.offer(alex.ID.String(), offered(party, 3, 0), offered(coast, 5, 0))
	approval, err = module.ApproveUpdates(t.Context(), stale)
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultSkipped, approval.People[0].Status)
	assert.Contains(t, approval.People[0].Message, "changed since this preview")

	// Excluding everything sends nothing.
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	request = approveAll(preview, "", nil)
	request.People[0].ExcludedOfferedAlbumIDs = []string{party.ID, coast.ID}
	approval, err = module.ApproveUpdates(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultSkipped, approval.People[0].Status)
	assert.Contains(t, approval.People[0].Message, "excluded")
}

func TestManyAlbumsNewToViewCollapseToTheNewestFew(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	albums := []notifications.OfferedAlbum{}
	for i := range 8 {
		album := seedAlbum(t, db, fmt.Sprintf("Album %d", i+1), "photo.jpg")
		albums = append(albums, offered(album, i+1, 0))
	}
	content.offer(alex.ID.String(), albums...)

	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	row := preview.People[0]
	require.Len(t, row.OfferedAlbums, 8, "the Curator sees every Album so any can be left out")
	assert.Equal(t, "Album 1", row.OfferedAlbums[0].Title)
	assert.Equal(t, "Album 8", row.OfferedAlbums[7].Title)

	// Leaving out a newer and an older Album; the notification shows the
	// newest five of the rest.
	request := approveAll(preview, "", nil)
	request.People[0].ExcludedOfferedAlbumIDs = []string{albums[1].AlbumID, albums[7].AlbumID}
	approval, err := module.ApproveUpdates(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, 6, approval.People[0].OfferedAlbumCount)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	notification := list.Notifications[0]
	require.Len(t, notification.OfferedAlbums, 5)
	assert.Equal(t, "Album 6", notification.OfferedAlbums[4].Title)
	assert.Equal(t, 1, notification.MoreOfferedAlbums)
	assert.Empty(t, notification.Albums)

	require.NoError(t, module.Execute(t.Context(), approval.People[0].Delivery.ID, false))
	require.Len(t, recorder.Sent(), 1)
	sent := recorder.Sent()[0]
	assert.Equal(t, "6 new albums you can view on Memento", sent.Subject)
	assert.Contains(t, sent.Body, "There is something new for you to see in 6 albums on Memento.\n")
	assert.Contains(t, sent.Body, "New albums you can view:\n\nAlbum 1\n1 photo\n\nAlbum 3\n3 photos\n")
	assert.Contains(t, sent.Body, "Album 6\n6 photos\n\nAnd 1 more album.\n")
	assert.NotContains(t, sent.Body, "Album 7")
	assert.Contains(t, sent.Body, "See them on Memento:\n"+publicURL+"/albums\n")

	// Every Album, shown or collapsed, was announced; the excluded ones wait.
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	require.Len(t, preview.People[0].OfferedAlbums, 2)
	assert.Equal(t, "Album 2", preview.People[0].OfferedAlbums[0].Title)
	assert.Equal(t, "Album 8", preview.People[0].OfferedAlbums[1].Title)
}

func TestUpdateEmailForOneAlbumNewToViewOpensIt(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.offer(alex.ID.String(), offered(wedding, 40, 2))
	result := approveOne(t, module, "")
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	sent := recorder.Sent()[0]
	assert.Equal(t, "You can now view Wedding on Memento", sent.Subject)
	assert.Contains(t, sent.Body, "New albums you can view:\n\nWedding\n40 photos and 2 videos\n")
	assert.Contains(t, sent.Body, "See them on Memento:\n"+publicURL+"/albums/"+wedding.ID+"/preview/photos\n")

	// An Album no longer offered by the time the email goes out is dropped,
	// and an update left with nothing is skipped.
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	content.offer(alex.ID.String(), offered(party, 3, 0))
	result = approveOne(t, module, "")
	content.offer(alex.ID.String())
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	delivery := status(t, db, module, result.Delivery.ID)
	assert.Equal(t, "skipped", delivery.Status)
	assert.Contains(t, delivery.Message, "any more")

	// Joining it since approval keeps it in the email.
	beach := seedAlbum(t, db, "Beach", "beach-01.jpg")
	content.offer(alex.ID.String(), offered(beach, 1, 0))
	result = approveOne(t, module, "")
	content.offer(alex.ID.String())
	content.set(alex.ID.String(), beach.Entries...)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	require.Len(t, recorder.Sent(), 2)
	assert.Equal(t, "You can now view Beach on Memento", recorder.Sent()[1].Subject)
}

func TestDismissingAdvancesAlbumsNewToView(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	content.offer(alex.ID.String(), offered(wedding, 1, 0), offered(party, 1, 0))
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	request := notifications.DismissRequest{People: []notifications.ApprovePerson{{PersonID: alex.ID.String(), ReviewToken: preview.People[0].ReviewToken, ExcludedOfferedAlbumIDs: []string{party.ID}}}}
	result, err := module.DismissUpdates(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultDismissed, result.People[0].Status)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	assert.Empty(t, list.Notifications)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(party, 1, 0)}, preview.People[0].OfferedAlbums, "only the unchecked Album stays pending")
}

func TestBaselineAndJoinsRecordAlbumsAsAlreadyNewToView(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg", "wedding-02.jpg")
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	content.offer(alex.ID.String(), offered(wedding, 2, 0))
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.RecordBaseline(ctx, tx, alex.ID.String())
		return err
	}))
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People, "Onboarding covers what was offered")

	// Joining announces the joined media and the Album; only what is added
	// afterwards is news, as an update to one of Alex's own Albums.
	content.offer(alex.ID.String(), offered(party, 1, 0))
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		return module.RecordJoin(ctx, tx, alex.ID.String(), party.ID, entryIDs(party.Entries...))
	}))
	content.offer(alex.ID.String())
	content.set(alex.ID.String(), party.Entries...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People, "joining announces nothing")
	content.offer(alex.ID.String(), offered(party, 1, 0))
	content.set(alex.ID.String())
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People, "a left Album is never new to view again")

	content.set(alex.ID.String(), append(party.Entries, wedding.Entries[0])...)
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, []notifications.NotificationAlbum{{ID: wedding.ID, Title: "Wedding", Status: notifications.AlbumNew, PhotoCount: 1}}, preview.People[0].Albums)
	data, err := json.Marshal(preview.People[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `"offered_albums":[]`, "an empty section is a list, never null")
}

func TestNewerMediaReorderingAlbumsNewToViewNeedsNoNewReview(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module := notifications.New(db, nil, nil, content, nil)
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{})
	content.offer(alex.ID.String(), offered(wedding, 1, 0), offered(party, 1, 0))
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	content.offer(alex.ID.String(), offered(party, 2, 0), offered(wedding, 1, 0))
	approval, err := module.ApproveUpdates(t.Context(), approveAll(preview, "", nil))
	require.NoError(t, err)
	assert.Equal(t, notifications.ResultNotified, approval.People[0].Status)
}

func TestUpdateEmailCountsAnAlbumInBothSectionsOnceAndDropsWithdrawnOffers(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries...)
	content.offer(alex.ID.String(), offered(coast, 4, 0))
	result := approveOne(t, module, "")
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	sent := recorder.Sent()[0]
	assert.Contains(t, sent.Body, "There is something new for you to see on Memento.\n", "one Album, even in both sections")
	assert.Contains(t, sent.Body, "Coast (new album)\n1 photo\n\nNew albums you can view:\n\nCoast\n4 photos\n")

	// Every listed Album new to view is withdrawn before sending, so the
	// collapsed count goes with them and only own Albums remain.
	family := seedAlbum(t, db, "Family", "family-01.jpg")
	content.set(alex.ID.String(), append(coast.Entries, family.Entries...)...)
	albums := []notifications.OfferedAlbum{}
	for i := range 6 {
		albums = append(albums, offered(seedAlbum(t, db, fmt.Sprintf("Offered %d", i+1), "photo.jpg"), 1, 0))
	}
	content.offer(alex.ID.String(), albums...)
	result = approveOne(t, module, "")
	content.offer(alex.ID.String(), albums[5])
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	sent = recorder.Sent()[1]
	assert.Equal(t, "Family was shared with you on Memento", sent.Subject)
	assert.Contains(t, sent.Body, "There is something new for you to see on Memento.\n")
	assert.NotContains(t, sent.Body, "New albums you can view")
	assert.NotContains(t, sent.Body, "more album")
}

func TestTurningOffAlbumsICanJoinLeavesThemOutOfEveryChannel(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true, offersOff: true})
	sam := seedPerson(t, db, "Sam", personOptions{offersOff: true})
	content.set(alex.ID.String(), coast.Entries...)
	content.offer(alex.ID.String(), offered(wedding, 40, 2))
	content.offer(sam.ID.String(), offered(wedding, 40, 2), offered(party, 3, 0))

	// Only Alex's own Album is pending, and Sam, with nothing else new, has
	// no update at all.
	result := approveOne(t, module, "")
	assert.Equal(t, notifications.ResultNotified, result.Status)
	assert.Equal(t, 1, result.AlbumCount)
	assert.Zero(t, result.OfferedAlbumCount)
	list, err := module.ListNotifications(t.Context(), alex.ID.String())
	require.NoError(t, err)
	require.Len(t, list.Notifications, 1)
	assert.Empty(t, list.Notifications[0].OfferedAlbums)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	assert.NotContains(t, recorder.Sent()[0].Body, "New albums you can view")
	preview, err := module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People)

	// Turning it back on records what is offered now as already announced,
	// so only Albums offered afterwards are news.
	for _, person := range []models.Person{alex, sam} {
		person.OfferedAlbumUpdates = true
		require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewUpdate().Model(&person).Column("offered_album_updates").WherePK().Exec(ctx); err != nil {
				return err
			}
			return module.RecordOfferedAlbums(ctx, tx, person.ID.String())
		}))
	}
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	assert.Empty(t, preview.People, "nothing offered while it was off is sent")
	beach := seedAlbum(t, db, "Beach", "beach-01.jpg")
	content.offer(sam.ID.String(), offered(beach, 1, 0), offered(wedding, 40, 2), offered(party, 3, 0))
	preview, err = module.PreviewUpdates(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	assert.Equal(t, []notifications.NotificationAlbum{newToView(beach, 1, 0)}, preview.People[0].OfferedAlbums)
}

func TestUpdateEmailDropsAlbumsNewToViewTurnedOffSinceApproval(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	content := &mutableContent{visible: map[string][]notifications.VisibleEntry{}}
	module, recorder, _ := mailModule(t, db, content)
	coast := seedAlbum(t, db, "Coast", "coast-01.jpg")
	wedding := seedAlbum(t, db, "Wedding", "wedding-01.jpg")
	alex := seedPerson(t, db, "Alex", personOptions{email: "alex@example.test", emailUpdates: true})
	content.set(alex.ID.String(), coast.Entries...)
	content.offer(alex.ID.String(), offered(wedding, 40, 2))
	result := approveOne(t, module, "")
	_, err := db.NewUpdate().Model((*models.Person)(nil)).Set("offered_album_updates = false").Where("id = ?", alex.ID).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	require.Len(t, recorder.Sent(), 1)
	assert.Contains(t, recorder.Sent()[0].Body, "Coast")
	assert.NotContains(t, recorder.Sent()[0].Body, "New albums you can view")

	// An update of only Albums new to view is skipped, saying why.
	party := seedAlbum(t, db, "Party", "party-01.jpg")
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("offered_album_updates = true").Where("id = ?", alex.ID).Exec(t.Context())
	require.NoError(t, err)
	content.offer(alex.ID.String(), offered(party, 1, 0))
	result = approveOne(t, module, "")
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("offered_album_updates = false").Where("id = ?", alex.ID).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), result.Delivery.ID, false))
	delivery := status(t, db, module, result.Delivery.ID)
	assert.Equal(t, "skipped", delivery.Status)
	assert.Equal(t, "This person turned off hearing about albums they can join.", delivery.Message)
	assert.Len(t, recorder.Sent(), 1)
}
