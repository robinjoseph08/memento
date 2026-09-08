package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"golang.org/x/oauth2"
)

// ErrGoogleUnavailable means discovery or token exchange could not complete.
var ErrGoogleUnavailable = errors.New("google sign-in is unavailable")

// GoogleProvider verifies Google credentials before exposing Identity claims.
// Discovery is lazy, so Google outages do not prevent startup or database health checks.
type GoogleProvider struct {
	issuer   string
	client   *http.Client
	oauth    oauth2.Config
	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

type GoogleOption func(*GoogleProvider)

// WithGoogleIssuer substitutes a local OIDC server at the adapter boundary in tests.
// Production configuration deliberately has no issuer or transport override.
func WithGoogleIssuer(issuer string, client *http.Client) GoogleOption {
	return func(p *GoogleProvider) {
		p.issuer = issuer
		if client != nil {
			p.client = client
		}
	}
}

func NewGoogleProvider(clientID, clientSecret, callbackURL string, options ...GoogleOption) *GoogleProvider {
	p := &GoogleProvider{
		issuer: "https://accounts.google.com",
		client: &http.Client{Timeout: 10 * time.Second},
		oauth:  oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: callbackURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
	}
	for _, option := range options {
		option(p)
	}
	return p
}

func (p *GoogleProvider) discover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verifier != nil {
		return nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.issuer)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: discovery: %w", ErrGoogleUnavailable, errorstack.CaptureContext(ctx, err))
	}
	endpoint := provider.Endpoint()
	for _, raw := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !strings.HasPrefix(p.issuer, "http://"))) {
			return fmt.Errorf("%w: %w", ErrGoogleUnavailable, errorstack.Capture(errors.New("invalid OIDC endpoint")))
		}
	}
	p.oauth.Endpoint = endpoint
	p.verifier = provider.Verifier(&oidc.Config{ClientID: p.oauth.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
	return nil
}

// AuthorizationURL starts a code flow bound to the caller's one-use state, nonce and PKCE verifier.
func (p *GoogleProvider) AuthorizationURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	if err := p.discover(ctx); err != nil {
		return "", err
	}
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Exchange returns only verified claims, never access or refresh tokens.
func (p *GoogleProvider) Exchange(ctx context.Context, code, nonce, verifier string) (Claims, error) {
	if err := p.discover(ctx); err != nil {
		return Claims{}, err
	}
	token, err := p.oauth.Exchange(oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		if ctx.Err() != nil {
			return Claims{}, ctx.Err()
		}
		var response *oauth2.RetrieveError
		if errors.As(err, &response) && response.Response.StatusCode == http.StatusBadRequest {
			return Claims{}, ErrUnauthenticated
		}
		return Claims{}, fmt.Errorf("%w: token exchange: %w", ErrGoogleUnavailable, errorstack.CaptureContext(ctx, err))
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Claims{}, ErrUnauthenticated
	}
	id, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	if id.AccessTokenHash != "" {
		if err := id.VerifyAccessToken(token.AccessToken); err != nil {
			return Claims{}, ErrUnauthenticated
		}
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(nonce)) != 1 {
		return Claims{}, ErrUnauthenticated
	}
	var profile struct {
		Email           string `json:"email"`
		EmailVerified   bool   `json:"email_verified"`
		Name            string `json:"name"`
		AuthorizedParty string `json:"azp"`
	}
	if err := id.Claims(&profile); err != nil {
		return Claims{}, ErrUnauthenticated
	}
	if strings.TrimSpace(id.Subject) == "" || (profile.AuthorizedParty != "" && profile.AuthorizedParty != p.oauth.ClientID) || (len(id.Audience) > 1 && profile.AuthorizedParty == "") {
		return Claims{}, ErrUnauthenticated
	}
	profile.Email = strings.TrimSpace(profile.Email)
	address, err := mail.ParseAddress(profile.Email)
	if err != nil || address.Address != profile.Email {
		return Claims{}, ErrUnauthenticated
	}
	if !profile.EmailVerified {
		return Claims{}, ErrUnverifiedIdentity
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		profile.Name = profile.Email
	}
	return Claims{Provider: "google", Subject: id.Subject, Email: profile.Email, EmailVerified: true, DisplayName: profile.Name}, nil
}
