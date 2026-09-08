package identity_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raceIdentityOperations releases all callers together and waits for their outcomes.
func raceIdentityOperations(operations ...func()) {
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(len(operations))
	for _, operation := range operations {
		done.Go(func() {
			ready.Done()
			<-start
			operation()
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
}

func TestDeactivationRacesSignInAndRenewal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	claims := identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"})
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, first.Person.ID, identity.PreauthorizeRequest{Email: "other@example.test"})
	require.NoError(t, err)
	otherClaims := identity.Claims{Provider: "google", Subject: "other", Email: "other@example.test", EmailVerified: true, DisplayName: "Alex"}
	other, err := module.SignIn(t.Context(), otherClaims)
	require.NoError(t, err)
	now = now.Add(24 * time.Hour)

	var issued, renewed identity.Session
	var issueErr, renewErr, deactivateErr error
	raceIdentityOperations(
		func() { issued, issueErr = module.SignIn(t.Context(), claims) },
		func() { renewed, renewErr = module.Authenticate(t.Context(), first.Token) },
		func() {
			_, deactivateErr = module.UpdatePerson(t.Context(), curator.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "Alex", Deactivated: true})
		},
	)
	require.NoError(t, deactivateErr)
	if issueErr != nil {
		require.ErrorIs(t, issueErr, identity.ErrAccessDenied)
	}
	if renewErr != nil {
		require.ErrorIs(t, renewErr, identity.ErrUnauthenticated)
	} else {
		assert.True(t, renewed.Renewed)
		assert.Equal(t, first.ExpiresAt.Add(24*time.Hour), renewed.ExpiresAt)
	}
	tokens := []string{first.Token, second.Token, other.Token}
	if issueErr == nil {
		require.NotEmpty(t, issued.Token)
		tokens = append(tokens, issued.Token)
	}
	for _, token := range tokens {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
	for _, claims := range []identity.Claims{claims, otherClaims} {
		_, err := module.SignIn(t.Context(), claims)
		require.ErrorIs(t, err, identity.ErrAccessDenied)
	}
	detail, err := module.GetPerson(t.Context(), curator.Token, first.Person.ID)
	require.NoError(t, err)
	assert.NotNil(t, detail.Person.DeactivatedAt)
	assert.Len(t, detail.Identities, 2)

	// Reactivation permits a fresh sign-in, not recovery of revoked browser sessions.
	_, err = module.UpdatePerson(t.Context(), curator.Token, first.Person.ID, identity.UpdatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	for _, token := range tokens {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated, "reactivation must not restore a revoked session")
	}
	fresh, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, first.Person.ID, fresh.Person.ID)
	sessions, err := module.Sessions(t.Context(), fresh.Token)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

func TestUnlinkRacesSignInAndRenewal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	retained := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	_, err := module.Preauthorize(t.Context(), curator.Token, retained.Person.ID, identity.PreauthorizeRequest{Email: "removed@example.test"})
	require.NoError(t, err)
	claims := identity.Claims{Provider: "google", Subject: "removed", Email: "removed@example.test", EmailVerified: true, DisplayName: "Alex"}
	first, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	profile, err := module.Profile(t.Context(), first.Token)
	require.NoError(t, err)
	var identityID string
	for _, linked := range profile.Identities {
		if linked.Email == claims.Email {
			identityID = linked.ID
		}
	}
	require.NotEmpty(t, identityID)
	now = now.Add(24 * time.Hour)

	var issued, renewed identity.Session
	var issueErr, renewErr, unlinkErr error
	raceIdentityOperations(
		func() { issued, issueErr = module.SignIn(t.Context(), claims) },
		func() { renewed, renewErr = module.Authenticate(t.Context(), first.Token) },
		func() { unlinkErr = module.UnlinkIdentity(t.Context(), curator.Token, retained.Person.ID, identityID) },
	)
	require.NoError(t, unlinkErr)
	if issueErr != nil {
		require.ErrorIs(t, issueErr, identity.ErrAccessDenied)
	}
	if renewErr != nil {
		require.ErrorIs(t, renewErr, identity.ErrUnauthenticated)
	} else {
		assert.True(t, renewed.Renewed)
	}
	tokens := []string{first.Token, second.Token}
	if issueErr == nil {
		tokens = append(tokens, issued.Token)
	}
	for _, token := range tokens {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.Authenticate(t.Context(), retained.Token)
	require.NoError(t, err)
	sessions, err := module.Sessions(t.Context(), retained.Token)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	profile, err = module.Profile(t.Context(), retained.Token)
	require.NoError(t, err)
	require.Len(t, profile.Identities, 1)
	assert.Equal(t, "alex@example.test", profile.Identities[0].Email)

	// Explicit relinking keeps ownership but cannot restore old sessions.
	_, err = module.Preauthorize(t.Context(), curator.Token, retained.Person.ID, identity.PreauthorizeRequest{Email: claims.Email})
	require.NoError(t, err)
	relinked, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, retained.Person.ID, relinked.Person.ID)
	for _, token := range tokens {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
}

func TestSignOutEverywhereRacesRenewal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	claims := identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"})
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, first.Person.ID, identity.PreauthorizeRequest{Email: "other@example.test"})
	require.NoError(t, err)
	other, err := module.SignIn(t.Context(), identity.Claims{Provider: "google", Subject: "other", Email: "other@example.test", EmailVerified: true, DisplayName: "Alex"})
	require.NoError(t, err)
	now = now.Add(24 * time.Hour)

	var renewed identity.Session
	var renewErr, signOutErr error
	raceIdentityOperations(
		func() { renewed, renewErr = module.Authenticate(t.Context(), second.Token) },
		func() { signOutErr = module.SignOutEverywhere(t.Context(), first.Token) },
	)
	require.NoError(t, signOutErr)
	if renewErr != nil {
		require.ErrorIs(t, renewErr, identity.ErrUnauthenticated)
	} else {
		assert.True(t, renewed.Renewed)
	}
	for _, token := range []string{first.Token, second.Token, other.Token} {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
	_, err = module.Authenticate(t.Context(), curator.Token)
	require.NoError(t, err)
	fresh, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	sessions, err := module.Sessions(t.Context(), fresh.Token)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.True(t, sessions[0].Current)
}

//nolint:tparallel // Each case passes its surviving Curator to the next case in the shared schema.
func TestSignedInCuratorsRaceEachOthersDemotionAndDeactivation(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	first := claimCurator(t, module)
	for _, scenario := range []struct {
		name       string
		deactivate bool
	}{
		{name: "demotion"},
		{name: "deactivation", deactivate: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			second := authorizePerson(t, module, first, "Second", scenario.name+"@example.test")
			_, err := module.UpdatePerson(t.Context(), first.Token, second.Person.ID, identity.UpdatePersonRequest{DisplayName: "Second", IsCurator: true})
			require.NoError(t, err)
			for _, token := range []string{first.Token, second.Token} {
				active, err := module.Authenticate(t.Context(), token)
				require.NoError(t, err)
				require.True(t, active.Person.IsCurator)
			}
			edit := identity.UpdatePersonRequest{DisplayName: "Former curator", IsCurator: scenario.deactivate, Deactivated: scenario.deactivate}
			var firstErr, secondErr error
			raceIdentityOperations(
				func() { _, firstErr = module.UpdatePerson(t.Context(), first.Token, second.Person.ID, edit) },
				func() { _, secondErr = module.UpdatePerson(t.Context(), second.Token, first.Person.ID, edit) },
			)
			wins := 0
			for _, err := range []error{firstErr, secondErr} {
				if err == nil {
					wins++
				} else {
					assert.True(t, errors.Is(err, identity.ErrAccessDenied) || errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrFinalCurator), "unexpected rejection: %v", err)
				}
			}
			require.Equal(t, 1, wins)
			survivor := first
			if secondErr == nil {
				survivor = second
			}
			people, err := module.ListPeople(t.Context(), survivor.Token, "")
			require.NoError(t, err)
			activeCurators := 0
			for _, person := range people {
				if person.IsCurator && person.DeactivatedAt == nil {
					activeCurators++
				}
			}
			assert.Equal(t, 1, activeCurators)
			_, err = module.UpdatePerson(t.Context(), survivor.Token, survivor.Person.ID, edit)
			require.ErrorIs(t, err, identity.ErrFinalCurator)
			first = survivor
		})
	}
}

func TestExpiredSessionsCannotRaceBackToLife(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	curator := claimCurator(t, module)
	first := authorizePerson(t, module, curator, "Alex", "alex@example.test")
	claims := identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"})
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	now = first.ExpiresAt
	var firstErr, secondErr error
	raceIdentityOperations(
		func() { _, firstErr = module.Authenticate(t.Context(), first.Token) },
		func() { _, secondErr = module.Authenticate(t.Context(), second.Token) },
	)
	require.ErrorIs(t, firstErr, identity.ErrUnauthenticated)
	require.ErrorIs(t, secondErr, identity.ErrUnauthenticated)

	// A clock correction must not make an already expired session usable again.
	now = now.Add(-time.Hour)
	for _, token := range []string{first.Token, second.Token} {
		_, err := module.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	}
	fresh, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	sessions, err := module.Sessions(t.Context(), fresh.Token)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.True(t, sessions[0].Current)
}
