package identity_test

import (
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandoffCodeExchangeIssuesABearerSessionOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	browser := claimCurator(t, module)

	code, err := module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)
	require.Len(t, code, 43)
	phone, err := module.ExchangeHandoffCode(t.Context(), code, "iPhone")
	require.NoError(t, err)
	assert.Equal(t, browser.Person, phone.Person)
	assert.NotEqual(t, browser.Token, phone.Token)
	assert.Equal(t, now.Add(identity.SessionLifetime), phone.ExpiresAt)

	// The phone's session replaces the browser's: it authenticates, is listed
	// with a label the Person recognizes, and the browser's is gone.
	active, err := module.Authenticate(t.Context(), phone.Token)
	require.NoError(t, err)
	assert.Equal(t, browser.Person, active.Person)
	_, err = module.Authenticate(t.Context(), browser.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated, "the session that handed itself off is over")
	sessions, err := module.Sessions(t.Context(), phone.Token)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "Memento on iPhone", sessions[0].Device)
	assert.True(t, sessions[0].Current)

	_, err = module.ExchangeHandoffCode(t.Context(), code, "iPhone")
	require.ErrorIs(t, err, identity.ErrHandoffCodeInvalid, "a code works once")
	_, err = module.ExchangeHandoffCode(t.Context(), "made-up-code", "iPhone")
	require.ErrorIs(t, err, identity.ErrHandoffCodeInvalid)
	_, err = module.IssueHandoffCode(t.Context(), "made-up-token")
	require.ErrorIs(t, err, identity.ErrUnauthenticated)

	require.NoError(t, module.SignOut(t.Context(), phone.Token))
	_, err = module.Authenticate(t.Context(), phone.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}

func TestHandoffCodeExpiresWithinAMinute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	module := identity.New(testdb.New(t), func() time.Time { return now })
	browser := claimCurator(t, module)

	code, err := module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)
	now = now.Add(identity.HandoffCodeLifetime)
	_, err = module.ExchangeHandoffCode(t.Context(), code, "Android")
	require.ErrorIs(t, err, identity.ErrHandoffCodeInvalid)

	// The hand-off ended the first browser session, so the retry signs in again.
	browser = claimCurator(t, module)
	fresh, err := module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)
	now = now.Add(identity.HandoffCodeLifetime - time.Second)
	phone, err := module.ExchangeHandoffCode(t.Context(), fresh, "Android")
	require.NoError(t, err)
	assert.Equal(t, browser.Person, phone.Person)
}

func TestDeactivationEndsABearerSession(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	curator := claimCurator(t, module)
	person, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	_, err = module.Preauthorize(t.Context(), curator.Token, person.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
	require.NoError(t, err)
	browser, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	code, err := module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)
	phone, err := module.ExchangeHandoffCode(t.Context(), code, "iPhone")
	require.NoError(t, err)
	_, err = module.Authenticate(t.Context(), phone.Token)
	require.NoError(t, err)

	_, err = module.UpdatePerson(t.Context(), curator.Token, person.ID, identity.UpdatePersonRequest{DisplayName: "Alex", Deactivated: true})
	require.NoError(t, err)
	_, err = module.Authenticate(t.Context(), phone.Token)
	require.ErrorIs(t, err, identity.ErrUnauthenticated)

	// A code minted before deactivation cannot be turned into a session after it.
	_, err = module.UpdatePerson(t.Context(), curator.Token, person.ID, identity.UpdatePersonRequest{DisplayName: "Alex"})
	require.NoError(t, err)
	browser, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "alex@example.test", DisplayName: "Alex"}))
	require.NoError(t, err)
	code, err = module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)
	_, err = module.UpdatePerson(t.Context(), curator.Token, person.ID, identity.UpdatePersonRequest{DisplayName: "Alex", Deactivated: true})
	require.NoError(t, err)
	_, err = module.ExchangeHandoffCode(t.Context(), code, "iPhone")
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}

func TestConcurrentExchangesAdmitExactlyOnePhone(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	browser := claimCurator(t, module)
	code, err := module.IssueHandoffCode(t.Context(), browser.Token)
	require.NoError(t, err)

	const attempts = 8
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			<-start
			_, err := module.ExchangeHandoffCode(t.Context(), code, "iPhone")
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
			require.ErrorIs(t, err, identity.ErrHandoffCodeInvalid)
		}
	}
	assert.Equal(t, 1, winners)
}
