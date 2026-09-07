package identity_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeSignInUsesEmailIdentity(t *testing.T) {
	t.Parallel()
	module := identity.New(testdb.New(t), nil)
	claims := identity.FakeClaims(identity.SignInRequest{Email: "  Curator@Example.test  ", DisplayName: "Alex"})
	assert.Equal(t, "curator@example.test", claims.Subject)
	assert.Equal(t, "curator@example.test", claims.Email)
	assert.True(t, claims.EmailVerified)
	first, err := module.SignIn(t.Context(), claims)
	require.NoError(t, err)
	returning, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "curator@example.test", DisplayName: "Changed name"}))
	require.NoError(t, err)
	assert.Equal(t, first.Person, returning.Person)
	active, err := module.Authenticate(t.Context(), first.Token)
	require.NoError(t, err)
	assert.Equal(t, first.Person, active.Person)
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "different@example.test", DisplayName: "Alex"}))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}
