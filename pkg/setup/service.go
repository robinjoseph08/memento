// Package setup owns first-browser identity creation and the initial Session.
package setup

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/uptrace/bun"
)

const (
	challengeLifetime    = 10 * time.Minute
	verificationLifetime = 30 * time.Minute
	maximumAttempts      = 5
	trustedLifetime      = 365 * 24 * time.Hour
	publicLifetime       = 12 * time.Hour
)

var (
	ErrSetupComplete      = errors.New("setup is complete")
	ErrInvalidCode        = errors.New("verification code is invalid or expired")
	ErrInvalidToken       = errors.New("setup verification is invalid or expired")
	ErrInvalidIdentity    = errors.New("setup identity is invalid")
	ErrInvalidChoices     = errors.New("onboarding choices are incomplete")
	ErrUnauthenticated    = errors.New("session is invalid or expired")
	ErrCSRF               = errors.New("CSRF token is invalid")
	ErrSessionType        = errors.New("session type does not support this operation")
	ErrNotCurator         = errors.New("curator authority is required")
	errGenerateCredential = errors.New("generate secure setup credential")
)

// AvailabilityResponse is generated to TypeScript by Tygo.
type AvailabilityResponse struct {
	Status string `json:"status"`
}

// RequestCodeRequest is generated to TypeScript by Tygo.
type RequestCodeRequest struct {
	DisplayName string `json:"display_name" mod:"trim" validate:"required,max=120"`
	Email       string `json:"email" mod:"trim" validate:"required,email,max=320"`
}

// RequestCodeResponse is generated to TypeScript by Tygo.
type RequestCodeResponse struct {
	ChallengeID string `json:"challenge_id"`
	Status      string `json:"status"`
}

// VerifyCodeRequest is generated to TypeScript by Tygo.
type VerifyCodeRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,len=64,hexadecimal"`
	Code        string `json:"code" validate:"required,len=8,numeric"`
}

// VerifyCodeResponse is generated to TypeScript by Tygo.
type VerifyCodeResponse struct {
	VerificationToken string `json:"verification_token"`
	Status            string `json:"status"`
}

// CompleteRequest is generated to TypeScript by Tygo.
type CompleteRequest struct {
	VerificationToken         string `json:"verification_token" validate:"required,len=64,hexadecimal"`
	PrivacyAcknowledged       bool   `json:"privacy_acknowledged" validate:"required"`
	EngagementAcknowledged    bool   `json:"engagement_acknowledged" validate:"required"`
	InterestListAcknowledged  bool   `json:"interest_list_acknowledged" validate:"required"`
	EmailPreviewsAcknowledged bool   `json:"email_previews_acknowledged" validate:"required"`
	PushGuidanceAcknowledged  bool   `json:"push_guidance_acknowledged" validate:"required"`
	EmailPreference           string `json:"email_preference" validate:"required,oneof=immediate weekly none"`
	SessionType               string `json:"session_type" validate:"required,oneof=trusted public"`
}

// CompleteResponse is generated to TypeScript by Tygo.
type CompleteResponse struct {
	Status    string `json:"status"`
	CSRFToken string `json:"csrf_token"`
}

// SessionResponse is generated to TypeScript by Tygo.
type SessionResponse struct {
	DisplayName        string `json:"display_name"`
	SessionType        string `json:"session_type"`
	CSRFToken          string `json:"csrf_token"`
	Curator            bool   `json:"curator"`
	OnboardingRequired bool   `json:"onboarding_required"`
}

type challenge struct {
	DisplayName           string
	Email                 string
	Normalized            string
	Attempts              int
	ExpiresAt             time.Time
	VerifiedAt            sql.NullTime
	VerificationExpiresAt sql.NullTime
	ConsumedAt            sql.NullTime
}

type completedSession = BrowserSession

type authenticatedSession struct {
	DisplayName string
	SessionType string
	AccessState string
	Credential  []byte
	PersonID    uuid.UUID
	AccessID    uuid.UUID
	SessionID   uuid.UUID
}

// SessionActor identifies an authenticated action without exposing a browser credential.
type SessionActor struct {
	PersonID  uuid.UUID
	AccessID  uuid.UUID
	SessionID uuid.UUID
	Curator   bool
}

// CurrentRecipientSession verifies the full persisted lifecycle of a Recipient Session.
// Callers map a false result to their own privacy-safe domain error.
func CurrentRecipientSession(ctx context.Context, db bun.IDB, actor SessionActor) (bool, error) {
	var current bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM sessions AS session
		JOIN people AS person
		  ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id AND access.is_current AND access.state = 'completed'
		JOIN system_settings AS settings
		  ON settings.id = 1 AND settings.setup_complete AND settings.security_epoch = session.security_epoch
		WHERE session.id = ? AND session.person_id = ? AND session.recipient_access_generation_id = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > now())
		    OR (session.session_type = 'public' AND session.absolute_expires_at > now()))
	)`, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &current)
	return current, err
}

// CuratorSession identifies an authenticated Curator action without exposing a browser credential.
type CuratorSession struct {
	PersonID  uuid.UUID
	SessionID uuid.UUID
}

// BrowserSession contains a newly issued opaque browser credential.
// Callers must place it only in Memento's protected Session cookie.
type BrowserSession struct {
	Credential  string
	CSRFToken   string
	SessionType string
	ExpiresAt   *time.Time
}

func sessionExpirations(sessionType string, now time.Time) (*time.Time, *time.Time, error) {
	switch sessionType {
	case "trusted":
		expiresAt := now.Add(trustedLifetime)
		return &expiresAt, nil, nil
	case "public":
		expiresAt := now.Add(publicLifetime)
		return nil, &expiresAt, nil
	default:
		return nil, nil, ErrSessionType
	}
}

// Service coordinates setup state, required email, identity, and Session persistence.
type Service struct {
	db       *bun.DB
	delivery *emaildelivery.Service
	security config.SecurityConfig
	secret   []byte
	now      func() time.Time
	random   io.Reader
	location LocationResolver
}

// Option configures a Service dependency.
type Option func(*Service)

// WithClock configures the time source used for expiration checks and writes.
func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

// LocationResolver resolves an address using operator-provided local data only.
type LocationResolver interface {
	Lookup(ip net.IP) string
}

func New(db *bun.DB, delivery *emaildelivery.Service, security config.SecurityConfig, options ...Option) *Service {
	service := &Service{
		db: db, delivery: delivery, security: security, secret: []byte(security.Secret),
		now: time.Now, random: rand.Reader,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// SetLocationResolver enables optional Session location from a local database.
func (s *Service) SetLocationResolver(resolver LocationResolver) { s.location = resolver }

// Available reports only whether the public setup workflow still exists.
func (s *Service) Available(ctx context.Context) (AvailabilityResponse, error) {
	var complete bool
	if err := s.db.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1`).Scan(ctx, &complete); err != nil {
		return AvailabilityResponse{}, err
	}
	if complete {
		return AvailabilityResponse{}, ErrSetupComplete
	}
	return AvailabilityResponse{Status: "available"}, nil
}

// RequestCode commits a challenge and required email without creating a Person.
func (s *Service) RequestCode(ctx context.Context, request RequestCodeRequest) (RequestCodeResponse, error) {
	displayName := strings.TrimSpace(request.DisplayName)
	email, normalizedEmail, err := normalizeEmail(request.Email)
	if displayName == "" || len([]rune(displayName)) > 120 || err != nil {
		return RequestCodeResponse{}, ErrInvalidIdentity
	}
	challengeID, challengeRaw, err := s.randomToken()
	if err != nil {
		return RequestCodeResponse{}, err
	}
	code, err := s.randomCode()
	if err != nil {
		return RequestCodeResponse{}, err
	}
	challengeUUID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return RequestCodeResponse{}, errGenerateCredential
	}
	codeHash := s.codeHash(challengeRaw, code)

	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var complete bool
		if err := tx.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1 FOR SHARE`).Scan(ctx, &complete); err != nil {
			return err
		}
		if complete {
			return ErrSetupComplete
		}
		now := s.now().UTC()
		expiresAt := now.Add(challengeLifetime)
		deliveryID, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind:          emaildelivery.KindSetupCode,
			Recipient:     email,
			Subject:       "Your Memento setup code",
			Body:          fmt.Sprintf("Your Memento setup code is %s. It expires in 10 minutes.", code),
			DeliverBefore: &expiresAt,
		})
		if err != nil {
			return err
		}
		if _, err = tx.NewRaw(`
			INSERT INTO login_challenges (
				id, challenge_hash, code_hash, display_name, email, normalized_email,
				email_delivery_id, expires_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, challengeUUID, tokenHash(challengeRaw), codeHash, displayName, email, normalizedEmail, deliveryID, expiresAt, now).Exec(ctx); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, "setup_code_requested", "success", nil, nil, nil)
	})
	if err != nil {
		return RequestCodeResponse{}, fmt.Errorf("request setup code: %w", err)
	}
	return RequestCodeResponse{ChallengeID: challengeID, Status: "queued"}, nil
}

// VerifyCode consumes one code and returns a separate credential for final setup.
func (s *Service) VerifyCode(ctx context.Context, request VerifyCodeRequest) (VerifyCodeResponse, error) {
	challengeRaw, err := decodeToken(request.ChallengeID)
	if err != nil || !validCode(request.Code) {
		return VerifyCodeResponse{}, ErrInvalidCode
	}
	verificationToken, verificationRaw, err := s.randomToken()
	if err != nil {
		return VerifyCodeResponse{}, err
	}
	invalid := false
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var complete bool
		if err := tx.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1 FOR SHARE`).Scan(ctx, &complete); err != nil {
			return err
		}
		if complete {
			return ErrSetupComplete
		}
		var stored challenge
		err := tx.NewRaw(`
			SELECT display_name, email, normalized_email, attempts, expires_at, verified_at, consumed_at
			FROM login_challenges WHERE challenge_hash = ? FOR UPDATE
		`, tokenHash(challengeRaw)).Scan(ctx, &stored.DisplayName, &stored.Email, &stored.Normalized, &stored.Attempts, &stored.ExpiresAt, &stored.VerifiedAt, &stored.ConsumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			invalid = true
			return s.appendAudit(ctx, tx, "setup_code_verified", "invalid", nil, nil, nil)
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if stored.Attempts >= maximumAttempts || !now.Before(stored.ExpiresAt) || stored.VerifiedAt.Valid || stored.ConsumedAt.Valid {
			invalid = true
			return s.appendAudit(ctx, tx, "setup_code_verified", "invalid", nil, nil, nil)
		}
		var expectedHash []byte
		if err := tx.NewRaw(`SELECT code_hash FROM login_challenges WHERE challenge_hash = ?`, tokenHash(challengeRaw)).Scan(ctx, &expectedHash); err != nil {
			return err
		}
		actualHash := s.codeHash(challengeRaw, request.Code)
		if subtle.ConstantTimeCompare(expectedHash, actualHash) != 1 {
			invalid = true
			if _, err := tx.NewRaw(`UPDATE login_challenges SET attempts = attempts + 1 WHERE challenge_hash = ?`, tokenHash(challengeRaw)).Exec(ctx); err != nil {
				return err
			}
			return s.appendAudit(ctx, tx, "setup_code_verified", "invalid", nil, nil, nil)
		}
		if _, err = tx.NewRaw(`
			UPDATE login_challenges
			SET verified_at = ?, verification_token_hash = ?, verification_expires_at = ?
			WHERE challenge_hash = ?
		`, now, tokenHash(verificationRaw), now.Add(verificationLifetime), tokenHash(challengeRaw)).Exec(ctx); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, "setup_code_verified", "success", nil, nil, nil)
	})
	if err != nil {
		return VerifyCodeResponse{}, fmt.Errorf("verify setup code: %w", err)
	}
	if invalid {
		return VerifyCodeResponse{}, ErrInvalidCode
	}
	return VerifyCodeResponse{VerificationToken: verificationToken, Status: "verified"}, nil
}

// complete atomically creates all identity, Onboarding, and Session records and closes setup.
func (s *Service) complete(ctx context.Context, request CompleteRequest) (completedSession, error) {
	verificationRaw, err := decodeToken(request.VerificationToken)
	if err != nil {
		return completedSession{}, ErrInvalidToken
	}
	if !request.PrivacyAcknowledged || !request.EngagementAcknowledged || !request.InterestListAcknowledged ||
		!request.EmailPreviewsAcknowledged || !request.PushGuidanceAcknowledged ||
		(request.EmailPreference != "immediate" && request.EmailPreference != "weekly" && request.EmailPreference != "none") ||
		(request.SessionType != "trusted" && request.SessionType != "public") {
		return completedSession{}, ErrInvalidChoices
	}
	personID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return completedSession{}, errGenerateCredential
	}
	accessID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return completedSession{}, errGenerateCredential
	}
	emailID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return completedSession{}, errGenerateCredential
	}
	sessionID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return completedSession{}, errGenerateCredential
	}
	credential, credentialRaw, err := s.randomToken()
	if err != nil {
		return completedSession{}, err
	}
	var idleExpiresAt, absoluteExpiresAt *time.Time
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var complete bool
		var securityEpoch []byte
		if err := tx.NewRaw(`SELECT setup_complete, security_epoch FROM system_settings WHERE id = 1 FOR UPDATE`).Scan(ctx, &complete, &securityEpoch); err != nil {
			return err
		}
		if complete {
			return ErrSetupComplete
		}
		var curatorExists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE role = 'curator')`).Scan(ctx, &curatorExists); err != nil {
			return err
		}
		if curatorExists {
			return ErrSetupComplete
		}
		var stored challenge
		err := tx.NewRaw(`
			SELECT display_name, email, normalized_email, attempts, expires_at, verified_at,
			       verification_expires_at, consumed_at
			FROM login_challenges WHERE verification_token_hash = ? FOR UPDATE
		`, tokenHash(verificationRaw)).Scan(
			ctx, &stored.DisplayName, &stored.Email, &stored.Normalized, &stored.Attempts,
			&stored.ExpiresAt, &stored.VerifiedAt, &stored.VerificationExpiresAt, &stored.ConsumedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if !stored.VerifiedAt.Valid || !stored.VerificationExpiresAt.Valid || stored.ConsumedAt.Valid ||
			!now.Before(stored.VerificationExpiresAt.Time) {
			return ErrInvalidToken
		}
		idleExpiresAt, absoluteExpiresAt, err = sessionExpirations(request.SessionType, now)
		if err != nil {
			return err
		}
		browser, platform, clientIP, location := s.sessionMetadata(ctx)
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO people (id, display_name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{personID, stored.DisplayName, strings.ToLower(stored.DisplayName), now, now}},
			{`INSERT INTO person_roles (person_id, role, created_at) VALUES (?, 'curator', ?), (?, 'recipient', ?)`, []any{personID, now, personID, now}},
			{`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at) VALUES (?, ?, 1, 'completed', true, ?, ?, ?)`, []any{accessID, personID, now, now, now}},
			{`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, ?, true, ?)`, []any{emailID, accessID, stored.Email, stored.Normalized, now}},
			{`INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged, interest_list_acknowledged, email_previews_acknowledged, push_guidance_acknowledged, informed_choices_version, email_preference, completed_at) VALUES (?, ?, ?, ?, ?, ?, 2, ?, ?)`, []any{accessID, true, true, true, true, true, request.EmailPreference, now}},
			{`INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at) VALUES (?, ?, ?)`, []any{accessID, request.EmailPreference, now}},
			{`UPDATE login_challenges SET consumed_at = ? WHERE verification_token_hash = ?`, []any{now, tokenHash(verificationRaw)}},
			{`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at, absolute_expires_at, browser, platform, client_ip, location) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, '')::inet, NULLIF(?, ''))`, []any{sessionID, tokenHash(credentialRaw), personID, accessID, securityEpoch, request.SessionType, now, now, idleExpiresAt, absoluteExpiresAt, browser, platform, clientIP, location}},
			{`UPDATE system_settings SET setup_complete = true, updated_at = ? WHERE id = 1 AND setup_complete = false`, []any{now}},
		}
		for _, statement := range statements {
			result, err := tx.NewRaw(statement.query, statement.args...).Exec(ctx)
			if err != nil {
				return err
			}
			if strings.HasPrefix(statement.query, "UPDATE system_settings") {
				affected, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if affected != 1 {
					return ErrSetupComplete
				}
			}
		}
		if err := s.appendAudit(ctx, tx, "setup_completed", "success", &personID, &personID, &sessionID); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, "session_created", "success", &personID, &personID, &sessionID)
	})
	if err != nil {
		return completedSession{}, fmt.Errorf("complete setup: %w", err)
	}
	return completedSession{
		Credential: credential, CSRFToken: s.csrfToken(credentialRaw),
		SessionType: request.SessionType, ExpiresAt: idleExpiresAt,
	}, nil
}

// Session returns safe browser state without mutating Session activity.
func (s *Service) Session(ctx context.Context, credential string) (SessionResponse, error) {
	authenticated, err := s.authenticateForStates(ctx, credential, "completed", "onboarding")
	if err != nil {
		return SessionResponse{}, err
	}
	var curator bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, authenticated.PersonID).Scan(ctx, &curator); err != nil {
		return SessionResponse{}, err
	}
	return SessionResponse{
		DisplayName: authenticated.DisplayName, SessionType: authenticated.SessionType,
		CSRFToken: s.csrfToken(authenticated.Credential), Curator: curator,
		OnboardingRequired: authenticated.AccessState == "onboarding",
	}, nil
}

// AuthorizeSession verifies a current Recipient Session and CSRF for mutations.
func (s *Service) AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (SessionActor, error) {
	authenticated, err := s.authenticate(ctx, credential)
	if err != nil {
		return SessionActor{}, err
	}
	if mutation && !s.validCSRF(authenticated.Credential, csrfToken) {
		return SessionActor{}, ErrCSRF
	}
	var curator bool
	if err := s.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator'
	)`, authenticated.PersonID).Scan(ctx, &curator); err != nil {
		return SessionActor{}, err
	}
	return SessionActor{PersonID: authenticated.PersonID, AccessID: authenticated.AccessID, SessionID: authenticated.SessionID, Curator: curator}, nil
}

// AuthorizeInterestSession allows Interest-list self-service during Onboarding while
// keeping every general Recipient policy restricted to completed access.
func (s *Service) AuthorizeInterestSession(ctx context.Context, credential, csrfToken string, mutation bool) (SessionActor, error) {
	authenticated, err := s.authenticateForStates(ctx, credential, "completed", "onboarding")
	if err != nil {
		return SessionActor{}, err
	}
	if mutation && !s.validCSRF(authenticated.Credential, csrfToken) {
		return SessionActor{}, ErrCSRF
	}
	var curator bool
	if err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM person_roles WHERE person_id = ? AND role = 'curator')`, authenticated.PersonID).Scan(ctx, &curator); err != nil {
		return SessionActor{}, err
	}
	return SessionActor{PersonID: authenticated.PersonID, AccessID: authenticated.AccessID, SessionID: authenticated.SessionID, Curator: curator}, nil
}

// AuthorizeOnboardingSession permits only a current Onboarding generation.
func (s *Service) AuthorizeOnboardingSession(ctx context.Context, credential, csrfToken string, mutation bool) (SessionActor, error) {
	authenticated, err := s.authenticateForStates(ctx, credential, "onboarding")
	if err != nil {
		return SessionActor{}, err
	}
	if mutation && !s.validCSRF(authenticated.Credential, csrfToken) {
		return SessionActor{}, ErrCSRF
	}
	return SessionActor{PersonID: authenticated.PersonID, AccessID: authenticated.AccessID, SessionID: authenticated.SessionID}, nil
}

// NewBrowserSessionIn creates a Session inside the caller's identity transaction.
func (s *Service) NewBrowserSessionIn(ctx context.Context, tx bun.Tx, personID, accessID uuid.UUID, sessionType string, now time.Time) (BrowserSession, uuid.UUID, error) {
	idleExpiresAt, absoluteExpiresAt, err := sessionExpirations(sessionType, now)
	if err != nil {
		return BrowserSession{}, uuid.Nil, err
	}
	sessionID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return BrowserSession{}, uuid.Nil, errGenerateCredential
	}
	credential, raw, err := s.randomToken()
	if err != nil {
		return BrowserSession{}, uuid.Nil, err
	}
	var securityEpoch []byte
	if err := tx.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1 AND setup_complete`).Scan(ctx, &securityEpoch); err != nil {
		return BrowserSession{}, uuid.Nil, err
	}
	browser, platform, clientIP, location := s.sessionMetadata(ctx)
	if _, err := tx.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at, absolute_expires_at, browser, platform, client_ip, location) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, '')::inet, NULLIF(?, ''))`, sessionID, tokenHash(raw), personID, accessID, securityEpoch, sessionType, now, now, idleExpiresAt, absoluteExpiresAt, browser, platform, clientIP, location).Exec(ctx); err != nil {
		return BrowserSession{}, uuid.Nil, err
	}
	return BrowserSession{Credential: credential, CSRFToken: s.csrfToken(raw), SessionType: sessionType, ExpiresAt: idleExpiresAt}, sessionID, nil
}

// RotateBrowserSessionIn rotates privilege-bound credentials and applies the
// Recipient's informed device choice in the completion transaction.
func (s *Service) RotateBrowserSessionIn(ctx context.Context, tx bun.Tx, actor SessionActor, sessionType string, now time.Time) (BrowserSession, error) {
	idleExpiresAt, absoluteExpiresAt, err := sessionExpirations(sessionType, now)
	if err != nil {
		return BrowserSession{}, err
	}
	credential, raw, err := s.randomToken()
	if err != nil {
		return BrowserSession{}, err
	}
	result, err := tx.NewRaw(`UPDATE sessions SET credential_hash = ?, session_type = ?, last_activity_at = ?, idle_expires_at = ?, absolute_expires_at = CASE WHEN session_type = 'public' AND ? = 'public' THEN LEAST(absolute_expires_at, ?) ELSE ? END WHERE id = ? AND person_id = ? AND recipient_access_generation_id = ? AND revoked_at IS NULL`, tokenHash(raw), sessionType, now, idleExpiresAt, sessionType, absoluteExpiresAt, absoluteExpiresAt, actor.SessionID, actor.PersonID, actor.AccessID).Exec(ctx)
	if err != nil {
		return BrowserSession{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return BrowserSession{}, err
	}
	if affected != 1 {
		return BrowserSession{}, ErrUnauthenticated
	}
	return BrowserSession{Credential: credential, CSRFToken: s.csrfToken(raw), SessionType: sessionType, ExpiresAt: idleExpiresAt}, nil
}

// AuthorizeCurator verifies a current Session, the sole Curator role, and CSRF for mutations.
func (s *Service) AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (CuratorSession, error) {
	actor, err := s.AuthorizeSession(ctx, credential, csrfToken, mutation)
	if err != nil {
		return CuratorSession{}, err
	}
	if !actor.Curator {
		return CuratorSession{}, ErrNotCurator
	}
	return CuratorSession{PersonID: actor.PersonID, SessionID: actor.SessionID}, nil
}

// refresh extends a Trusted-device Session after a Session-bound CSRF check.
func (s *Service) refresh(ctx context.Context, credential, csrfToken string) (completedSession, error) {
	authenticated, err := s.authenticate(ctx, credential)
	if err != nil {
		return completedSession{}, err
	}
	if authenticated.SessionType != "trusted" {
		return completedSession{}, ErrSessionType
	}
	if !s.validCSRF(authenticated.Credential, csrfToken) {
		return completedSession{}, ErrCSRF
	}
	now := s.now().UTC()
	expiresAt := now.Add(trustedLifetime)
	result, err := s.db.NewRaw(`
		UPDATE sessions AS session
		SET last_activity_at = ?, idle_expires_at = ?
		WHERE session.id = ? AND session.revoked_at IS NULL AND session.idle_expires_at > ?
		  AND EXISTS (
			SELECT 1 FROM system_settings AS settings
			WHERE settings.id = 1 AND settings.setup_complete AND settings.security_epoch = session.security_epoch
		  )
	`, now, expiresAt, authenticated.SessionID, now).Exec(ctx)
	if err != nil {
		return completedSession{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return completedSession{}, err
	}
	if affected != 1 {
		return completedSession{}, ErrUnauthenticated
	}
	return completedSession{
		Credential: credential, CSRFToken: s.csrfToken(authenticated.Credential),
		SessionType: authenticated.SessionType, ExpiresAt: &expiresAt,
	}, nil
}

// Logout revokes the current Session after a Session-bound CSRF check.
func (s *Service) Logout(ctx context.Context, credential, csrfToken string) error {
	authenticated, err := s.authenticateForStates(ctx, credential, "completed", "onboarding")
	if err != nil {
		return err
	}
	if !s.validCSRF(authenticated.Credential, csrfToken) {
		return ErrCSRF
	}
	now := s.now().UTC()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, authenticated.SessionID).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrUnauthenticated
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = ? WHERE session_id = ? AND disabled_at IS NULL`, now, authenticated.SessionID).Exec(ctx); err != nil {
			return err
		}
		return s.appendAudit(
			ctx, tx, "session_signed_out", "success",
			&authenticated.PersonID, &authenticated.PersonID, &authenticated.SessionID,
		)
	})
}

func (s *Service) authenticate(ctx context.Context, credential string) (authenticatedSession, error) {
	return s.authenticateForStates(ctx, credential, "completed")
}

func (s *Service) authenticateForStates(ctx context.Context, credential string, states ...string) (authenticatedSession, error) {
	credentialRaw, err := decodeToken(credential)
	if err != nil {
		return authenticatedSession{}, ErrUnauthenticated
	}
	allowCompleted, allowOnboarding := false, false
	for _, state := range states {
		switch state {
		case "completed":
			allowCompleted = true
		case "onboarding":
			allowOnboarding = true
		default:
			return authenticatedSession{}, ErrUnauthenticated
		}
	}
	var result authenticatedSession
	result.Credential = credentialRaw
	now := s.now().UTC()
	err = s.db.NewRaw(`
		SELECT person.display_name, session.session_type, access.state, person.id, access.id, session.id
		FROM sessions AS session
		JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id
		 AND access.is_current = true
		 AND ((? AND access.state = 'completed') OR (? AND access.state = 'onboarding'))
		JOIN system_settings AS settings
		  ON settings.id = 1
		 AND settings.setup_complete = true
		 AND settings.security_epoch = session.security_epoch
		WHERE session.credential_hash = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > ?)
		    OR (session.session_type = 'public' AND session.absolute_expires_at > ?))
	`, allowCompleted, allowOnboarding, tokenHash(credentialRaw), now, now).Scan(
		ctx, &result.DisplayName, &result.SessionType, &result.AccessState,
		&result.PersonID, &result.AccessID, &result.SessionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authenticatedSession{}, ErrUnauthenticated
	}
	if err != nil {
		return authenticatedSession{}, err
	}
	return result, nil
}

func (s *Service) sessionMetadata(ctx context.Context) (browser, platform, clientIP, location string) {
	metadata := metadataFromContext(ctx)
	clientIP = metadata.ClientIP
	browser, platform = describeUserAgent(metadata.UserAgent)
	if s.location != nil {
		location = s.location.Lookup(net.ParseIP(clientIP))
	}
	return browser, platform, clientIP, location
}

func describeUserAgent(value string) (string, string) {
	browser, platform := "Unknown browser", "Unknown platform"
	switch {
	case strings.Contains(value, "Firefox/"), strings.Contains(value, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(value, "Edg/"), strings.Contains(value, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(value, "Chrome/"), strings.Contains(value, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(value, "Safari/"):
		browser = "Safari"
	}
	switch {
	case strings.Contains(value, "iPhone"), strings.Contains(value, "iPad"):
		platform = "iOS"
	case strings.Contains(value, "Android"):
		platform = "Android"
	case strings.Contains(value, "Windows"):
		platform = "Windows"
	case strings.Contains(value, "Macintosh"):
		platform = "macOS"
	case strings.Contains(value, "Linux"):
		platform = "Linux"
	}
	return browser, platform
}

func (s *Service) validCSRF(credential []byte, token string) bool {
	expected := s.csrfToken(credential)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

func normalizeEmail(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", "", ErrInvalidIdentity
	}
	return parsed.Address, strings.ToLower(parsed.Address), nil
}

func (s *Service) randomCode() (string, error) {
	value, err := rand.Int(s.random, big.NewInt(100_000_000))
	if err != nil {
		return "", errGenerateCredential
	}
	return fmt.Sprintf("%08d", value.Int64()), nil
}

func (s *Service) randomToken() (string, []byte, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(s.random, value); err != nil {
		return "", nil, errGenerateCredential
	}
	return hex.EncodeToString(value), value, nil
}

func (s *Service) codeHash(challenge []byte, code string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("setup-code:"))
	_, _ = mac.Write(challenge)
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (s *Service) csrfToken(credential []byte) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("csrf:"))
	_, _ = mac.Write(credential)
	return hex.EncodeToString(mac.Sum(nil))
}

func tokenHash(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}

func decodeToken(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvalidToken
	}
	return decoded, nil
}

func validCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
