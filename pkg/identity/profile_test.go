package identity_test

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func authorizePerson(t *testing.T, module *identity.Module, curator identity.Session, name, email string) identity.Session {
	t.Helper()
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: name})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: email})
	require.NoError(t, err)
	session, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: email, DisplayName: name}))
	require.NoError(t, err)
	return session
}

func TestFirstLinkedIdentityEnablesEmailUpdates(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	assert.Equal(t, "curator@example.test", curator.Person.UpdateEmail)
	assert.True(t, curator.Person.EmailUpdates)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	assert.Empty(t, person.UpdateEmail)
	assert.False(t, person.EmailUpdates)
	_, err = module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	member, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	assert.Equal(t, "alex@example.test", member.Person.UpdateEmail)
	assert.True(t, member.Person.EmailUpdates)
	for _, session := range []identity.Session{curator, member} {
		profile, err := module.Profile(t.Context(), session.Token)
		require.NoError(t, err)
		assert.Equal(t, session.Person, profile.Person)
		authenticated, err := module.Authenticate(t.Context(), session.Token)
		require.NoError(t, err)
		assert.Equal(t, session.Person, authenticated.Person)
		detail, err := module.GetPerson(t.Context(), curator.Token, session.Person.ID)
		require.NoError(t, err)
		assert.Equal(t, session.Person, detail.Person)
	}
}

func TestLaterSignInsAndIdentityLinksPreserveEmailOptOut(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	optedOut, err := module.UpdateProfile(t.Context(), member.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "alex@example.test", EmailUpdates: false})
	require.NoError(t, err)
	claims := identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"})
	returning, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, optedOut.Person, returning.Person)
	_, err = module.Preauthorize(t.Context(), curator.Token, member.Person.ID, identity.PreauthorizeRequest{Email: "second@example.test"})
	require.NoError(t, err)
	second, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "second@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	assert.Equal(t, optedOut.Person, second.Person)
	profile, err := module.Profile(t.Context(), second.Token)
	require.NoError(t, err)
	assert.Equal(t, optedOut.Person, profile.Person)

	// A Curator can remove every account. Their history still prevents defaults
	// from being applied when an old subject returns or a new account is added.
	for _, linked := range profile.Identities {
		require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, member.Person.ID, linked.ID))
	}
	_, err = module.Preauthorize(t.Context(), curator.Token, member.Person.ID, identity.PreauthorizeRequest{Email: claims.Email})
	require.NoError(t, err)
	relinked, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.False(t, relinked.Person.EmailUpdates)
	assert.Empty(t, relinked.Person.UpdateEmail)
	profile, err = module.Profile(t.Context(), relinked.Token)
	require.NoError(t, err)
	require.Len(t, profile.Identities, 1)
	require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, member.Person.ID, profile.Identities[0].ID))
	_, err = module.Preauthorize(t.Context(), curator.Token, member.Person.ID, identity.PreauthorizeRequest{Email: "third@example.test"})
	require.NoError(t, err)
	third, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "third@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	assert.False(t, third.Person.EmailUpdates)
	assert.Empty(t, third.Person.UpdateEmail)
	persisted, err := module.Profile(t.Context(), third.Token)
	require.NoError(t, err)
	assert.Equal(t, third.Person, persisted.Person)
}

func TestSelectedIdentityEmailChangeClearsEmailUpdates(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	claims := identity.FakeClaims(identity.SignInRequest{Email: "curator@example.test", DisplayName: "Curator"})
	claims.Email = "changed@example.test"
	changed, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Empty(t, changed.Person.UpdateEmail)
	assert.False(t, changed.Person.EmailUpdates)
	profile, err := module.Profile(t.Context(), curator.Token)
	require.NoError(t, err)
	assert.Equal(t, changed.Person, profile.Person)
	require.Len(t, profile.Identities, 1)
	assert.Equal(t, "changed@example.test", profile.Identities[0].Email)
}

func TestProfileAndMultipleIdentities(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := module.Preauthorize(t.Context(), curator.Token, first.Person.ID, identity.PreauthorizeRequest{Email: "second@example.test"})
	require.NoError(t, err)
	now = now.Add(400 * 24 * time.Hour)
	// Preauthorizations have no automatic expiry, even after all old sessions expire.
	second, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "second@example.test", DisplayName: "Other name"}))
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, second.Person.ID)
	profile, err := module.Profile(t.Context(), second.Token)
	require.NoError(t, err)
	require.Len(t, profile.Identities, 2)
	_, err = module.UpdateProfile(t.Context(), second.Token, identity.UpdateProfileRequest{DisplayName: "New Alex", UpdateEmail: "arbitrary@example.test", EmailUpdates: true})
	require.Error(t, err)
	profile, err = module.UpdateProfile(t.Context(), second.Token, identity.UpdateProfileRequest{DisplayName: "New Alex", UpdateEmail: "second@example.test", EmailUpdates: true})
	require.NoError(t, err)
	assert.Equal(t, "New Alex", profile.Person.DisplayName)
	assert.Equal(t, "second@example.test", profile.Person.UpdateEmail)
	assert.True(t, profile.Person.EmailUpdates)
	current, err := module.Authenticate(t.Context(), second.Token)
	require.NoError(t, err)
	assert.Equal(t, profile.Person, current.Person)
}

func TestUnselectedIdentityEmailChangeKeepsNotificationPreference(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := module.Preauthorize(t.Context(), curator.Token, member.Person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	secondClaims := identity.Claims{Provider: "google", Subject: "another-subject", Email: "alex@example.test", EmailVerified: true, DisplayName: "Alex"}
	_, err = module.SignIn(t.Context(), secondClaims)
	require.NoError(t, err)
	before, err := module.UpdateProfile(t.Context(), member.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "alex@example.test", EmailUpdates: true})
	require.NoError(t, err)
	secondClaims.Email = "new@example.test"
	_, err = module.SignIn(t.Context(), secondClaims)
	require.NoError(t, err)
	after, err := module.Profile(t.Context(), member.Token)
	require.NoError(t, err)
	assert.Equal(t, before.Person, after.Person)
}

func TestLastCuratorKeepsASignInAccount(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	profile, err := module.Profile(t.Context(), curator.Token)
	require.NoError(t, err)
	require.Len(t, profile.Identities, 1)
	err = module.UnlinkIdentity(t.Context(), curator.Token, "", profile.Identities[0].ID)
	require.ErrorIs(t, err, identity.ErrLastAccount)
	// A role assigned to a name-only Person cannot recover a locked installation.
	nameOnly, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "No login"})
	require.NoError(t, err)
	_, err = module.UpdatePerson(t.Context(), curator.Token, nameOnly.ID, identity.UpdatePersonRequest{DisplayName: "No login", IsCurator: true})
	require.NoError(t, err)
	_, err = module.UpdatePerson(t.Context(), curator.Token, curator.Person.ID, identity.UpdatePersonRequest{DisplayName: "Curator"})
	requirePersonFieldError(t, err, "is_curator")
	err = module.UnlinkIdentity(t.Context(), curator.Token, "", profile.Identities[0].ID)
	require.ErrorIs(t, err, identity.ErrLastAccount)
	other := authorizePerson(t, module, curator, "Other", "other@example.test")
	_, err = module.UpdatePerson(t.Context(), curator.Token, other.Person.ID, identity.UpdatePersonRequest{DisplayName: "Other", IsCurator: true})
	require.NoError(t, err)
	for _, personID := range []string{"", curator.Person.ID} {
		err := module.UnlinkIdentity(t.Context(), curator.Token, personID, profile.Identities[0].ID)
		require.ErrorIs(t, err, identity.ErrLastAccount)
	}
	require.NoError(t, module.UnlinkIdentity(t.Context(), other.Token, curator.Person.ID, profile.Identities[0].ID))
	_, err = module.Authenticate(t.Context(), curator.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	_, err = module.Authenticate(t.Context(), other.Token)
	require.NoError(t, err)
	// The remaining usable Curator also cannot demote themselves into a lockout.
	_, err = module.UpdatePerson(t.Context(), other.Token, other.Person.ID, identity.UpdatePersonRequest{DisplayName: "Other"})
	requirePersonFieldError(t, err, "is_curator")
}

func TestMemberKeepsLastAccountUnlessAnotherCuratorUnlinksIt(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	member := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	profile, err := module.Profile(t.Context(), member.Token)
	require.NoError(t, err)
	require.Len(t, profile.Identities, 1)
	id := profile.Identities[0].ID
	err = module.UnlinkIdentity(t.Context(), member.Token, "", id)
	require.ErrorIs(t, err, identity.ErrLastAccount)
	_, err = module.Authenticate(t.Context(), member.Token)
	require.NoError(t, err)
	err = module.UnlinkIdentity(t.Context(), member.Token, member.Person.ID, id)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	err = module.UnlinkIdentity(t.Context(), member.Token, curator.Person.ID, id)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, member.Person.ID, id))
	detail, err := module.GetPerson(t.Context(), curator.Token, member.Person.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Identities)
	assert.Empty(t, detail.Sessions)
	_, err = module.Authenticate(t.Context(), member.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}

func TestIdentityUnlinkAndSessions(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := module.Preauthorize(t.Context(), curator.Token, first.Person.ID, identity.PreauthorizeRequest{Email: "second@example.test"})
	require.NoError(t, err)
	claims := identity.FakeClaims(identity.SignInRequest{Email: "second@example.test", DisplayName: "Alex"})
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	sessions, err := module.Sessions(t.Context(), first.Token)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	assert.True(t, sessions[0].Current)
	assert.False(t, sessions[1].Current)
	assert.NotEqual(t, sessions[0].ID, sessions[1].ID)
	_, err = module.UpdateProfile(t.Context(), first.Token, identity.UpdateProfileRequest{DisplayName: "Alex", UpdateEmail: "second@example.test", EmailUpdates: true})
	require.NoError(t, err)
	require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, first.Person.ID, sessions[1].IdentityID))
	_, err = module.Authenticate(t.Context(), second.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	remaining, err := module.Authenticate(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Empty(t, remaining.Person.UpdateEmail)
	assert.False(t, remaining.Person.EmailUpdates)
	sessions, err = module.Sessions(t.Context(), first.Token)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	// Explicit preauthorization can relink the same subject to the same Person.
	_, err = module.Preauthorize(t.Context(), curator.Token, first.Person.ID, identity.PreauthorizeRequest{Email: claims.Email})
	require.NoError(t, err)
	relinked, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, relinked.Person.ID)
	require.NoError(t, module.SignOutEverywhere(t.Context(), first.Token))
	for _, token := range []string{first.Token, relinked.Token} {
		_, err = module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
	_, err = module.Authenticate(t.Context(), curator.Token)
	require.NoError(t, err)
}
