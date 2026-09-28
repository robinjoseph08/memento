package identity_test

import (
	"fmt"
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:tparallel // Rejected claims must finish before the parent consumes their shared approval.
func TestPreauthorizationRaceLinksOneEmail(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)

	for _, scenario := range []struct {
		name   string
		claims identity.Claims
		err    error
	}{
		{name: "unverified exact email", claims: identity.Claims{Email: "alex@example.test", DisplayName: "Alex"}, err: identity.ErrUnverifiedEmail},
		{name: "matching name only", claims: identity.Claims{Email: "stranger@example.test", EmailVerified: true, DisplayName: "Alex"}, err: identity.ErrAccessRequested},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := module.SignIn(t.Context(), scenario.claims)
			require.ErrorIs(t, err, scenario.err)
		})
	}

	// Simultaneous sign-ins with the approved address all reach the same
	// Person through one Linked Email, whichever of them consumed the approval.
	const attempts = 8
	sessions := make([]identity.Session, attempts)
	errs := make([]error, attempts)
	operations := make([]func(), attempts)
	for i := range attempts {
		operations[i] = func() {
			sessions[i], errs[i] = module.SignIn(t.Context(), identity.Claims{Email: fmt.Sprintf("%s@Example.test", []string{"alex", "Alex", "ALEX"}[i%3]), EmailVerified: true, DisplayName: "Provider name"})
		}
	}
	raceIdentityOperations(operations...)
	for i := range attempts {
		require.NoError(t, errs[i])
		assert.Equal(t, person.ID, sessions[i].Person.ID)
		assert.Equal(t, "Alex", sessions[i].Person.DisplayName)
	}
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Emails, 1)
	assert.Equal(t, "alex@example.test", detail.Emails[0].Email)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
	assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)
	assert.Nil(t, detail.Person.OnboardingCompletedAt)
	assert.Len(t, detail.Sessions, attempts)

	// A different address is a stranger, whatever name it reports.
	_, err = module.SignIn(t.Context(), identity.Claims{Email: "changed@example.test", EmailVerified: true, DisplayName: "Alex"})
	require.ErrorIs(t, err, identity.ErrAccessRequested)
}

func TestPreauthorizationRaceCannotAuthorizeCompetingPeople(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	first, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "First"})
	require.NoError(t, err)
	second, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Second"})
	require.NoError(t, err)
	var firstErr, secondErr error
	raceIdentityOperations(
		func() {
			_, firstErr = module.Preauthorize(t.Context(), curator.Token, first.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
		},
		func() {
			_, secondErr = module.Preauthorize(t.Context(), curator.Token, second.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
		},
	)
	require.NotEqual(t, firstErr == nil, secondErr == nil, "exactly one Person may receive the email approval")
	winner, loser := first, second
	if secondErr == nil {
		winner, loser = second, first
	}
	session, err := module.SignIn(t.Context(), identity.Claims{Email: "shared@example.test", EmailVerified: true, DisplayName: "Shared"})
	require.NoError(t, err)
	assert.Equal(t, winner.ID, session.Person.ID)
	_, err = module.Preauthorize(t.Context(), curator.Token, loser.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
	require.Error(t, err, "consumption must not let the same email authorize a competing Person")
	detail, err := module.GetPerson(t.Context(), curator.Token, loser.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Preauthorizations)
	assert.Empty(t, detail.Emails)
}

//nolint:tparallel // Rejected mutations must finish before the parent checks the shared Persons and approval.
func TestIdentityOperationsRejectWrongPerson(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	second := authorizePerson(t, module, curator, "Sam", "sam@example.test")
	profile, err := module.Profile(t.Context(), second.Token)
	require.NoError(t, err)
	require.Len(t, profile.Emails, 1)
	secondIdentity := profile.Emails[0].ID
	approval, err := module.Preauthorize(t.Context(), curator.Token, second.Person.ID, identity.PreauthorizeRequest{Email: "sam-extra@example.test"})
	require.NoError(t, err)

	for _, scenario := range []struct {
		name      string
		operation func() error
	}{
		{name: "create Person", operation: func() error {
			_, err := module.CreatePerson(t.Context(), first.Token, identity.CreatePersonRequest{DisplayName: "Unauthorized"})
			return err
		}},
		{name: "read another Person", operation: func() error {
			_, err := module.GetPerson(t.Context(), first.Token, second.Person.ID)
			return err
		}},
		{name: "edit another Person", operation: func() error {
			_, err := module.UpdatePerson(t.Context(), first.Token, second.Person.ID, identity.UpdatePersonRequest{DisplayName: "Unauthorized", Deactivated: true})
			return err
		}},
		{name: "self promotion", operation: func() error {
			_, err := module.UpdatePerson(t.Context(), first.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "Alex", IsCurator: true})
			return err
		}},
		{name: "preauthorize another Person", operation: func() error {
			_, err := module.Preauthorize(t.Context(), first.Token, second.Person.ID, identity.PreauthorizeRequest{Email: "unauthorized@example.test"})
			return err
		}},
		{name: "revoke another Person approval", operation: func() error {
			return module.RevokePreauthorization(t.Context(), first.Token, second.Person.ID, approval.ID)
		}},
		{name: "unlink another Person identity", operation: func() error {
			return module.UnlinkEmail(t.Context(), first.Token, second.Person.ID, secondIdentity)
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			require.ErrorIs(t, scenario.operation(), identity.ErrAccessDenied)
		})
	}
	// Self-service and Curator routes must also enforce identity-to-Person ownership.
	err = module.UnlinkEmail(t.Context(), first.Token, "", secondIdentity)
	require.Error(t, err)
	err = module.UnlinkEmail(t.Context(), curator.Token, first.Person.ID, secondIdentity)
	require.Error(t, err)
	err = module.RevokePreauthorization(t.Context(), curator.Token, first.Person.ID, approval.ID)
	require.Error(t, err)

	detail, err := module.GetPerson(t.Context(), curator.Token, second.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, "Sam", detail.Person.DisplayName)
	assert.Nil(t, detail.Person.DeactivatedAt)
	require.Len(t, detail.Emails, 1)
	assert.Equal(t, secondIdentity, detail.Emails[0].ID)
	_, err = module.Authenticate(t.Context(), second.Token)
	require.NoError(t, err)
	linked, err := module.SignIn(t.Context(), identity.Claims{Email: "sam-extra@example.test", EmailVerified: true, DisplayName: "Sam"})
	require.NoError(t, err)
	assert.Equal(t, second.Person.ID, linked.Person.ID)
	self, err := module.Profile(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, self.Person.ID)
	assert.False(t, self.Person.IsCurator)
}

// TestUnlinkedEmailFollowsTheNextPreauthorization covers the address a
// Curator unlinked: refused on its own, admitted to whichever Person a Curator
// preauthorizes next, whether or not its old Person is deactivated.
func TestUnlinkedEmailFollowsTheNextPreauthorization(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	for _, deactivated := range []bool{false, true} {
		t.Run(fmt.Sprintf("deactivated=%t", deactivated), func(t *testing.T) {
			t.Parallel()
			email := fmt.Sprintf("original-%t@example.test", deactivated)
			original := authorizePerson(t, module, curator, "Original", email)
			claims := identity.FakeClaims(identity.SignInRequest{Email: email, DisplayName: "Original"})
			profile, err := module.Profile(t.Context(), original.Token)
			require.NoError(t, err)
			require.Len(t, profile.Emails, 1)
			originalLinkedEmailID := profile.Emails[0].ID
			require.NoError(t, module.UnlinkEmail(t.Context(), curator.Token, original.Person.ID, originalLinkedEmailID))
			if deactivated {
				_, err = module.UpdatePerson(t.Context(), curator.Token, original.Person.ID, identity.UpdatePersonRequest{DisplayName: "Original", Deactivated: true})
				require.NoError(t, err)
			}
			_, err = module.SignIn(t.Context(), claims)
			require.ErrorIs(t, err, identity.ErrAccessDenied)
			_, err = module.Authenticate(t.Context(), original.Token)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
			other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Other"})
			require.NoError(t, err)
			approval, err := module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: email})
			require.NoError(t, err)
			admitted, err := module.SignIn(t.Context(), claims)
			require.NoError(t, err)
			assert.Equal(t, other.ID, admitted.Person.ID)
			detail, err := module.GetPerson(t.Context(), curator.Token, other.ID)
			require.NoError(t, err)
			require.Len(t, detail.Emails, 1)
			require.Len(t, detail.Preauthorizations, 1)
			assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
			assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)
			_, err = module.UpdatePerson(t.Context(), curator.Token, original.Person.ID, identity.UpdatePersonRequest{DisplayName: "Original"})
			require.NoError(t, err)
			claims.Email = fmt.Sprintf("relinked-%t@example.test", deactivated)
			_, err = module.Preauthorize(t.Context(), curator.Token, original.Person.ID, identity.PreauthorizeRequest{Email: claims.Email})
			require.NoError(t, err)
			relinked, err := module.SignIn(t.Context(), claims)
			require.NoError(t, err)
			assert.Equal(t, original.Person.ID, relinked.Person.ID)
			profile, err = module.Profile(t.Context(), relinked.Token)
			require.NoError(t, err)
			require.Len(t, profile.Emails, 1)
			assert.NotEqual(t, originalLinkedEmailID, profile.Emails[0].ID, "the unlinked address stays unlinked")
		})
	}
}
