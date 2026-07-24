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
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/uptrace/bun"
)

const (
	challengeLifetime = 10 * time.Minute
	maximumAttempts   = 5
	trustedLifetime   = 365 * 24 * time.Hour
	publicLifetime    = 12 * time.Hour
)

var (
	ErrSetupComplete      = errors.New("setup is complete")
	ErrInvalidCode        = errors.New("verification code is invalid or expired")
	ErrInvalidToken       = errors.New("setup verification is invalid or expired")
	ErrInvalidIdentity    = errors.New("setup identity is invalid")
	ErrInvalidChoices     = errors.New("onboarding choices are incomplete")
	ErrUnauthenticated    = errors.New("session is invalid or expired")
	ErrCSRF               = errors.New("CSRF token is invalid")
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
	VerificationToken        string `json:"verification_token" validate:"required,len=64,hexadecimal"`
	PrivacyAcknowledged      bool   `json:"privacy_acknowledged" validate:"required"`
	EngagementAcknowledged   bool   `json:"engagement_acknowledged" validate:"required"`
	InterestListAcknowledged bool   `json:"interest_list_acknowledged" validate:"required"`
	EmailPreference          string `json:"email_preference" validate:"required,oneof=immediate weekly none"`
	SessionType              string `json:"session_type" validate:"required,oneof=trusted public"`
}

// CompleteResponse is generated to TypeScript by Tygo.
type CompleteResponse struct {
	Status    string `json:"status"`
	CSRFToken string `json:"csrf_token"`
}

// SessionResponse is generated to TypeScript by Tygo.
type SessionResponse struct {
	DisplayName string `json:"display_name"`
	SessionType string `json:"session_type"`
	CSRFToken   string `json:"csrf_token"`
}

type challenge struct {
	DisplayName string
	Email       string
	Normalized  string
	Attempts    int
	ExpiresAt   time.Time
	VerifiedAt  sql.NullTime
	ConsumedAt  sql.NullTime
}

type completedSession struct {
	Credential  string
	CSRFToken   string
	SessionType string
}

type authenticatedSession struct {
	DisplayName string
	SessionType string
	Credential  []byte
}

// Service coordinates setup state, required email, identity, and Session persistence.
type Service struct {
	db       *bun.DB
	delivery *emaildelivery.Service
	secret   []byte
	now      func() time.Time
	random   io.Reader
}

func New(db *bun.DB, delivery *emaildelivery.Service, secret string) *Service {
	return &Service{db: db, delivery: delivery, secret: []byte(secret), now: time.Now, random: rand.Reader}
}

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
	now := s.now().UTC()
	expiresAt := now.Add(challengeLifetime)
	codeHash := s.codeHash(challengeRaw, code)

	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var complete bool
		if err := tx.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1 FOR SHARE`).Scan(ctx, &complete); err != nil {
			return err
		}
		if complete {
			return ErrSetupComplete
		}
		deliveryID, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind:      emaildelivery.KindSetupCode,
			Recipient: email,
			Subject:   "Your Memento setup code",
			Body:      fmt.Sprintf("Your Memento setup code is %s. It expires in 10 minutes.", code),
		})
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`
			INSERT INTO login_challenges (
				id, challenge_hash, code_hash, display_name, email, normalized_email,
				email_delivery_id, expires_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, challengeUUID, tokenHash(challengeRaw), codeHash, displayName, email, normalizedEmail, deliveryID, expiresAt, now).Exec(ctx)
		return err
	})
	if err != nil {
		return RequestCodeResponse{}, fmt.Errorf("request setup code: %w", err)
	}
	return RequestCodeResponse{ChallengeID: challengeID, Status: "code_sent"}, nil
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
	now := s.now().UTC()
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
			return nil
		}
		if err != nil {
			return err
		}
		if stored.Attempts >= maximumAttempts || !now.Before(stored.ExpiresAt) || stored.VerifiedAt.Valid || stored.ConsumedAt.Valid {
			invalid = true
			return nil
		}
		var expectedHash []byte
		if err := tx.NewRaw(`SELECT code_hash FROM login_challenges WHERE challenge_hash = ?`, tokenHash(challengeRaw)).Scan(ctx, &expectedHash); err != nil {
			return err
		}
		actualHash := s.codeHash(challengeRaw, request.Code)
		if subtle.ConstantTimeCompare(expectedHash, actualHash) != 1 {
			invalid = true
			_, err := tx.NewRaw(`UPDATE login_challenges SET attempts = attempts + 1 WHERE challenge_hash = ?`, tokenHash(challengeRaw)).Exec(ctx)
			return err
		}
		_, err = tx.NewRaw(`
			UPDATE login_challenges
			SET verified_at = ?, verification_token_hash = ?
			WHERE challenge_hash = ?
		`, now, tokenHash(verificationRaw), tokenHash(challengeRaw)).Exec(ctx)
		return err
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
	now := s.now().UTC()
	var idleExpiresAt, absoluteExpiresAt *time.Time
	if request.SessionType == "trusted" {
		expiry := now.Add(trustedLifetime)
		idleExpiresAt = &expiry
	} else {
		expiry := now.Add(publicLifetime)
		absoluteExpiresAt = &expiry
	}

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
			SELECT display_name, email, normalized_email, attempts, expires_at, verified_at, consumed_at
			FROM login_challenges WHERE verification_token_hash = ? FOR UPDATE
		`, tokenHash(verificationRaw)).Scan(ctx, &stored.DisplayName, &stored.Email, &stored.Normalized, &stored.Attempts, &stored.ExpiresAt, &stored.VerifiedAt, &stored.ConsumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if !stored.VerifiedAt.Valid || stored.ConsumedAt.Valid || !now.Before(stored.ExpiresAt) {
			return ErrInvalidToken
		}
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO people (id, display_name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{personID, stored.DisplayName, strings.ToLower(stored.DisplayName), now, now}},
			{`INSERT INTO person_roles (person_id, role, created_at) VALUES (?, 'curator', ?), (?, 'recipient', ?)`, []any{personID, now, personID, now}},
			{`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at) VALUES (?, ?, 1, 'completed', true, ?, ?, ?)`, []any{accessID, personID, now, now, now}},
			{`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, ?, true, ?)`, []any{emailID, accessID, stored.Email, stored.Normalized, now}},
			{`INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged, interest_list_acknowledged, email_preference, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{accessID, true, true, true, request.EmailPreference, now}},
			{`INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at) VALUES (?, ?, ?)`, []any{accessID, request.EmailPreference, now}},
			{`UPDATE login_challenges SET consumed_at = ? WHERE verification_token_hash = ?`, []any{now, tokenHash(verificationRaw)}},
			{`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, created_at, last_activity_at, idle_expires_at, absolute_expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []any{sessionID, tokenHash(credentialRaw), personID, accessID, securityEpoch, request.SessionType, now, now, idleExpiresAt, absoluteExpiresAt}},
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
		return nil
	})
	if err != nil {
		return completedSession{}, fmt.Errorf("complete setup: %w", err)
	}
	return completedSession{Credential: credential, CSRFToken: s.csrfToken(credentialRaw), SessionType: request.SessionType}, nil
}

// Session returns safe browser state without mutating Session activity.
func (s *Service) Session(ctx context.Context, credential string) (SessionResponse, error) {
	authenticated, err := s.authenticate(ctx, credential)
	if err != nil {
		return SessionResponse{}, err
	}
	return SessionResponse{
		DisplayName: authenticated.DisplayName,
		SessionType: authenticated.SessionType,
		CSRFToken:   s.csrfToken(authenticated.Credential),
	}, nil
}

// Logout revokes the current Session after a Session-bound CSRF check.
func (s *Service) Logout(ctx context.Context, credential, csrfToken string) error {
	authenticated, err := s.authenticate(ctx, credential)
	if err != nil {
		return err
	}
	expected := s.csrfToken(authenticated.Credential)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(csrfToken)) != 1 {
		return ErrCSRF
	}
	result, err := s.db.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE credential_hash = ? AND revoked_at IS NULL`, s.now().UTC(), tokenHash(authenticated.Credential)).Exec(ctx)
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
	return nil
}

func (s *Service) authenticate(ctx context.Context, credential string) (authenticatedSession, error) {
	credentialRaw, err := decodeToken(credential)
	if err != nil {
		return authenticatedSession{}, ErrUnauthenticated
	}
	var result authenticatedSession
	result.Credential = credentialRaw
	err = s.db.NewRaw(`
		SELECT person.display_name, session.session_type
		FROM sessions AS session
		JOIN people AS person ON person.id = session.person_id AND person.archived_at IS NULL
		JOIN recipient_access_generations AS access
		  ON access.id = session.recipient_access_generation_id
		 AND access.person_id = session.person_id
		 AND access.is_current = true
		 AND access.state = 'completed'
		JOIN system_settings AS settings
		  ON settings.id = 1
		 AND settings.setup_complete = true
		 AND settings.security_epoch = session.security_epoch
		WHERE session.credential_hash = ?
		  AND session.revoked_at IS NULL
		  AND ((session.session_type = 'trusted' AND session.idle_expires_at > ?)
		    OR (session.session_type = 'public' AND session.absolute_expires_at > ?))
	`, tokenHash(credentialRaw), s.now().UTC(), s.now().UTC()).Scan(ctx, &result.DisplayName, &result.SessionType)
	if errors.Is(err, sql.ErrNoRows) {
		return authenticatedSession{}, ErrUnauthenticated
	}
	if err != nil {
		return authenticatedSession{}, err
	}
	return result, nil
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
