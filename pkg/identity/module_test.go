package identity_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrentClaim(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	const attempts = 12
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			<-start
			_, err := module.SignIn(t.Context(), identity.Claims{Provider: "fake", Subject: uuid.NewString(), Email: "same@example.test", EmailVerified: true, DisplayName: "Concurrent Curator"})
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, identity.ErrAccessDenied)
		}
	}
	assert.Equal(t, 1, winners)
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.FixedZone("local", -6*60*60))
	module := identity.New(testdb.New(t), func() time.Time { return now })
	claims := identity.Claims{Provider: "fake", Subject: "owner", Email: "owner@example.test", EmailVerified: true, DisplayName: "Owner"}
	first, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	second, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, first.ExpiresAt.Location())
	assert.Equal(t, "2026-07-01T06:00:00Z", first.ExpiresAt.Format(time.RFC3339))
	now = now.Add(23 * time.Hour)
	active, err := module.Authenticate(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, first.ExpiresAt, active.ExpiresAt)
	assert.False(t, active.Renewed)
	now = now.Add(time.Hour)
	active, err = module.Authenticate(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-02T06:00:00Z", active.ExpiresAt.Format(time.RFC3339))
	assert.True(t, active.Renewed)
	sameDay, err := module.Authenticate(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, active.ExpiresAt, sameDay.ExpiresAt)
	assert.False(t, sameDay.Renewed)
	require.NoError(t, module.SignOut(t.Context(), second.Token))
	require.NoError(t, module.SignOut(t.Context(), second.Token))
	_, err = module.Authenticate(t.Context(), second.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	now = active.ExpiresAt
	_, err = module.Authenticate(t.Context(), first.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	// Moving the controlled clock back confirms opportunistic removal, not just rejection.
	now = now.Add(-time.Hour)
	_, err = module.Authenticate(t.Context(), first.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	_, err = module.Authenticate(t.Context(), "made-up-token")
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}

func TestUnverifiedClaimDoesNotClaimInstallation(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	claims := identity.Claims{Provider: "fake", Subject: "owner", Email: "owner@example.test", DisplayName: "Owner"}
	_, err := module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrUnverifiedIdentity)
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	claims.EmailVerified = true
	_, err = module.SignIn(t.Context(), claims)
	require.NoError(t, err)
}

func TestInvalidProviderSubjectDoesNotClaim(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	// Invalid provider subjects must not be silently changed by the SQL adapter.
	claims := identity.Claims{Provider: "fake", Subject: "invalid\x00subject", Email: "owner@example.test", EmailVerified: true, DisplayName: "Owner"}
	_, err := module.SignIn(t.Context(), claims)
	require.Error(t, err)
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	claims.Subject = "valid-subject"
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.True(t, session.Person.IsCurator)
}

func TestClaimAndReturningSignIn(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	module := identity.New(db, func() time.Time { return now })
	claims := identity.Claims{Provider: "fake", Subject: "curator", Email: "curator@example.test", EmailVerified: true, DisplayName: "Alex"}
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.True(t, session.Person.IsCurator)
	assert.Equal(t, "Alex", session.Person.DisplayName)
	id, err := uuid.Parse(session.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
	assert.NotEmpty(t, session.Token)
	claimed, err = module.Claimed(t.Context())
	require.NoError(t, err)
	assert.True(t, claimed)
	// A fresh module uses persisted state and the same provider subject, not email or name matching.
	module = identity.New(db, func() time.Time { return now })
	claims.DisplayName = "Provider name changed"
	claims.Email = "new@example.test"
	returning, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, session.Person, returning.Person)
	assert.NotEqual(t, session.Token, returning.Token)
	authenticated, err := module.Authenticate(t.Context(), session.Token)
	require.NoError(t, err)
	assert.Equal(t, session.Person, authenticated.Person)
	claims.Subject = "unknown"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}
