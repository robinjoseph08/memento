package identity_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claimCurator(t *testing.T, module *identity.Module) identity.Session {
	t.Helper()
	session, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "curator@example.test", DisplayName: "Curator"}))
	require.NoError(t, err)
	return session
}

func TestPreauthorizationLinksExactVerifiedEmail(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	authorization, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	other, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Also Alex"})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, other.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.Error(t, err)
	claims := identity.Claims{Provider: "google", Subject: "alex-subject", Email: "alex@example.test", EmailVerified: true, DisplayName: "Different name"}
	for _, rejected := range []identity.Claims{
		{Provider: "google", Subject: "alex-subject", Email: "Alex@example.test", EmailVerified: true, DisplayName: "Alex"},
		{Provider: "google", Subject: "alex-subject", Email: "alex@example.test", DisplayName: "Alex"},
		{Provider: "google", Subject: "alex-subject", Email: "other@example.test", EmailVerified: true, DisplayName: "Alex"},
	} {
		_, err := module.SignIn(t.Context(), rejected)
		require.Error(t, err)
	}
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, person.ID, session.Person.ID)
	assert.Equal(t, "Alex", session.Person.DisplayName)
	assert.Nil(t, session.Person.OnboardingCompletedAt)
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, authorization.ID, detail.Preauthorizations[0].ID)
	assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)
	require.Len(t, detail.Identities, 1)
	claims.Subject = "another-subject"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	claims.Subject = "alex-subject"
	claims.Email = "changed@example.test"
	returning, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, person.ID, returning.Person.ID)
	_, err = module.Preauthorize(t.Context(), session.Token, other.ID, identity.PreauthorizeRequest{Email: "not-allowed@example.test"})
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	revoked, err := module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "revoked@example.test"})
	require.NoError(t, err)
	require.NoError(t, module.RevokePreauthorization(t.Context(), curator.Token, person.ID, revoked.ID))
	claims.Subject = "revoked-subject"
	claims.Email = "revoked@example.test"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}

func TestPreauthorizationRechecksEmailOwnershipAtConsumption(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "First", "old@example.test")
	second, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Second"})
	require.NoError(t, err)
	approval, err := module.Preauthorize(t.Context(), curator.Token, second.ID, identity.PreauthorizeRequest{Email: "shared@example.test"})
	require.NoError(t, err)
	changed := identity.FakeClaims(identity.SignInRequest{Email: "old@example.test", DisplayName: "First"})
	changed.Email = "shared@example.test"
	returning, err := module.SignIn(t.Context(), changed)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, returning.Person.ID)
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "shared@example.test", DisplayName: "Second"}))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	detail, err := module.GetPerson(t.Context(), curator.Token, second.ID)
	require.NoError(t, err)
	require.Len(t, detail.Preauthorizations, 1)
	assert.Equal(t, approval.ID, detail.Preauthorizations[0].ID)
	assert.Nil(t, detail.Preauthorizations[0].ConsumedAt)
	assert.Empty(t, detail.Identities)
}

func TestFinalActiveCurator(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	first := claimCurator(t, module)
	for _, edit := range []identity.UpdatePersonRequest{
		{DisplayName: "Curator"},
		{DisplayName: "Curator", IsCurator: true, Deactivated: true},
	} {
		_, err := module.UpdatePerson(t.Context(), first.Token, first.Person.ID, edit)
		require.ErrorIs(t, err, identity.ErrFinalCurator)
	}
	second, err := module.CreatePerson(t.Context(), first.Token, identity.CreatePersonRequest{DisplayName: "Second"})
	require.NoError(t, err)
	_, err = module.UpdatePerson(t.Context(), first.Token, second.ID, identity.UpdatePersonRequest{DisplayName: "Second", IsCurator: true})
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{first.Person.ID, second.ID} {
		wg.Go(func() {
			<-start
			_, err := module.UpdatePerson(t.Context(), first.Token, id, identity.UpdatePersonRequest{DisplayName: "Former curator", Deactivated: true})
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			require.True(t, errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrFinalCurator))
		}
	}
	assert.Equal(t, 1, wins)
}

func TestPeopleWithoutLogin(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	assert.Equal(t, "Alex", person.DisplayName)
	assert.False(t, person.IsCurator)
	assert.Nil(t, person.OnboardingCompletedAt)
	assert.Nil(t, person.DeactivatedAt)
	people, err := module.ListPeople(t.Context(), curator.Token, "alex")
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, person, people[0])
	detail, err := module.GetPerson(t.Context(), curator.Token, person.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Identities)
	assert.Empty(t, detail.Preauthorizations)
	renamed, err := module.UpdatePerson(t.Context(), curator.Token, person.ID, identity.UpdatePersonRequest{DisplayName: "Alex Smith"})
	require.NoError(t, err)
	assert.Equal(t, "Alex Smith", renamed.DisplayName)
	_, err = module.CreatePerson(t.Context(), "invalid", identity.CreatePersonRequest{DisplayName: "Not allowed"})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}
