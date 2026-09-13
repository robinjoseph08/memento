package identity_test

import (
	"sort"
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnboardingIsSharedOneTimeAndCommitsBaselineAtomically(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, true)
	module := a.identity
	curator := claimCurator(t, module)
	require.Nil(t, curator.Person.OnboardingCompletedAt, "the first Curator also onboards")
	album := a.publishedAlbum(t)
	ids := entryIDs(album)
	require.Len(t, ids, 3)

	// Invited and directly preauthorized People enter the same flow.
	invited, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Invited"})
	require.NoError(t, err)
	invitedApproval, err := module.Preauthorize(t.Context(), curator.Token, invited.ID, identity.PreauthorizeRequest{Email: "invited@example.test"})
	require.NoError(t, err)
	_, err = module.SendInvitation(t.Context(), curator.Token, invited.ID, identity.SendInvitationRequest{PreauthorizationID: invitedApproval.ID})
	require.NoError(t, err)
	a.deliverAll(t)
	require.Len(t, a.recorder.Sent(), 1)
	direct := authorizePerson(t, module, curator, "Direct", "direct@example.test")
	invitedSession, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "invited@example.test", DisplayName: "Invited"}))
	require.NoError(t, err)
	for _, session := range []identity.Session{invitedSession, direct} {
		assert.Nil(t, session.Person.OnboardingCompletedAt)
		profile, err := module.Profile(t.Context(), session.Token)
		require.NoError(t, err)
		assert.Len(t, profile.Identities, 1)
	}

	// Entry A is visible to Direct before completion; nothing is visible to Invited.
	_, err = a.albums.SaveEntryRules(t.Context(), album.ID, ids[0], publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: direct.Person.ID, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	request := identity.UpdateProfileRequest{DisplayName: "Direct Confirmed", UpdateEmail: "direct@example.test", EmailUpdates: false}
	_, err = module.CompleteOnboarding(t.Context(), direct.Token, identity.UpdateProfileRequest{DisplayName: "Direct", UpdateEmail: "someone-else@example.test", EmailUpdates: true})
	require.Error(t, err, "an unlinked email is rejected")
	incomplete, err := module.Authenticate(t.Context(), direct.Token)
	require.NoError(t, err)
	require.Nil(t, incomplete.Person.OnboardingCompletedAt, "a rejected completion changes nothing")
	completed, err := module.CompleteOnboarding(t.Context(), direct.Token, request)
	require.NoError(t, err)
	require.NotNil(t, completed.OnboardingCompletedAt)
	assert.Equal(t, "Direct Confirmed", completed.DisplayName)
	assert.Equal(t, "direct@example.test", completed.UpdateEmail)
	assert.False(t, completed.EmailUpdates)
	announced, err := a.mail.AnnouncedEntryIDs(t.Context(), direct.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{ids[0]}, announced)
	detail, err := module.GetPerson(t.Context(), curator.Token, direct.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, identity.AnnouncedContent{Albums: 1, Entries: 1}, detail.Announced)

	// Zero visible Albums still completes with an empty baseline.
	completedInvited, err := module.CompleteOnboarding(t.Context(), invitedSession.Token, identity.UpdateProfileRequest{DisplayName: "Invited", UpdateEmail: "", EmailUpdates: false})
	require.NoError(t, err)
	require.NotNil(t, completedInvited.OnboardingCompletedAt)
	invitedDetail, err := module.GetPerson(t.Context(), curator.Token, invited.ID)
	require.NoError(t, err)
	assert.Equal(t, identity.AnnouncedContent{}, invitedDetail.Announced)

	// Granting B afterwards, linking a second identity, replaying completion,
	// and signing in again never advance the completed baseline.
	_, err = a.albums.SaveEntryRules(t.Context(), album.ID, ids[1], publishing.SaveRulesRequest{Decisions: []publishing.AccessResolution{{PersonID: direct.Person.ID, Decision: publishing.DecisionAllow}}})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, direct.Person.ID, identity.PreauthorizeRequest{Email: "direct-second@example.test"})
	require.NoError(t, err)
	second, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "direct-second@example.test", DisplayName: "Other name"}))
	require.NoError(t, err)
	assert.Equal(t, completed.OnboardingCompletedAt, second.Person.OnboardingCompletedAt)
	replay, err := module.CompleteOnboarding(t.Context(), second.Token, identity.UpdateProfileRequest{DisplayName: "Replayed", UpdateEmail: "direct-second@example.test", EmailUpdates: true})
	require.NoError(t, err)
	assert.Equal(t, completed, replay, "replaying completion returns the existing Person untouched")
	returning, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "direct@example.test", DisplayName: "Direct"}))
	require.NoError(t, err)
	assert.Equal(t, completed.OnboardingCompletedAt, returning.Person.OnboardingCompletedAt)
	announced, err = a.mail.AnnouncedEntryIDs(t.Context(), direct.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{ids[0]}, announced, "B stays unannounced for a later Update Notification")

	// The Curator's bypass never becomes a viewer announcement.
	curatorDone, err := module.CompleteOnboarding(t.Context(), curator.Token, identity.UpdateProfileRequest{DisplayName: "Curator", UpdateEmail: "curator@example.test", EmailUpdates: true})
	require.NoError(t, err)
	require.NotNil(t, curatorDone.OnboardingCompletedAt)
	curatorDetail, err := module.GetPerson(t.Context(), curator.Token, curator.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, identity.AnnouncedContent{}, curatorDetail.Announced)
}

func TestConcurrentOnboardingCompletionsRecordOneBaseline(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, true)
	module := a.identity
	curator := claimCurator(t, module)
	album := a.publishedAlbum(t)
	person := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := a.albums.SaveAlbumAccess(t.Context(), album.ID, publishing.SaveAlbumAccessRequest{People: []publishing.AlbumAccessChoice{{PersonID: person.Person.ID, Allowed: true}}})
	require.NoError(t, err)
	const attempts = 6
	results := make([]identity.Person, attempts)
	errs := make([]error, attempts)
	operations := make([]func(), attempts)
	for i := range attempts {
		operations[i] = func() {
			results[i], errs[i] = module.CompleteOnboarding(t.Context(), person.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "alex@example.test", EmailUpdates: true})
		}
	}
	raceIdentityOperations(operations...)
	for i := range attempts {
		require.NoError(t, errs[i])
		assert.Equal(t, results[0].OnboardingCompletedAt, results[i].OnboardingCompletedAt)
	}
	announced, err := a.mail.AnnouncedEntryIDs(t.Context(), person.Person.ID)
	require.NoError(t, err)
	expected := entryIDs(album)
	sort.Strings(expected)
	assert.Equal(t, expected, announced)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, identity.AnnouncedContent{Albums: 1, Entries: 3}, detail.Announced)
}

func TestOnboardingWithoutAnnouncementStorageFailsClearly(t *testing.T) {
	t.Parallel()
	a := newAdmission(t, true)
	a.identity.Announcements = nil
	curator := claimCurator(t, a.identity)
	_, err := a.identity.CompleteOnboarding(t.Context(), curator.Token, identity.UpdateProfileRequest{DisplayName: "Curator"})
	require.Error(t, err)
	session, err := a.identity.Authenticate(t.Context(), curator.Token)
	require.NoError(t, err)
	assert.Nil(t, session.Person.OnboardingCompletedAt)
}
