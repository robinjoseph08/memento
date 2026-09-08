package identity_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// oidcSubstitute speaks discovery, token exchange and JWKS without Google credentials.
type oidcSubstitute struct {
	server                *httptest.Server
	claims                map[string]any
	key                   *rsa.PrivateKey
	tokenStatus           int
	tokenForm             url.Values
	missingToken          bool
	discoveryStatus       atomic.Int32
	discoveryRequests     atomic.Int32
	discoveryIssuer       string
	authorizationEndpoint string
}

func newOIDCSubstitute(t *testing.T) *oidcSubstitute {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	s := &oidcSubstitute{key: key}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			s.discoveryRequests.Add(1)
			if status := s.discoveryStatus.Load(); status != 0 {
				w.WriteHeader(int(status))
				return
			}
			issuer := s.server.URL
			if s.discoveryIssuer != "" {
				issuer = s.discoveryIssuer
			}
			endpoint := s.server.URL + "/authorize"
			if s.authorizationEndpoint != "" {
				endpoint = s.authorizationEndpoint
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": endpoint, "token_endpoint": s.server.URL + "/token", "jwks_uri": s.server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "local", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			if !assert.NoError(t, r.ParseForm()) {
				return
			}
			s.tokenForm = r.PostForm
			if s.tokenStatus != 0 {
				w.WriteHeader(s.tokenStatus)
				_, _ = w.Write([]byte(`{"error":"provider_failure","error_description":"private-detail"}`))
				return
			}
			payload, err := json.Marshal(s.claims)
			if !assert.NoError(t, err) {
				return
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: s.key}, (&jose.SignerOptions{}).WithHeader("kid", "local"))
			if !assert.NoError(t, err) {
				return
			}
			signed, err := signer.Sign(payload)
			if !assert.NoError(t, err) {
				return
			}
			token, err := signed.CompactSerialize()
			if !assert.NoError(t, err) {
				return
			}
			response := map[string]any{"access_token": "not-persisted", "token_type": "Bearer", "expires_in": 3600}
			if !s.missingToken {
				response["id_token"] = token
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	s.claims = map[string]any{"iss": s.server.URL, "aud": "client-id", "sub": "google-subject", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": "request-nonce", "email": "alex@example.com", "email_verified": true, "name": "Alex"}
	return s
}

func (s *oidcSubstitute) provider() *identity.GoogleProvider {
	return identity.NewGoogleProvider("client-id", "client-secret", "http://localhost:3579/api/identity/google/callback", identity.WithGoogleIssuer(s.server.URL, s.server.Client()))
}

func TestGoogleDiscoveryRetriesAfterOutage(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	s.discoveryStatus.Store(http.StatusServiceUnavailable)
	provider := s.provider()
	require.Zero(t, s.discoveryRequests.Load(), "construction must not depend on Google")
	_, err := provider.AuthorizationURL(t.Context(), "state", "nonce", oauth2.GenerateVerifier())
	require.ErrorIs(t, err, identity.ErrGoogleUnavailable)
	s.discoveryStatus.Store(0)
	authorization, err := provider.AuthorizationURL(t.Context(), "state", "nonce", oauth2.GenerateVerifier())
	require.NoError(t, err)
	require.Contains(t, authorization, s.server.URL+"/authorize?")
}

func TestGoogleRejectsInvalidDiscovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, issuer, endpoint string }{
		{name: "wrong issuer", issuer: "https://wrong.example.com"},
		{name: "relative endpoint", endpoint: "/authorize"},
		{name: "endpoint scheme", endpoint: "javascript:alert(1)"},
		{name: "endpoint credentials", endpoint: "https://user:secret@example.com/authorize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newOIDCSubstitute(t)
			s.discoveryIssuer = tc.issuer
			s.authorizationEndpoint = tc.endpoint
			_, err := s.provider().AuthorizationURL(t.Context(), "state", "nonce", oauth2.GenerateVerifier())
			require.ErrorIs(t, err, identity.ErrGoogleUnavailable)
		})
	}
}

func TestGoogleRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(*oidcSubstitute)
	}{
		{"issuer", func(s *oidcSubstitute) { s.claims["iss"] = "https://wrong.example.com" }},
		{"audience", func(s *oidcSubstitute) { s.claims["aud"] = "wrong-client" }},
		{"signature", func(s *oidcSubstitute) { s.key = wrongKey }},
		{"expiry", func(s *oidcSubstitute) { s.claims["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{"nonce", func(s *oidcSubstitute) { s.claims["nonce"] = "another-nonce" }},
		{"access token hash", func(s *oidcSubstitute) { s.claims["at_hash"] = "wrong" }},
		{"missing nonce", func(s *oidcSubstitute) { delete(s.claims, "nonce") }},
		{"missing subject", func(s *oidcSubstitute) { delete(s.claims, "sub") }},
		{"missing email", func(s *oidcSubstitute) { delete(s.claims, "email") }},
		{"unverified email", func(s *oidcSubstitute) { s.claims["email_verified"] = false }},
		{"missing verification", func(s *oidcSubstitute) { delete(s.claims, "email_verified") }},
		{"wrong verification type", func(s *oidcSubstitute) { s.claims["email_verified"] = "true" }},
		{"invalid email", func(s *oidcSubstitute) { s.claims["email"] = "not an email" }},
		{"wrong authorized party", func(s *oidcSubstitute) { s.claims["azp"] = "another-client" }},
		{"missing authorized party for multiple audiences", func(s *oidcSubstitute) { s.claims["aud"] = []string{"client-id", "another-client"} }},
		{"missing token", func(s *oidcSubstitute) { s.missingToken = true }},
		{"provider failure", func(s *oidcSubstitute) { s.tokenStatus = http.StatusServiceUnavailable }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newOIDCSubstitute(t)
			tc.mutate(s)
			claims, err := s.provider().Exchange(t.Context(), "code", "request-nonce", oauth2.GenerateVerifier())
			require.Error(t, err)
			require.Empty(t, claims)
		})
	}
}

func TestGoogleMissingOptionalProfileName(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	delete(s.claims, "name")
	claims, err := s.provider().Exchange(t.Context(), "code", "request-nonce", oauth2.GenerateVerifier())
	require.NoError(t, err)
	require.Equal(t, "alex@example.com", claims.DisplayName)
}

func TestGoogleVerifiedClaims(t *testing.T) {
	t.Parallel()
	s := newOIDCSubstitute(t)
	provider := s.provider()
	authorization, err := provider.AuthorizationURL(t.Context(), "request-state", "request-nonce", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	require.NoError(t, err)
	u, err := url.Parse(authorization)
	require.NoError(t, err)
	query := u.Query()
	require.Equal(t, "openid profile email", query.Get("scope"))
	require.Equal(t, "http://localhost:3579/api/identity/google/callback", query.Get("redirect_uri"))
	require.Equal(t, "request-state", query.Get("state"))
	require.Equal(t, "request-nonce", query.Get("nonce"))
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	// RFC 7636 Appendix B provides this independent S256 test vector.
	require.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", query.Get("code_challenge"))
	claims, err := provider.Exchange(t.Context(), "authorization-code", "request-nonce", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	require.NoError(t, err)
	require.Equal(t, identity.Claims{Provider: "google", Subject: "google-subject", Email: "alex@example.com", EmailVerified: true, DisplayName: "Alex"}, claims)
	require.Equal(t, "authorization-code", s.tokenForm.Get("code"))
	require.Equal(t, "authorization_code", s.tokenForm.Get("grant_type"))
	require.Equal(t, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", s.tokenForm.Get("code_verifier"))
	require.Equal(t, "http://localhost:3579/api/identity/google/callback", s.tokenForm.Get("redirect_uri"))
}
