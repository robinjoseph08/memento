package identity_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pendingOffers is the Albums new to view on the Curator's updates page for
// the Person, or nil when nothing is waiting for them.
func pendingOffers(t *testing.T, a *admission, personID string) []notifications.NotificationAlbum {
	t.Helper()
	preview, err := a.mail.PreviewUpdates(t.Context())
	require.NoError(t, err)
	for _, row := range preview.People {
		if row.PersonID == personID {
			return row.OfferedAlbums
		}
	}
	return nil
}

func TestAlbumsICanJoinIsEditedEverywhereAndStartsFreshWhenTurnedBackOn(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, false)
	module := a.identity
	curator := claimCurator(t, module)
	album := a.publishedAlbum(t)

	// Everyone starts with it on, and Onboarding saves their choice.
	people := map[string]identity.Session{}
	for name, on := range map[string]bool{"Pat": false, "Sam": true, "Kim": true} {
		session := authorizePerson(t, module, curator, name, name+"@example.test")
		assert.True(t, session.Person.OfferedAlbumUpdates)
		completed, err := module.CompleteOnboarding(t.Context(), session.Token, identity.UpdateProfileRequest{DisplayName: name, OfferedAlbumUpdates: on})
		require.NoError(t, err)
		assert.Equal(t, on, completed.OfferedAlbumUpdates)
		people[name] = session
	}
	pat, sam, kim := people["Pat"], people["Sam"], people["Kim"]

	// Kim turns it off in the profile.
	profile, err := module.UpdateProfile(t.Context(), kim.Token, identity.UpdateProfileRequest{DisplayName: "Kim", OfferedAlbumUpdates: false})
	require.NoError(t, err)
	assert.False(t, profile.Person.OfferedAlbumUpdates)

	circle, err := module.CreateCircle(t.Context(), curator.Token, identity.CircleRequest{Name: "Family"})
	require.NoError(t, err)
	_, err = module.SetCircleMembers(t.Context(), curator.Token, circle.ID, identity.CircleMembersRequest{PersonIDs: []string{pat.Person.ID, sam.Person.ID, kim.Person.ID}})
	require.NoError(t, err)
	_, err = a.albums.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{}, Circles: []publishing.AlbumOfferChoice{{CircleID: circle.ID, Offered: true}}})
	require.NoError(t, err)
	require.Len(t, pendingOffers(t, a, sam.Person.ID), 1, "Sam hears about the offered Album")
	assert.Nil(t, pendingOffers(t, a, pat.Person.ID))
	assert.Nil(t, pendingOffers(t, a, kim.Person.ID))

	// Pat turns it back on in the profile, and a Curator does it for Kim on
	// the person-details page. What was offered meanwhile stays unsent.
	profile, err = module.UpdateProfile(t.Context(), pat.Token, identity.UpdateProfileRequest{DisplayName: "Pat", OfferedAlbumUpdates: true})
	require.NoError(t, err)
	assert.True(t, profile.Person.OfferedAlbumUpdates)
	updated, err := module.UpdatePerson(t.Context(), curator.Token, kim.Person.ID, identity.UpdatePersonRequest{DisplayName: "Kim", OfferedAlbumUpdates: true})
	require.NoError(t, err)
	assert.True(t, updated.OfferedAlbumUpdates)
	detail, err := module.GetPerson(t.Context(), curator.Token, kim.Person.ID)
	require.NoError(t, err)
	assert.True(t, detail.Person.OfferedAlbumUpdates)
	assert.Nil(t, pendingOffers(t, a, pat.Person.ID))
	assert.Nil(t, pendingOffers(t, a, kim.Person.ID))

	// Saving it on again keeps what is pending, and a Curator can turn it off.
	_, err = module.UpdateProfile(t.Context(), sam.Token, identity.UpdateProfileRequest{DisplayName: "Sam", OfferedAlbumUpdates: true})
	require.NoError(t, err)
	require.Len(t, pendingOffers(t, a, sam.Person.ID), 1)
	updated, err = module.UpdatePerson(t.Context(), curator.Token, sam.Person.ID, identity.UpdatePersonRequest{DisplayName: "Sam", OfferedAlbumUpdates: false})
	require.NoError(t, err)
	assert.False(t, updated.OfferedAlbumUpdates)
	assert.Nil(t, pendingOffers(t, a, sam.Person.ID))
}
