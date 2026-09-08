package identity_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// Both adapters feed the same public Identity use cases. Google's claims come
// from signed ID tokens, while fake claims come from the development adapter.
func TestProviderIdentityContract(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"fake", "google"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			module := identity.New(testdb.New(t), nil)
			claimsFor := func(email, name string) identity.Claims {
				return identity.FakeClaims(identity.SignInRequest{Email: email, DisplayName: name})
			}
			if provider == "google" {
				substitute := newOIDCSubstitute(t)
				google := substitute.provider()
				claimsFor = func(email, name string) identity.Claims {
					// Test subjects are stable for each fixture account, never linked by name.
					substitute.claims["sub"] = email
					substitute.claims["email"] = email
					substitute.claims["name"] = name
					claims, err := google.Exchange(t.Context(), "code", "request-nonce", oauth2.GenerateVerifier())
					require.NoError(t, err)
					return claims
				}
			}
			curatorClaims := claimsFor("curator@example.test", "Curator")
			curator, err := module.SignIn(t.Context(), curatorClaims)
			require.NoError(t, err)
			assert.True(t, curator.Person.IsCurator)
			assert.Nil(t, curator.Person.OnboardingCompletedAt)
			alex, err := module.CreatePerson(t.Context(), curator.Token, identity.CreatePersonRequest{DisplayName: "Alex"})
			require.NoError(t, err)
			_, err = module.SignIn(t.Context(), claimsFor("alex@example.test", "Alex"))
			require.ErrorIs(t, err, identity.ErrAccessDenied)
			_, err = module.Preauthorize(t.Context(), curator.Token, alex.ID, identity.PreauthorizeRequest{Email: "alex@example.test"})
			require.NoError(t, err)
			_, err = module.SignIn(t.Context(), claimsFor("Alex@example.test", "Alex"))
			require.ErrorIs(t, err, identity.ErrAccessDenied)
			memberClaims := claimsFor("alex@example.test", "Provider name")
			member, err := module.SignIn(t.Context(), memberClaims)
			require.NoError(t, err)
			assert.Equal(t, alex.ID, member.Person.ID)
			assert.Equal(t, "Alex", member.Person.DisplayName)
			returning, err := module.SignIn(t.Context(), claimsFor("alex@example.test", "Changed provider name"))
			require.NoError(t, err)
			assert.Equal(t, member.Person, returning.Person)
			_, err = module.Preauthorize(t.Context(), curator.Token, alex.ID, identity.PreauthorizeRequest{Email: "second@example.test"})
			require.NoError(t, err)
			second, err := module.SignIn(t.Context(), claimsFor("second@example.test", "Second account"))
			require.NoError(t, err)
			assert.Equal(t, alex.ID, second.Person.ID)
			detail, err := module.GetPerson(t.Context(), curator.Token, alex.ID)
			require.NoError(t, err)
			require.Len(t, detail.Identities, 2)
			require.Len(t, detail.Preauthorizations, 2)
			for _, approval := range detail.Preauthorizations {
				assert.NotNil(t, approval.ConsumedAt)
			}
			require.NoError(t, module.UnlinkIdentity(t.Context(), curator.Token, alex.ID, detail.Identities[1].ID))
			_, err = module.Authenticate(t.Context(), second.Token)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
			_, err = module.Authenticate(t.Context(), member.Token)
			require.NoError(t, err)
			_, err = module.UpdatePerson(t.Context(), curator.Token, alex.ID, identity.UpdatePersonRequest{DisplayName: "Alex", Deactivated: true})
			require.NoError(t, err)
			_, err = module.Authenticate(t.Context(), returning.Token)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
			_, err = module.SignIn(t.Context(), memberClaims)
			require.ErrorIs(t, err, identity.ErrAccessDenied)
		})
	}
}
