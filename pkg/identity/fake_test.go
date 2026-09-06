package identity_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExistingFakeIdentitySurvivesEmailUpgrade(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	module := identity.New(db, nil)
	legacy, err := module.SignIn(t.Context(), identity.Claims{Provider: "fake", Subject: "curator-local", Email: "Curator@Example.test", EmailVerified: true, DisplayName: "Existing Curator"})
	require.NoError(t, err)
	// Recreate the pre-upgrade migration state while keeping the legacy login and session.
	migrator := migrations.NewMigrator(db)
	applied, err := migrator.MigrationsWithStatus(t.Context())
	require.NoError(t, err)
	for _, migration := range applied {
		if migration.Name == "20260820000002" {
			require.NoError(t, migrator.MarkUnapplied(t.Context(), &migration))
		}
	}
	_, err = migrations.BringUpToDate(t.Context(), db)
	require.NoError(t, err)
	returning, err := module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "curator@example.test", DisplayName: "Changed name"}))
	require.NoError(t, err)
	assert.Equal(t, legacy.Person, returning.Person)
	active, err := module.Authenticate(t.Context(), legacy.Token)
	require.NoError(t, err)
	assert.Equal(t, legacy.Person, active.Person)
}

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
	_, err = module.SignIn(t.Context(), identity.FakeClaims(identity.SignInRequest{Email: "different@example.test", DisplayName: "Alex"}))
	require.ErrorIs(t, err, identity.ErrAccessDenied)
}
