package publishing_test

import (
	"context"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// announcing wires real Update Notifications to the fixture's Publishing the
// way main does, so Joins land in notification baselines.
func (f offerFixture) announcing(t *testing.T) *notifications.Module {
	t.Helper()
	updates := notifications.New(f.db, nil, nil, f.module, nil)
	f.module.Announcements = updates
	return updates
}

// onboard finishes Onboarding for the Person, recording their baseline the
// way Identity does.
func (f offerFixture) onboard(t *testing.T, updates *notifications.Module, personID string) {
	t.Helper()
	require.NoError(t, f.db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := updates.RecordBaseline(ctx, tx, personID); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*models.Person)(nil)).Set("onboarding_completed_at = ?", time.Now().UTC()).Where("id = ?", personID).Exec(ctx)
		return err
	}))
}

// pendingFor is the Person's row on the Curator's updates page, or nil when
// nothing is waiting for them.
func pendingFor(t *testing.T, updates *notifications.Module, personID string) *notifications.PreviewPerson {
	t.Helper()
	preview, err := updates.PreviewUpdates(t.Context())
	require.NoError(t, err)
	for _, row := range preview.People {
		if row.PersonID == personID {
			return &row
		}
	}
	return nil
}

func send(t *testing.T, updates *notifications.Module, row *notifications.PreviewPerson) notifications.Notification {
	t.Helper()
	approval, err := updates.ApproveUpdates(t.Context(), notifications.ApproveRequest{People: []notifications.ApprovePerson{{PersonID: row.PersonID, ReviewToken: row.ReviewToken}}})
	require.NoError(t, err)
	require.Equal(t, notifications.ResultNotified, approval.People[0].Status)
	list, err := updates.ListNotifications(t.Context(), row.PersonID)
	require.NoError(t, err)
	return list.Notifications[0]
}

func TestAnOfferedAlbumIsAnnouncedAsNewToViewOnce(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	updates := f.announcing(t)
	grandma := f.person(t, "Grandma")
	f.onboard(t, updates, grandma)
	extended := f.circle(t, "Extended family", grandma)
	later := f.album.Moments[1]
	f.offer(t, true, extended)
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionWithhold)
	f.publish(t)

	row := pendingFor(t, updates, grandma)
	require.NotNil(t, row)
	require.Empty(t, row.Albums, "nothing is Grandma's own yet")
	require.Equal(t, []notifications.NotificationAlbum{{ID: f.album.ID, Title: "Summer", Status: notifications.AlbumOffered, PhotoCount: 1}}, row.OfferedAlbums)
	notification := send(t, updates, row)
	require.Empty(t, notification.Albums)
	require.Equal(t, row.OfferedAlbums, notification.OfferedAlbums)
	require.Nil(t, pendingFor(t, updates, grandma))

	// Leaving, a withdrawn and restored Offer, and a later Moment Offer never
	// make the Album new to view again.
	require.NoError(t, f.module.JoinAlbum(t.Context(), grandma, f.album.ID))
	_, err := f.module.LeaveAlbum(t.Context(), grandma, f.album.ID)
	require.NoError(t, err)
	f.offer(t, false, extended)
	require.Nil(t, pendingFor(t, updates, grandma), "withdrawing an Offer sends nothing")
	f.offer(t, true, extended)
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionOffer)
	require.Nil(t, pendingFor(t, updates, grandma))

	// What Grandma joins is hers already; media offered after the Join is
	// news about one of her own Albums.
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionWithhold)
	require.NoError(t, f.module.JoinAlbum(t.Context(), grandma, f.album.ID))
	require.Nil(t, pendingFor(t, updates, grandma), "joining announces nothing")
	f.momentOffer(t, later.ID, extended, publishing.OfferDecisionOffer)
	row = pendingFor(t, updates, grandma)
	require.NotNil(t, row)
	require.Equal(t, []notifications.NotificationAlbum{{ID: f.album.ID, Title: "Summer", Status: notifications.AlbumUpdated, PhotoCount: len(later.Entries)}}, row.Albums)
	require.Empty(t, row.OfferedAlbums)

	// Removing Grandma from the Circle takes the Album away quietly.
	f.removeMember(t, extended, grandma)
	require.Nil(t, pendingFor(t, updates, grandma))
}

func TestAnAlbumWithPartialDirectAccessIsNewToViewWhenMoreIsOffered(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	updates := f.announcing(t)
	sam := f.person(t, "Sam")
	f.onboard(t, updates, sam)
	first := f.album.Moments[0]
	f.rule(t, sam, first.ID, "", publishing.DecisionAllow)
	f.publish(t)
	row := pendingFor(t, updates, sam)
	require.NotNil(t, row)
	require.Equal(t, []notifications.NotificationAlbum{{ID: f.album.ID, Title: "Summer", Status: notifications.AlbumNew, PhotoCount: 1}}, row.Albums)
	require.Empty(t, row.OfferedAlbums)
	send(t, updates, row)

	extended := f.circle(t, "Extended family", sam)
	f.offer(t, true, extended)
	row = pendingFor(t, updates, sam)
	require.NotNil(t, row, "adding Sam to an offered Circle shows the rest of the Album")
	require.Empty(t, row.Albums)
	require.Equal(t, []notifications.NotificationAlbum{{ID: f.album.ID, Title: "Summer", Status: notifications.AlbumOffered, PhotoCount: 2}}, row.OfferedAlbums, "only the offered photos count")
}

func TestOnboardingCoversAlbumsAlreadyOffered(t *testing.T) {
	t.Parallel()
	f := newOfferFixture(t)
	updates := f.announcing(t)
	newcomer := f.person(t, "Newcomer")
	f.circle(t, "Extended family", newcomer)
	f.offer(t, true, f.circle(t, "Everyone", newcomer))
	f.publish(t)
	f.onboard(t, updates, newcomer)
	require.Nil(t, pendingFor(t, updates, newcomer), "what was offered before Onboarding is their starting point")
}
