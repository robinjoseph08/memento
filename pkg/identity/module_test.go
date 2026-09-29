package identity_test

import (
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

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
	for i := range attempts {
		wg.Go(func() {
			<-start
			_, err := module.SignIn(t.Context(), identity.Claims{Email: fmt.Sprintf("curator-%d@example.test", i), EmailVerified: true, DisplayName: "Concurrent Curator"})
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
			require.ErrorIs(t, err, identity.ErrAccessRequested)
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
	claims := identity.Claims{Email: "owner@example.test", EmailVerified: true, DisplayName: "Owner"}
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
	claims := identity.Claims{Email: "owner@example.test", DisplayName: "Owner"}
	_, err := module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrUnverifiedEmail)
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	claims.EmailVerified = true
	_, err = module.SignIn(t.Context(), claims)
	require.NoError(t, err)
}

func TestInvalidAddressDoesNotClaim(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	// A malformed address must be refused, not silently changed by the SQL adapter.
	claims := identity.Claims{Email: "owner\x00@example.test", EmailVerified: true, DisplayName: "Owner"}
	_, err := module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrUnverifiedEmail)
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	claims.Email = "owner@example.test"
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.True(t, session.Person.IsCurator)
}

func TestClaimAndReturningSignIn(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	module := identity.New(db, func() time.Time { return now })
	claims := identity.Claims{Email: "curator@example.test", EmailVerified: true, DisplayName: "Alex"}
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.False(t, claimed)
	session, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.True(t, session.Person.IsCurator)
	assert.Equal(t, "Alex", session.Person.DisplayName)
	id, err := uuid.Parse(session.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, byte(7), id[6]>>4)
	assert.NotEmpty(t, session.Token)
	claimed, err = module.Claimed(t.Context())
	require.NoError(t, err)
	assert.True(t, claimed)
	// A fresh module uses persisted state and the same address, not name matching.
	module = identity.New(db, func() time.Time { return now })
	claims.DisplayName = "Provider name changed"
	returning, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	assert.Equal(t, session.Person, returning.Person)
	assert.NotEqual(t, session.Token, returning.Token)
	authenticated, err := module.Authenticate(t.Context(), session.Token)
	require.NoError(t, err)
	assert.Equal(t, session.Person, authenticated.Person)
	claims.Email = "new@example.test"
	_, err = module.SignIn(t.Context(), claims)
	require.ErrorIs(t, err, identity.ErrAccessRequested)
}

// TestSignInResolvesAddressInOrder walks the four resolution steps of ADR
// 0014 with claims that carry only an address, then the two refusals.
func TestSignInResolvesAddressInOrder(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	claims := func(email, name string) identity.Claims {
		return identity.Claims{Email: email, EmailVerified: true, DisplayName: name}
	}

	// 1. An unclaimed Installation makes the first sign-in its Curator.
	curator, err := module.SignIn(t.Context(), claims("Curator@Example.test", "Curator"))
	require.NoError(t, err)
	assert.True(t, curator.Person.IsCurator)
	assert.Equal(t, "curator@example.test", curator.Person.UpdateEmail, "addresses are stored lowercased")
	claimed, err := module.Claimed(t.Context())
	require.NoError(t, err)
	assert.True(t, claimed)

	// 4. An unknown address becomes an Access Request, not a Person.
	_, err = module.SignIn(t.Context(), claims("Stranger@example.test", "Stranger"))
	require.ErrorIs(t, err, identity.ErrAccessRequested)
	requests, err := module.ListAccessRequests(t.Context(), curator.Token)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	assert.Equal(t, "stranger@example.test", requests[0].Email)
	assert.Equal(t, "join", requests[0].Kind)

	// 3. A Preauthorization links the address to its Person and is consumed.
	alex, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, alex.ID, identity.PreauthorizeRequest{Email: "Alex@Example.test"})
	require.NoError(t, err)
	linked, err := module.SignIn(t.Context(), claims("ALEX@example.test", "Provider name"))
	require.NoError(t, err)
	assert.Equal(t, alex.ID, linked.Person.ID)
	assert.Equal(t, "Alex", linked.Person.DisplayName)
	assert.Equal(t, "alex@example.test", linked.Person.UpdateEmail)
	assert.True(t, linked.Person.EmailUpdates)
	detail, err := module.GetPerson(t.Context(), curator.Token, alex.ID)
	require.NoError(t, err)
	require.Len(t, detail.Emails, 1)
	assert.Equal(t, "alex@example.test", detail.Emails[0].Email)
	require.Len(t, detail.Preauthorizations, 1)
	assert.NotNil(t, detail.Preauthorizations[0].ConsumedAt)

	_, err = module.Preauthorize(t.Context(), curator.Token, alex.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.Error(t, err, "a linked address needs no approval, so none can be left unused")

	// 2. A Linked Email signs its Person in, whatever the letter case or name.
	returning, err := module.SignIn(t.Context(), claims("alex@EXAMPLE.test", "Changed name"))
	require.NoError(t, err)
	assert.Equal(t, linked.Person, returning.Person)
	_, err = module.Authenticate(t.Context(), linked.Token)
	require.NoError(t, err)
	detail, err = module.GetPerson(t.Context(), curator.Token, alex.ID)
	require.NoError(t, err)
	require.Len(t, detail.Emails, 1, "a returning sign-in links nothing new")

	// An unlinked address is refused outright until a Curator preauthorizes it again.
	require.NoError(t, module.UnlinkEmail(t.Context(), curator.Token, alex.ID, detail.Emails[0].ID))
	_, err = module.SignIn(t.Context(), claims("alex@example.test", "Alex"))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	requests, err = module.ListAccessRequests(t.Context(), curator.Token)
	require.NoError(t, err)
	assert.Len(t, requests, 1, "a refused unlinked address records no Access Request")
	_, err = module.Preauthorize(t.Context(), curator.Token, alex.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	relinked, err := module.SignIn(t.Context(), claims("alex@example.test", "Alex"))
	require.NoError(t, err)
	assert.Equal(t, alex.ID, relinked.Person.ID)
	_, err = module.Authenticate(t.Context(), linked.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated, "unlinking ended the earlier sessions")
	detail, err = module.GetPerson(t.Context(), curator.Token, alex.ID)
	require.NoError(t, err)
	require.Len(t, detail.Emails, 1)

	// Once unlinked, the address may be preauthorized for someone else instead.
	sam, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Sam"})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, sam.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.Error(t, err, "a linked address cannot be preauthorized for another Person")
	require.NoError(t, module.UnlinkEmail(t.Context(), curator.Token, alex.ID, detail.Emails[0].ID))
	_, err = module.Preauthorize(t.Context(), curator.Token, sam.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	moved, err := module.SignIn(t.Context(), claims("alex@example.test", "Sam"))
	require.NoError(t, err)
	assert.Equal(t, sam.ID, moved.Person.ID)

	// A deactivated Person is refused even with a Linked Email.
	_, err = module.UpdatePerson(t.Context(), curator.Token, sam.ID, identity.UpdatePersonRequest{DisplayName: "Sam", Deactivated: true})
	require.NoError(t, err)
	_, err = module.SignIn(t.Context(), claims("alex@example.test", "Sam"))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
	_, err = module.Authenticate(t.Context(), moved.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}
