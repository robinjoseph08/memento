// Package sessions owns passwordless sign-in and separately revocable browser Sessions.
package sessions

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	codeLifetime    = 10 * time.Minute
	maximumAttempts = 5
	maximumLabel    = 80
)

var (
	ErrInvalidIdentity  = errors.New("identity is invalid")
	ErrInvalidCode      = errors.New("code is invalid or expired")
	ErrInvalidSession   = errors.New("Session is unavailable")
	ErrInvalidLabel     = errors.New("Session label is invalid")
	ErrEmailInUse       = errors.New("email is already in use")
	ErrEmailUnchanged   = errors.New("email is unchanged")
	ErrChangeNotFound   = errors.New("email change is unavailable")
	ErrRecoveryNotFound = errors.New("recovery is unavailable")
	ErrRecoveryCurator  = errors.New("the Curator cannot recover their own email here")
	errRandom           = errors.New("generate secure Session credential")
)

// SignInRequest starts an enumeration-resistant passwordless sign-in.
type SignInRequest struct {
	Email string `json:"email" mod:"trim" validate:"required,email,max=320"`
}

// SignInStartResponse is identical whether or not the email is eligible.
type SignInStartResponse struct {
	ChallengeID string `json:"challenge_id"`
	Status      string `json:"status"`
}

// SignInVerifyRequest verifies one code and chooses the Session policy.
type SignInVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,len=64,hexadecimal"`
	Code        string `json:"code" validate:"required,len=8,numeric"`
	SessionType string `json:"session_type" validate:"required,oneof=trusted public"`
}

// SignInResponse confirms that the opaque cookie was issued.
type SignInResponse struct {
	Status  string `json:"status"`
	session setup.BrowserSession
}

// Session is safe for Recipient and Curator Session inspection.
type Session struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Browser        string     `json:"browser"`
	Platform       string     `json:"platform"`
	SessionType    string     `json:"session_type"`
	CreatedAt      time.Time  `json:"created_at"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	Status         string     `json:"status"`
	Current        bool       `json:"current"`
	Location       *string    `json:"location,omitempty"`
	PushAllowed    bool       `json:"push_allowed"`
}

// ListResponse lists Sessions without exposing credential hashes or raw IP data.
type ListResponse struct {
	Sessions []Session `json:"sessions"`
}

// RenameRequest labels one browser or device.
type RenameRequest struct {
	Label string `json:"label" mod:"trim" validate:"max=80"`
}

// EmailChangeRequest sends fresh codes to both current and replacement addresses.
type EmailChangeRequest struct {
	NewEmail string `json:"new_email" mod:"trim" validate:"required,email,max=320"`
}

// EmailChangeStartResponse identifies the paired verification without exposing either code.
type EmailChangeStartResponse struct {
	RequestID string    `json:"request_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// EmailChangeCompleteRequest proves both mailboxes.
type EmailChangeCompleteRequest struct {
	RequestID string `json:"request_id" validate:"required,uuid"`
	OldCode   string `json:"old_code" validate:"required,len=8,numeric"`
	NewCode   string `json:"new_code" validate:"required,len=8,numeric"`
}

// EmailChangeResponse confirms identity rotation and supplies the rotated CSRF token.
type EmailChangeResponse struct {
	Status    string `json:"status"`
	CSRFToken string `json:"csrf_token"`
	session   setup.BrowserSession
}

// RecoveryRequest starts Curator-assisted recovery to a fresh mailbox.
type RecoveryRequest struct {
	NewEmail string `json:"new_email" mod:"trim" validate:"required,email,max=320"`
}

// RecoveryStartResponse identifies a new-mailbox verification.
type RecoveryStartResponse struct {
	RecoveryID string    `json:"recovery_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// RecoveryCompleteRequest proves the replacement mailbox.
type RecoveryCompleteRequest struct {
	RecoveryID string `json:"recovery_id" validate:"required,uuid"`
	Code       string `json:"code" validate:"required,len=8,numeric"`
}

type Service struct {
	db       *bun.DB
	delivery *emaildelivery.Service
	auth     *setup.Service
	security config.SecurityConfig
	secret   []byte
	now      func() time.Time
	random   io.Reader
}

func New(db *bun.DB, delivery *emaildelivery.Service, auth *setup.Service, security config.SecurityConfig) *Service {
	return &Service{db: db, delivery: delivery, auth: auth, security: security, secret: []byte(security.Secret), now: time.Now, random: rand.Reader}
}

// RequestSignIn always returns the same accepted shape after applying global delivery and rate policy.
func (s *Service) RequestSignIn(ctx context.Context, request SignInRequest) (SignInStartResponse, error) {
	_, normalized, err := normalizeEmail(request.Email)
	if err != nil {
		return SignInStartResponse{}, ErrInvalidIdentity
	}
	challengeID, challengeRaw, err := s.randomToken()
	if err != nil {
		return SignInStartResponse{}, err
	}
	response := SignInStartResponse{ChallengeID: challengeID, Status: "accepted"}
	if !s.delivery.Configured() {
		return SignInStartResponse{}, emaildelivery.ErrNotConfigured
	}
	code, err := s.randomCode()
	if err != nil {
		return SignInStartResponse{}, err
	}
	challengeUUID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return SignInStartResponse{}, errRandom
	}
	now := s.now().UTC()
	expiresAt := now.Add(codeLifetime)
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var accessID, emailID uuid.UUID
		var email, name string
		err := tx.NewRaw(`
			SELECT access.id, recipient_email.id, recipient_email.email, person.display_name
			FROM recipient_emails AS recipient_email
			JOIN recipient_access_generations AS access ON access.id = recipient_email.recipient_access_generation_id
			JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
			JOIN system_settings AS settings ON settings.id = 1 AND settings.setup_complete
			WHERE recipient_email.normalized_email = ? AND recipient_email.is_current
			  AND access.is_current AND access.state = 'completed'
			  AND (NOT settings.recovery_hold OR EXISTS (
				SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator'
			  ))
			FOR UPDATE OF recipient_email, access, settings
		`, normalized).Scan(ctx, &accessID, &emailID, &email, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		deliveryID, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindSignInCode, Recipient: email, Subject: "Your Memento sign-in code",
			Body: fmt.Sprintf("Hello %s,\n\nYour Memento sign-in code is %s. It expires in 10 minutes and can be used once.", name, code), DeliverBefore: &expiresAt,
		})
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`INSERT INTO sign_in_challenges (id, challenge_hash, code_hash, recipient_access_generation_id, recipient_email_id, email_delivery_id, security_epoch, expires_at, created_at)
			SELECT ?, ?, ?, ?, ?, ?, security_epoch, ?, ? FROM system_settings WHERE id = 1`, challengeUUID, digest(challengeRaw), s.codeHash("sign-in", challengeRaw, code), accessID, emailID, deliveryID, expiresAt, now).Exec(ctx)
		return err
	})
	if err != nil {
		return SignInStartResponse{}, err
	}
	return response, nil
}

// VerifySignIn consumes one challenge and atomically issues an opaque Session.
func (s *Service) VerifySignIn(ctx context.Context, request SignInVerifyRequest) (SignInResponse, error) {
	challengeRaw, err := decodeToken(request.ChallengeID)
	if err != nil || !validCode(request.Code) || !validSessionType(request.SessionType) || s.auth == nil {
		return SignInResponse{}, ErrInvalidCode
	}
	var response SignInResponse
	invalid := false
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var challengeID, accessID, personID uuid.UUID
		var expected []byte
		var attempts int
		var expiresAt time.Time
		var consumedAt *time.Time
		err := tx.NewRaw(`
			SELECT challenge.id, challenge.code_hash, challenge.attempts, challenge.expires_at, challenge.consumed_at,
			       access.id, access.person_id
			FROM sign_in_challenges AS challenge
			JOIN recipient_access_generations AS access ON access.id = challenge.recipient_access_generation_id AND access.is_current AND access.state = 'completed'
			JOIN recipient_emails AS recipient_email ON recipient_email.id = challenge.recipient_email_id AND recipient_email.recipient_access_generation_id = access.id AND recipient_email.is_current
			JOIN system_settings AS settings ON settings.id = 1 AND settings.security_epoch = challenge.security_epoch
			WHERE challenge.challenge_hash = ?
			  AND (NOT settings.recovery_hold OR EXISTS (
				SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator'
			  ))
			FOR UPDATE OF challenge, access, settings
		`, digest(challengeRaw)).Scan(ctx, &challengeID, &expected, &attempts, &expiresAt, &consumedAt, &accessID, &personID)
		if errors.Is(err, sql.ErrNoRows) {
			invalid = true
			return nil
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if attempts >= maximumAttempts || consumedAt != nil || !now.Before(expiresAt) || subtle.ConstantTimeCompare(expected, s.codeHash("sign-in", challengeRaw, request.Code)) != 1 {
			invalid = true
			if attempts < maximumAttempts && consumedAt == nil {
				_, err = tx.NewRaw(`UPDATE sign_in_challenges SET attempts = attempts + 1 WHERE id = ?`, challengeID).Exec(ctx)
			}
			return err
		}
		browserSession, sessionID, err := s.auth.NewBrowserSessionIn(ctx, tx, personID, accessID, request.SessionType, now)
		if err != nil {
			return err
		}
		if _, err = tx.NewRaw(`UPDATE sign_in_challenges SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, now, challengeID).Exec(ctx); err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, setup.SessionActor{PersonID: personID, AccessID: accessID, SessionID: sessionID}, personID, "session_created", map[string]any{"session_type": request.SessionType}); err != nil {
			return err
		}
		response = SignInResponse{Status: "signed_in", session: browserSession}
		return nil
	})
	if err != nil {
		return SignInResponse{}, err
	}
	if invalid {
		return SignInResponse{}, ErrInvalidCode
	}
	return response, nil
}

// ListSelf lists all retained Sessions in the current access generation.
func (s *Service) ListSelf(ctx context.Context, actor setup.SessionActor) (ListResponse, error) {
	return s.list(ctx, actor.PersonID, actor.AccessID, actor.SessionID)
}

// ListRecipient lets the Curator inspect Sessions without raw IP disclosure.
func (s *Service) ListRecipient(ctx context.Context, personID uuid.UUID) (ListResponse, error) {
	var accessID uuid.UUID
	if err := s.db.NewRaw(`SELECT id FROM recipient_access_generations WHERE person_id = ? AND is_current`, personID).Scan(ctx, &accessID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ListResponse{}, ErrInvalidSession
		}
		return ListResponse{}, err
	}
	return s.list(ctx, personID, accessID, uuid.Nil)
}

func (s *Service) list(ctx context.Context, personID, accessID, currentID uuid.UUID) (ListResponse, error) {
	type row struct {
		ID                                          uuid.UUID
		Label, Browser, Platform, SessionType       string
		CreatedAt, LastActivityAt                   time.Time
		IdleExpiresAt, AbsoluteExpiresAt, RevokedAt *time.Time
		Location                                    *string
	}
	rows := make([]row, 0)
	if err := s.db.NewRaw(`SELECT id, label, browser, platform, session_type, created_at, last_activity_at, idle_expires_at, absolute_expires_at, revoked_at, location FROM sessions WHERE person_id = ? AND recipient_access_generation_id = ? ORDER BY created_at DESC, id DESC`, personID, accessID).Scan(ctx, &rows); err != nil {
		return ListResponse{}, err
	}
	now := s.now().UTC()
	response := ListResponse{Sessions: make([]Session, 0, len(rows))}
	for _, row := range rows {
		expiresAt := row.AbsoluteExpiresAt
		if expiresAt == nil {
			expiresAt = row.IdleExpiresAt
		}
		status := "active"
		if row.RevokedAt != nil {
			status = "revoked"
		} else if expiresAt == nil || !now.Before(*expiresAt) {
			status = "expired"
		}
		value := Session{ID: row.ID.String(), Label: row.Label, Browser: row.Browser, Platform: row.Platform, SessionType: row.SessionType, CreatedAt: row.CreatedAt, LastActivityAt: row.LastActivityAt, RevokedAt: row.RevokedAt, Status: status, Current: row.ID == currentID, Location: row.Location, PushAllowed: row.SessionType == "trusted" && status == "active"}
		if expiresAt != nil {
			value.ExpiresAt = *expiresAt
		}
		response.Sessions = append(response.Sessions, value)
	}
	return response, nil
}

// Rename changes only the current Recipient's Session label.
func (s *Service) Rename(ctx context.Context, actor setup.SessionActor, sessionID uuid.UUID, request RenameRequest) error {
	label := strings.TrimSpace(request.Label)
	if len([]rune(label)) > maximumLabel {
		return ErrInvalidLabel
	}
	result, err := s.db.NewRaw(`UPDATE sessions SET label = ? WHERE id = ? AND person_id = ? AND recipient_access_generation_id = ?`, label, sessionID, actor.PersonID, actor.AccessID).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrInvalidSession
	}
	return nil
}

// Revoke invalidates one Session and its linked push subscription atomically.
func (s *Service) Revoke(ctx context.Context, actor setup.SessionActor, sessionID uuid.UUID) (bool, error) {
	now := s.now().UTC()
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE id = ? AND person_id = ? AND recipient_access_generation_id = ? AND revoked_at IS NULL`, now, sessionID, actor.PersonID, actor.AccessID).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrInvalidSession
		}
		if _, err = tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = ? WHERE session_id = ? AND disabled_at IS NULL`, now, sessionID).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, actor.PersonID, "session_revoked", map[string]any{"session_id": sessionID.String()})
	})
	return sessionID == actor.SessionID, err
}

// SignOutAll invalidates every Session and push subscription for the Recipient.
func (s *Service) SignOutAll(ctx context.Context, actor setup.SessionActor) error {
	now := s.now().UTC()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE person_id = ? AND recipient_access_generation_id = ? AND revoked_at IS NULL`, now, actor.PersonID, actor.AccessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = ? WHERE person_id = ? AND disabled_at IS NULL`, now, actor.PersonID).Exec(ctx); err != nil {
			return err
		}
		return appendAudit(ctx, tx, actor, actor.PersonID, "sessions_signed_out_all", nil)
	})
}

// StartEmailChange queues independent proofs to both addresses.
func (s *Service) StartEmailChange(ctx context.Context, actor setup.SessionActor, request EmailChangeRequest) (EmailChangeStartResponse, error) {
	newEmail, normalized, err := normalizeEmail(request.NewEmail)
	if err != nil {
		return EmailChangeStartResponse{}, ErrInvalidIdentity
	}
	if !s.delivery.Configured() {
		return EmailChangeStartResponse{}, emaildelivery.ErrNotConfigured
	}
	requestID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return EmailChangeStartResponse{}, errRandom
	}
	oldCode, err := s.randomCode()
	if err != nil {
		return EmailChangeStartResponse{}, err
	}
	newCode, err := s.randomCode()
	if err != nil {
		return EmailChangeStartResponse{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(codeLifetime)
	var response EmailChangeStartResponse
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var oldEmail, oldNormalized string
		if err := tx.NewRaw(`SELECT email, normalized_email FROM recipient_emails WHERE recipient_access_generation_id = ? AND is_current FOR UPDATE`, actor.AccessID).Scan(ctx, &oldEmail, &oldNormalized); err != nil {
			return err
		}
		if normalized == oldNormalized {
			return ErrEmailUnchanged
		}
		var used bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_emails WHERE normalized_email = ? AND is_current)`, normalized).Scan(ctx, &used); err != nil {
			return err
		}
		if used {
			return ErrEmailInUse
		}
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{Kind: emaildelivery.KindEmailChangeOldCode, Recipient: oldEmail, Subject: "Confirm your Memento email change", Body: fmt.Sprintf("Your old-address confirmation code is %s. It expires in 10 minutes.", oldCode), DeliverBefore: &expiresAt}); err != nil {
			return err
		}
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{Kind: emaildelivery.KindEmailChangeNewCode, Recipient: newEmail, Subject: "Confirm your new Memento email", Body: fmt.Sprintf("Your new-address confirmation code is %s. It expires in 10 minutes.", newCode), DeliverBefore: &expiresAt}); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO email_change_requests (id, person_id, recipient_access_generation_id, session_id, old_email, new_email, new_normalized_email, old_code_hash, new_code_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, requestID, actor.PersonID, actor.AccessID, actor.SessionID, oldEmail, newEmail, normalized, s.codeHash("email-change-old", requestID[:], oldCode), s.codeHash("email-change-new", requestID[:], newCode), expiresAt, now).Exec(ctx)
		return err
	})
	if err != nil {
		return EmailChangeStartResponse{}, err
	}
	response = EmailChangeStartResponse{RequestID: requestID.String(), ExpiresAt: expiresAt}
	return response, nil
}

// CompleteEmailChange proves both addresses, changes history, revokes sibling Sessions, and rotates the current credential.
func (s *Service) CompleteEmailChange(ctx context.Context, actor setup.SessionActor, request EmailChangeCompleteRequest) (EmailChangeResponse, error) {
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil || !validCode(request.OldCode) || !validCode(request.NewCode) {
		return EmailChangeResponse{}, ErrInvalidCode
	}
	var response EmailChangeResponse
	invalid := false
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var oldHash, newHash []byte
		var newEmail, normalized string
		var attempts int
		var expiresAt time.Time
		var consumedAt *time.Time
		err := tx.NewRaw(`
			SELECT request.old_code_hash, request.new_code_hash, request.new_email, request.new_normalized_email,
			       request.attempts, request.expires_at, request.consumed_at
			FROM email_change_requests AS request
			JOIN recipient_emails AS current_email
			  ON current_email.recipient_access_generation_id = request.recipient_access_generation_id
			 AND current_email.is_current
			 AND current_email.normalized_email = lower(request.old_email)
			WHERE request.id = ? AND request.person_id = ?
			  AND request.recipient_access_generation_id = ? AND request.session_id = ?
			FOR UPDATE OF request, current_email
		`, requestID, actor.PersonID, actor.AccessID, actor.SessionID).Scan(ctx, &oldHash, &newHash, &newEmail, &normalized, &attempts, &expiresAt, &consumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChangeNotFound
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		valid := attempts < maximumAttempts && consumedAt == nil && now.Before(expiresAt) && subtle.ConstantTimeCompare(oldHash, s.codeHash("email-change-old", requestID[:], request.OldCode)) == 1 && subtle.ConstantTimeCompare(newHash, s.codeHash("email-change-new", requestID[:], request.NewCode)) == 1
		if !valid {
			invalid = true
			if attempts < maximumAttempts && consumedAt == nil {
				_, err = tx.NewRaw(`UPDATE email_change_requests SET attempts = attempts + 1 WHERE id = ?`, requestID).Exec(ctx)
			}
			return err
		}
		var used bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_emails WHERE normalized_email = ? AND is_current AND recipient_access_generation_id <> ?)`, normalized, actor.AccessID).Scan(ctx, &used); err != nil {
			return err
		}
		if used {
			return ErrEmailInUse
		}
		if _, err := tx.NewRaw(`UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current`, now, actor.AccessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, ?, true, ?)`, uuid.New(), actor.AccessID, newEmail, normalized, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE sign_in_challenges SET consumed_at = ? WHERE recipient_access_generation_id = ? AND consumed_at IS NULL`, now, actor.AccessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE person_id = ? AND id <> ? AND revoked_at IS NULL`, now, actor.PersonID, actor.SessionID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions SET disabled_at = ? WHERE person_id = ? AND session_id <> ? AND disabled_at IS NULL`, now, actor.PersonID, actor.SessionID).Exec(ctx); err != nil {
			return err
		}
		var sessionType string
		if err := tx.NewRaw(`SELECT session_type FROM sessions WHERE id = ?`, actor.SessionID).Scan(ctx, &sessionType); err != nil {
			return err
		}
		browserSession, err := s.auth.RotateBrowserSessionIn(ctx, tx, actor, sessionType, now)
		if err != nil {
			return err
		}
		if _, err = tx.NewRaw(`UPDATE email_change_requests SET consumed_at = ? WHERE recipient_access_generation_id = ? AND consumed_at IS NULL`, now, actor.AccessID).Exec(ctx); err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, actor, actor.PersonID, "recipient_email_changed", nil); err != nil {
			return err
		}
		response = EmailChangeResponse{Status: "changed", CSRFToken: browserSession.CSRFToken, session: browserSession}
		return nil
	})
	if err != nil {
		return EmailChangeResponse{}, err
	}
	if invalid {
		return EmailChangeResponse{}, ErrInvalidCode
	}
	return response, nil
}

// StartRecovery queues a new-mailbox proof without contacting the unavailable old address.
func (s *Service) StartRecovery(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, request RecoveryRequest) (RecoveryStartResponse, error) {
	newEmail, normalized, err := normalizeEmail(request.NewEmail)
	if err != nil {
		return RecoveryStartResponse{}, ErrInvalidIdentity
	}
	if !s.delivery.Configured() {
		return RecoveryStartResponse{}, emaildelivery.ErrNotConfigured
	}
	recoveryID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return RecoveryStartResponse{}, errRandom
	}
	code, err := s.randomCode()
	if err != nil {
		return RecoveryStartResponse{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(codeLifetime)
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var accessID uuid.UUID
		var curator bool
		if err := tx.NewRaw(`SELECT access.id, EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator') FROM recipient_access_generations AS access WHERE access.person_id = ? AND access.is_current AND access.state IN ('completed', 'suspended') FOR UPDATE`, personID).Scan(ctx, &accessID, &curator); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrRecoveryNotFound
			}
			return err
		}
		if curator {
			return ErrRecoveryCurator
		}
		var used bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_emails WHERE normalized_email = ? AND is_current)`, normalized).Scan(ctx, &used); err != nil {
			return err
		}
		if used {
			return ErrEmailInUse
		}
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{Kind: emaildelivery.KindCuratorRecoveryCode, Recipient: newEmail, Subject: "Confirm Memento account recovery", Body: fmt.Sprintf("Your Curator-assisted Memento recovery code is %s. It expires in 10 minutes.", code), DeliverBefore: &expiresAt}); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO curator_recovery_requests (id, person_id, recipient_access_generation_id, new_email, new_normalized_email, code_hash, expires_at, created_by_person_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, recoveryID, personID, accessID, newEmail, normalized, s.codeHash("recovery", recoveryID[:], code), expiresAt, actor.PersonID, now).Exec(ctx)
		return err
	})
	if err != nil {
		return RecoveryStartResponse{}, err
	}
	return RecoveryStartResponse{RecoveryID: recoveryID.String(), ExpiresAt: expiresAt}, nil
}

// CompleteRecovery changes only login email and revokes every target Session and push subscription.
func (s *Service) CompleteRecovery(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, request RecoveryCompleteRequest) error {
	recoveryID, err := uuid.Parse(request.RecoveryID)
	if err != nil || !validCode(request.Code) {
		return ErrInvalidCode
	}
	invalid := false
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var accessID uuid.UUID
		var email, normalized string
		var codeHash []byte
		var attempts int
		var expiresAt time.Time
		var consumedAt *time.Time
		err := tx.NewRaw(`
			SELECT request.recipient_access_generation_id, request.new_email, request.new_normalized_email,
			       request.code_hash, request.attempts, request.expires_at, request.consumed_at
			FROM curator_recovery_requests AS request
			JOIN recipient_access_generations AS access
			  ON access.id = request.recipient_access_generation_id
			 AND access.person_id = request.person_id
			 AND access.is_current
			 AND access.state IN ('completed', 'suspended')
			WHERE request.id = ? AND request.person_id = ?
			FOR UPDATE OF request, access
		`, recoveryID, personID).Scan(ctx, &accessID, &email, &normalized, &codeHash, &attempts, &expiresAt, &consumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecoveryNotFound
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		valid := attempts < maximumAttempts && consumedAt == nil && now.Before(expiresAt) && subtle.ConstantTimeCompare(codeHash, s.codeHash("recovery", recoveryID[:], request.Code)) == 1
		if !valid {
			invalid = true
			if attempts < maximumAttempts && consumedAt == nil {
				_, err = tx.NewRaw(`UPDATE curator_recovery_requests SET attempts = attempts + 1 WHERE id = ?`, recoveryID).Exec(ctx)
			}
			return err
		}
		var used bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_emails WHERE normalized_email = ? AND is_current AND recipient_access_generation_id <> ?)`, normalized, accessID).Scan(ctx, &used); err != nil {
			return err
		}
		if used {
			return ErrEmailInUse
		}
		if _, err := tx.NewRaw(`UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current`, now, accessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, ?, true, ?)`, uuid.New(), accessID, email, normalized, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE sign_in_challenges SET consumed_at = ? WHERE recipient_access_generation_id = ? AND consumed_at IS NULL`, now, accessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE sessions SET revoked_at = ? WHERE recipient_access_generation_id = ? AND revoked_at IS NULL`, now, accessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE push_subscriptions AS push SET disabled_at = ? WHERE push.person_id = ? AND push.disabled_at IS NULL AND EXISTS (SELECT 1 FROM sessions AS session WHERE session.id = push.session_id AND session.recipient_access_generation_id = ?)`, now, personID, accessID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE curator_recovery_requests SET consumed_at = ? WHERE id = ?`, now, recoveryID).Exec(ctx); err != nil {
			return err
		}
		return appendCuratorAudit(ctx, tx, actor, personID, "recipient_email_recovered", nil)
	})
	if err != nil {
		return err
	}
	if invalid {
		return ErrInvalidCode
	}
	return nil
}

func normalizeEmail(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 320 {
		return "", "", ErrInvalidIdentity
	}
	return parsed.Address, strings.ToLower(parsed.Address), nil
}
func validSessionType(value string) bool { return value == "trusted" || value == "public" }
func validCode(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func (s *Service) randomCode() (string, error) {
	value, err := rand.Int(s.random, big.NewInt(100_000_000))
	if err != nil {
		return "", errRandom
	}
	return fmt.Sprintf("%08d", value.Int64()), nil
}
func (s *Service) randomToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", nil, errRandom
	}
	return hex.EncodeToString(raw), raw, nil
}
func decodeToken(value string) ([]byte, error) {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return nil, ErrInvalidCode
	}
	return raw, nil
}
func digest(value []byte) []byte { hash := sha256.Sum256(value); return hash[:] }
func (s *Service) codeHash(purpose string, binding []byte, code string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("sessions:" + purpose + ":"))
	_, _ = mac.Write(binding)
	_, _ = mac.Write([]byte(":" + code))
	return mac.Sum(nil)
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.SessionActor, subject uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata) VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)`, actor.PersonID, subject, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
func appendCuratorAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject uuid.UUID, action string, metadata map[string]any) error {
	return appendAudit(ctx, tx, setup.SessionActor{PersonID: actor.PersonID, SessionID: actor.SessionID}, subject, action, metadata)
}
