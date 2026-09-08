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
func TestPreauthorizationRaceAdmitsExactlyOneSubject(t *testing.T) {
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
		{name: "unverified exact email", claims: identity.Claims{Provider: "google", Subject: "unverified", Email: "alex@example.test", DisplayName: "Alex"}, err: identity.ErrUnverifiedIdentity},
		{name: "matching name only", claims: identity.Claims{Provider: "google", Subject: "matching-name", Email: "stranger@example.test", EmailVerified: true, DisplayName: "Alex"}, err: identity.ErrAccessDenied},
		{name: "email case differs", claims: identity.Claims{Provider: "google", Subject: "case-differs", Email: "Alex@example.test", EmailVerified: true, DisplayName: "Alex"}, err: identity.ErrAccessDenied},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := module.SignIn(t.Context(), scenario.claims)
			require.ErrorIs(t, err, scenario.err)
		})
	}

	const attempts = 8
	type outcome struct {
		claims  identity.Claims
		session identity.Session
		err     error
	}
	results := make([]outcome, attempts)
	operations := make([]func(), attempts)
	for i := range attempts {
		results[i].claims = identity.Claims{Provider: "google", Subject: fmt.Sprintf("subject-%d", i), Email: "alex@example.test", EmailVerified: true, DisplayName: "Provider name"}
		operations[i] = func() {
			results[i].session, results[i].err = module.SignIn(t.Context(), results[i].claims)
		}
	}
	raceIdentityOperations(operations...)
	var winner outcome
	wins := 0
	for _, result := range results {
		if result.err == nil {
			wins++
			winner = result
			assert.Equal(t, person.ID, result.session.Person.ID)
			assert.Equal(t, "Alex", result.session.Person.DisplayName)
		} else {
			require.ErrorIs(t, result.err, identity.ErrAccessDenied)
		}
	}
	require.Equal(t, 1, wins)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Identities, 1)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
	assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)
	assert.Nil(t, detail.Person.OnboardingCompletedAt)

	// The admitted subject can return without consuming another approval.
	winner.claims.Email = "changed@example.test"
	winner.claims.DisplayName = "Changed provider name"
	returning, err := module.SignIn(t.Context(), winner.claims)
	require.NoError(t, err)
	assert.Equal(t, person.ID, returning.Person.ID)
	assert.Equal(t, "Alex", returning.Person.DisplayName)
	for _, result := range results {
		if result.err != nil {
			_, err := module.SignIn(t.Context(), result.claims)
			require.ErrorIs(t, err, identity.ErrAccessDenied)
		}
	}
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
	session, err := module.SignIn(t.Context(), identity.Claims{Provider: "google", Subject: "shared", Email: "shared@example.test", EmailVerified: true, DisplayName: "Shared"})
	require.NoError(t, err)
	assert.Equal(t, winner.ID, session.Person.ID)
	_, err = module.Preauthorize(t.Context(), curator.Token, loser.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
	require.Error(t, err, "consumption must not let the same email authorize a competing Person")
	detail, err := module.GetPerson(t.Context(), curator.Token, loser.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Preauthorizations)
	assert.Empty(t, detail.Identities)
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
	require.Len(t, profile.Identities, 1)
	secondIdentity := profile.Identities[0].ID
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
			return module.UnlinkIdentity(t.Context(), first.Token, second.Person.ID, secondIdentity)
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			require.ErrorIs(t, scenario.operation(), identity.ErrAccessDenied)
		})
	}
	// Self-service and Curator routes must also enforce identity-to-Person ownership.
	err = module.UnlinkIdentity(t.Context(), first.Token, "", secondIdentity)
	require.Error(t, err)
	err = module.UnlinkIdentity(t.Context(), curator.Token, first.Person.ID, secondIdentity)
	require.Error(t, err)
	err = module.RevokePreauthorization(t.Context(), curator.Token, first.Person.ID, approval.ID)
	require.Error(t, err)

	detail, err := module.GetPerson(t.Context(), curator.Token, second.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, "Sam", detail.Person.DisplayName)
	assert.Nil(t, detail.Person.DeactivatedAt)
	require.Len(t, detail.Identities, 1)
	assert.Equal(t, secondIdentity, detail.Identities[0].ID)
	_, err = module.Authenticate(t.Context(), second.Token)
	require.NoError(t, err)
	linked, err := module.SignIn(t.Context(), identity.Claims{Provider: "google", Subject: "sam-extra", Email: "sam-extra@example.test", EmailVerified: true, DisplayName: "Sam"})
	require.NoError(t, err)
	assert.Equal(t, second.Person.ID, linked.Person.ID)
	self, err := module.Profile(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, self.Person.ID)
	assert.False(t, self.Person.IsCurator)
}

func TestKnownSubjectCannotMoveToAnotherPreauthorizedPerson(t *testing.T) {
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
			require.Len(t, profile.Identities, 1)
			originalIdentityID := profile.Identities[0].ID
			require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, original.Person.ID, originalIdentityID))
			if deactivated {
				_, err = module.UpdatePerson(t.Context(), curator.Token, original.Person.ID, identity.UpdatePersonRequest{DisplayName: "Original", Deactivated: true})
				require.NoError(t, err)
			}
			other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Other"})
			require.NoError(t, err)
			approval, err := module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: email})
			require.NoError(t, err)
			_, err = module.SignIn(t.Context(), claims)
			require.ErrorIs(t, err, identity.ErrAccessDenied)
			_, err = module.Authenticate(t.Context(), original.Token)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
			detail, err := module.GetPerson(t.Context(), curator.Token, other.ID)
			require.NoError(t, err)
			assert.Empty(t, detail.Identities)
			require.Len(t, detail.Preauthorizations, 1)
			assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
			assert.Nil(t, detail.Preauthorizations[0].ConsumedAt)

			newClaims := claims
			newClaims.Subject += "-different-subject"
			admitted, err := module.SignIn(t.Context(), newClaims)
			require.NoError(t, err)
			assert.Equal(t, other.ID, admitted.Person.ID)
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
			require.Len(t, profile.Identities, 1)
			assert.Equal(t, originalIdentityID, profile.Identities[0].ID)
		})
	}
}
