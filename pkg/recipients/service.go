// Package recipients owns Pending Recipient designation and Curator-controlled Invitations.
package recipients

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/uptrace/bun"
)

const (
	invitationLifetime = 14 * 24 * time.Hour
	reminderDelay      = 7 * 24 * time.Hour
)

var (
	ErrPersonNotFound        = errors.New("person not found")
	ErrPersonUnavailable     = errors.New("person cannot become a Pending Recipient")
	ErrAlreadyRecipient      = errors.New("person already has current Recipient access")
	ErrEmailInvalid          = errors.New("login email is invalid")
	ErrEmailInUse            = errors.New("login email is already in use")
	ErrRecipientNotFound     = errors.New("pending Recipient not found")
	ErrInvitationExists      = errors.New("a live Invitation already exists")
	ErrInvitationNotFound    = errors.New("Invitation not found")
	ErrInvitationNotLive     = errors.New("Invitation is not live")
	ErrInvitationNotSent     = errors.New("Invitation initial delivery has not completed")
	ErrInvitationStale       = errors.New("Invitation changed since it was inspected")
	ErrInvitationToken       = errors.New("Invitation is invalid")
	ErrInvitationState       = errors.New("Recipient state does not permit this Invitation action")
	ErrOnboardingUnavailable = errors.New("Onboarding is not available")
	ErrOnboardingChoices     = errors.New("Onboarding choices are incomplete")
	errGenerateToken         = errors.New("generate Invitation token")
)

// DesignateRequest creates Recipient access without sending an Invitation.
type DesignateRequest struct {
	Email string `json:"email" mod:"trim" validate:"required,email,max=320"`
}

// Access identifies the current access generation.
type Access struct {
	ID         string `json:"id"`
	Generation int    `json:"generation"`
	State      string `json:"state"`
}

// DeliveryStatus exposes only safe state needed for Curator recovery actions.
type DeliveryStatus struct {
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Failure     *string    `json:"failure,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
}

// Invitation is the Curator-safe Invitation status. It never contains a token.
type Invitation struct {
	ID                            string          `json:"id"`
	Status                        string          `json:"status"`
	IssuedAt                      time.Time       `json:"issued_at"`
	ExpiresAt                     time.Time       `json:"expires_at"`
	SentAt                        *time.Time      `json:"sent_at,omitempty"`
	AcceptedAt                    *time.Time      `json:"accepted_at,omitempty"`
	RevokedAt                     *time.Time      `json:"revoked_at,omitempty"`
	SupersededAt                  *time.Time      `json:"superseded_at,omitempty"`
	AutomaticReminderScheduledAt  time.Time       `json:"automatic_reminder_scheduled_at"`
	AutomaticRemindedAt           *time.Time      `json:"automatic_reminded_at,omitempty"`
	LastManualReminderRequestedAt *time.Time      `json:"last_manual_reminder_requested_at,omitempty"`
	LastManualRemindedAt          *time.Time      `json:"last_manual_reminded_at,omitempty"`
	ManualReminderCount           int             `json:"manual_reminder_count"`
	InitialDelivery               *DeliveryStatus `json:"initial_delivery,omitempty"`
	AutomaticReminderDelivery     *DeliveryStatus `json:"automatic_reminder_delivery,omitempty"`
	LastManualReminderDelivery    *DeliveryStatus `json:"last_manual_reminder_delivery,omitempty"`
}

// Recipient is the Curator's current Recipient administration view.
type Recipient struct {
	PersonID   string      `json:"person_id"`
	PersonName string      `json:"person_name"`
	Email      string      `json:"email"`
	Access     Access      `json:"access"`
	Invitation *Invitation `json:"invitation,omitempty"`
}

// InvitationActionRequest protects a Curator action from targeting a replacement Invitation.
type InvitationActionRequest struct {
	InvitationID string `json:"invitation_id" validate:"required,uuid"`
}

// TokenRequest exchanges a browser-held Invitation token without putting it in the API URL.
type TokenRequest struct {
	Token string `json:"token" validate:"required,len=64,hexadecimal"`
}

// InspectResponse is safe to show to the bearer before acceptance.
type InspectResponse struct {
	RecipientName string    `json:"recipient_name"`
	CuratorName   string    `json:"curator_name"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// AcceptResponse confirms the explicit exchange and restricted Onboarding Session.
type AcceptResponse struct {
	Status  string `json:"status"`
	session setup.BrowserSession
}

// OnboardingRequest is both a resumable draft and the explicit completion payload.
type OnboardingRequest struct {
	PrivacyAcknowledged       bool   `json:"privacy_acknowledged"`
	EngagementAcknowledged    bool   `json:"engagement_acknowledged"`
	InterestListAcknowledged  bool   `json:"interest_list_acknowledged"`
	EmailPreviewsAcknowledged bool   `json:"email_previews_acknowledged"`
	PushGuidanceAcknowledged  bool   `json:"push_guidance_acknowledged"`
	EmailPreference           string `json:"email_preference" validate:"required,oneof=immediate weekly none"`
	SessionType               string `json:"session_type" validate:"omitempty,oneof=trusted public"`
}

// OnboardingResponse restores informed choices without exposing identity credentials.
type OnboardingResponse struct {
	Status                    string `json:"status"`
	RecipientName             string `json:"recipient_name"`
	PrivacyAcknowledged       bool   `json:"privacy_acknowledged"`
	EngagementAcknowledged    bool   `json:"engagement_acknowledged"`
	InterestListAcknowledged  bool   `json:"interest_list_acknowledged"`
	EmailPreviewsAcknowledged bool   `json:"email_previews_acknowledged"`
	PushGuidanceAcknowledged  bool   `json:"push_guidance_acknowledged"`
	EmailPreference           string `json:"email_preference"`
	SessionType               string `json:"session_type"`
	CSRFToken                 string `json:"csrf_token"`
}

// OnboardingCompleteResponse confirms completion and the rotated CSRF token.
type OnboardingCompleteResponse struct {
	Status    string `json:"status"`
	CSRFToken string `json:"csrf_token"`
	session   setup.BrowserSession
}

type Service struct {
	db        *bun.DB
	delivery  *emaildelivery.Service
	auth      *setup.Service
	publicURL string
	now       func() time.Time
	random    io.Reader
}

func New(db *bun.DB, delivery *emaildelivery.Service, publicURL string, auth *setup.Service) *Service {
	return &Service{
		db: db, delivery: delivery, auth: auth,
		publicURL: strings.TrimRight(publicURL, "/"), now: time.Now, random: rand.Reader,
	}
}

func normalizeEmail(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 320 {
		return "", "", ErrEmailInvalid
	}
	return parsed.Address, strings.ToLower(parsed.Address), nil
}

// Designate creates exactly one current pending generation and login email, with no delivery side effect.
func (s *Service) Designate(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, request DesignateRequest) (Recipient, error) {
	email, normalized, err := normalizeEmail(request.Email)
	if err != nil {
		return Recipient{}, err
	}
	accessID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return Recipient{}, errGenerateToken
	}
	emailID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return Recipient{}, errGenerateToken
	}
	now := s.now().UTC()
	var result Recipient
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActorAndRecipient(ctx, tx, actor.PersonID, personID); err != nil {
			return err
		}
		var archivedAt, mergedAt *time.Time
		err := tx.NewRaw(`SELECT archived_at, merged_at FROM people WHERE id = ?`, personID).Scan(ctx, &archivedAt, &mergedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPersonNotFound
		}
		if err != nil {
			return err
		}
		if archivedAt != nil || mergedAt != nil {
			return ErrPersonUnavailable
		}
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM recipient_access_generations WHERE person_id = ? AND is_current)`, personID).Scan(ctx, &exists); err != nil {
			return err
		}
		if exists {
			return ErrAlreadyRecipient
		}
		var generation int
		if err := tx.NewRaw(`SELECT COALESCE(max(generation), 0) + 1 FROM recipient_access_generations WHERE person_id = ?`, personID).Scan(ctx, &generation); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, created_at, updated_at) VALUES (?, ?, ?, 'pending', true, ?, ?)`, accessID, personID, generation, now, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, ?, ?, true, ?)`, emailID, accessID, email, normalized, now).Exec(ctx); err != nil {
			if strings.Contains(err.Error(), "recipient_emails_current_normalized_idx") {
				return ErrEmailInUse
			}
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO person_roles (person_id, role, created_at) VALUES (?, 'recipient', ?) ON CONFLICT DO NOTHING`, personID, now).Exec(ctx); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, personID, "pending_recipient_designated", map[string]any{"generation": generation}); err != nil {
			return err
		}
		result, err = getRecipient(ctx, tx, personID, now)
		return err
	})
	return result, err
}

// Get returns the current generation and newest Invitation for Curator inspection.
func (s *Service) Get(ctx context.Context, personID uuid.UUID) (Recipient, error) {
	return getRecipient(ctx, s.db, personID, s.now().UTC())
}

func getRecipient(ctx context.Context, db bun.IDB, personID uuid.UUID, now time.Time) (Recipient, error) {
	var result Recipient
	var accessID uuid.UUID
	err := db.NewRaw(`
		SELECT person.id, person.display_name, email.email, access.id, access.generation, access.state
		FROM people AS person
		JOIN recipient_access_generations AS access ON access.person_id = person.id AND access.is_current
		JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE person.id = ? AND person.merged_at IS NULL
	`, personID).Scan(ctx, &result.PersonID, &result.PersonName, &result.Email, &accessID, &result.Access.Generation, &result.Access.State)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipient{}, ErrRecipientNotFound
	}
	if err != nil {
		return Recipient{}, err
	}
	result.Access.ID = accessID.String()
	var row invitationRow
	err = db.NewRaw(`
		SELECT id, issued_at, expires_at, sent_at, accepted_at, revoked_at, superseded_at,
		       automatic_reminder_scheduled_at, automatic_reminded_at,
		       last_manual_reminder_requested_at, last_manual_reminded_at, manual_reminder_count
		FROM invitations WHERE recipient_access_generation_id = ?
		ORDER BY (accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL) DESC,
		         COALESCE(accepted_at, revoked_at, superseded_at, issued_at) DESC, issued_at DESC, id DESC
		LIMIT 1
	`, accessID).Scan(ctx, &row.ID, &row.IssuedAt, &row.ExpiresAt, &row.SentAt, &row.AcceptedAt, &row.RevokedAt, &row.SupersededAt,
		&row.AutomaticReminderScheduledAt, &row.AutomaticRemindedAt, &row.LastManualReminderRequestedAt, &row.LastManualRemindedAt, &row.ManualReminderCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Recipient{}, err
	}
	if err == nil {
		invitation := row.public(now)
		var deliveries []struct {
			Kind        string
			Status      string
			Attempts    int
			Failure     *string `bun:"last_safe_error"`
			NextRetryAt *time.Time
		}
		if err := db.NewRaw(`
			SELECT kind, status, attempts, last_safe_error, next_retry_at
			FROM email_deliveries WHERE invitation_id = ? ORDER BY id
		`, row.ID).Scan(ctx, &deliveries); err != nil {
			return Recipient{}, err
		}
		for index := range deliveries {
			delivery := DeliveryStatus{
				Status: deliveries[index].Status, Attempts: deliveries[index].Attempts,
				Failure: deliveries[index].Failure, NextRetryAt: deliveries[index].NextRetryAt,
			}
			switch deliveries[index].Kind {
			case emaildelivery.KindInvitationInitial:
				invitation.InitialDelivery = &delivery
			case emaildelivery.KindInvitationAutomaticReminder:
				invitation.AutomaticReminderDelivery = &delivery
			case emaildelivery.KindInvitationManualReminder:
				invitation.LastManualReminderDelivery = &delivery
			}
		}
		result.Invitation = &invitation
	}
	return result, nil
}

type invitationRow struct {
	ID                            uuid.UUID
	IssuedAt                      time.Time
	ExpiresAt                     time.Time
	SentAt                        *time.Time
	AcceptedAt                    *time.Time
	RevokedAt                     *time.Time
	SupersededAt                  *time.Time
	AutomaticReminderScheduledAt  time.Time
	AutomaticRemindedAt           *time.Time
	LastManualReminderRequestedAt *time.Time
	LastManualRemindedAt          *time.Time
	ManualReminderCount           int
}

func (row invitationRow) public(now time.Time) Invitation {
	status := "active"
	switch {
	case row.AcceptedAt != nil:
		status = "accepted"
	case row.RevokedAt != nil:
		status = "revoked"
	case row.SupersededAt != nil:
		status = "superseded"
	case !now.Before(row.ExpiresAt):
		status = "expired"
	}
	return Invitation{
		ID: row.ID.String(), Status: status, IssuedAt: row.IssuedAt, ExpiresAt: row.ExpiresAt,
		SentAt: row.SentAt, AcceptedAt: row.AcceptedAt, RevokedAt: row.RevokedAt, SupersededAt: row.SupersededAt,
		AutomaticReminderScheduledAt: row.AutomaticReminderScheduledAt, AutomaticRemindedAt: row.AutomaticRemindedAt,
		LastManualReminderRequestedAt: row.LastManualReminderRequestedAt, LastManualRemindedAt: row.LastManualRemindedAt,
		ManualReminderCount: row.ManualReminderCount,
	}
}

// Send creates and queues the first Invitation for a pending generation.
func (s *Service) Send(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID) (Recipient, error) {
	return s.issue(ctx, actor, personID, nil)
}

// Reissue supersedes the inspected offer and creates a fresh token and expiry.
func (s *Service) Reissue(ctx context.Context, actor setup.CuratorSession, personID, invitationID uuid.UUID) (Recipient, error) {
	return s.issue(ctx, actor, personID, &invitationID)
}

func (s *Service) issue(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, reissueID *uuid.UUID) (Recipient, error) {
	invitationID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return Recipient{}, errGenerateToken
	}
	token := make([]byte, 32)
	if _, err := io.ReadFull(s.random, token); err != nil {
		return Recipient{}, errGenerateToken
	}
	var result Recipient
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActorAndRecipient(ctx, tx, actor.PersonID, personID); err != nil {
			return err
		}
		current, err := lockPendingRecipient(ctx, tx, personID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		expiresAt := now.Add(invitationLifetime)
		reminderAt := now.Add(reminderDelay)
		var liveCount, historyCount int
		if err := tx.NewRaw(`SELECT count(*) FILTER (WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL), count(*) FROM invitations WHERE recipient_access_generation_id = ?`, current.accessID).Scan(ctx, &liveCount, &historyCount); err != nil {
			return err
		}
		if reissueID == nil && historyCount > 0 {
			return ErrInvitationExists
		}
		if reissueID == nil && liveCount > 0 {
			return ErrInvitationExists
		}
		if reissueID != nil && historyCount == 0 {
			return ErrInvitationNotFound
		}
		if reissueID != nil {
			var latestID uuid.UUID
			if err := tx.NewRaw(`
				SELECT id FROM invitations WHERE recipient_access_generation_id = ?
				ORDER BY (accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL) DESC,
				         COALESCE(accepted_at, revoked_at, superseded_at, issued_at) DESC, issued_at DESC, id DESC
				LIMIT 1
			`, current.accessID).Scan(ctx, &latestID); err != nil {
				return err
			}
			if latestID != *reissueID {
				return ErrInvitationStale
			}
			if _, err := tx.NewRaw(`UPDATE invitations SET superseded_at = ? WHERE recipient_access_generation_id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`, now, current.accessID).Exec(ctx); err != nil {
				return err
			}
		}
		hash := sha256.Sum256(token)
		if _, err := tx.NewRaw(`
			INSERT INTO invitations (id, recipient_access_generation_id, recipient_email_id, token_hash, issued_at, expires_at, automatic_reminder_scheduled_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, invitationID, current.accessID, current.emailID, hash[:], now, expiresAt, reminderAt).Exec(ctx); err != nil {
			if strings.Contains(err.Error(), "invitations_one_live_generation_idx") {
				return ErrInvitationExists
			}
			return err
		}
		curatorName, err := curatorName(ctx, tx, actor.PersonID)
		if err != nil {
			return err
		}
		rawToken := hex.EncodeToString(token)
		link := s.publicURL + "/invitation?token=" + rawToken
		invitationIDString := invitationID.String()
		expiry := expiresAt.Format(time.RFC1123)
		body := fmt.Sprintf("Hello %s,\n\n%s invited you to Memento, a private family photo and video archive. This personalized offer is only for your login email and can be used once. Open %s before %s, then explicitly accept and complete Onboarding before any Media becomes available. Do not forward this private link.", current.personName, curatorName, link, expiry)
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationInitial, Recipient: current.email, Subject: curatorName + " invited you to Memento",
			Body: body, DeliverBefore: &expiresAt, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		reminderBody := fmt.Sprintf("Hello %s,\n\nThis is the one automatic reminder that %s invited you to the private Memento family archive. Your single-use Invitation expires at %s. Open %s and complete Onboarding before Media becomes available.", current.personName, curatorName, expiry, link)
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationAutomaticReminder, Recipient: current.email, Subject: "Reminder: your Memento Invitation",
			Body: reminderBody, DeliverBefore: &expiresAt, AvailableAt: &reminderAt, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		action := "invitation_sent"
		if reissueID != nil {
			action = "invitation_reissued"
		}
		if err := appendAudit(ctx, tx, actor, personID, action, map[string]any{"invitation_id": invitationID.String(), "generation": current.generation, "expires_at": expiresAt}); err != nil {
			return err
		}
		result, err = getRecipient(ctx, tx, personID, now)
		return err
	})
	return result, err
}

type lockedRecipient struct {
	accessID   uuid.UUID
	emailID    uuid.UUID
	generation int
	personName string
	email      string
}

func lockPendingRecipient(ctx context.Context, tx bun.Tx, personID uuid.UUID) (lockedRecipient, error) {
	var result lockedRecipient
	err := tx.NewRaw(`
		SELECT access.id, email.id, access.generation, person.display_name, email.email
		FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE access.person_id = ? AND access.is_current
		FOR UPDATE OF access
	`, personID).Scan(ctx, &result.accessID, &result.emailID, &result.generation, &result.personName, &result.email)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedRecipient{}, ErrRecipientNotFound
	}
	if err != nil {
		return lockedRecipient{}, err
	}
	var state string
	if err := tx.NewRaw(`SELECT state FROM recipient_access_generations WHERE id = ?`, result.accessID).Scan(ctx, &state); err != nil {
		return lockedRecipient{}, err
	}
	if state != "pending" {
		return lockedRecipient{}, ErrInvitationState
	}
	return result, nil
}

// Revoke invalidates the current live Invitation without ending Recipient access.
func (s *Service) Revoke(ctx context.Context, actor setup.CuratorSession, personID, invitationID uuid.UUID) (Recipient, error) {
	return s.mutateLive(ctx, actor, personID, invitationID, "invitation_revoked", func(ctx context.Context, tx bun.Tx, _ lockedRecipient, invitationID uuid.UUID, now time.Time) error {
		result, err := tx.NewRaw(`UPDATE invitations SET revoked_at = ? WHERE id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL AND expires_at > ?`, now, invitationID, now).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrInvitationNotLive
		}
		return nil
	})
}

// Remind queues a manual reminder without extending or replacing the Invitation.
func (s *Service) Remind(ctx context.Context, actor setup.CuratorSession, personID, invitationID uuid.UUID) (Recipient, error) {
	return s.mutateLive(ctx, actor, personID, invitationID, "invitation_manual_reminder_requested", func(ctx context.Context, tx bun.Tx, current lockedRecipient, invitationID uuid.UUID, now time.Time) error {
		var expiresAt time.Time
		var sentAt *time.Time
		if err := tx.NewRaw(`SELECT expires_at, sent_at FROM invitations WHERE id = ?`, invitationID).Scan(ctx, &expiresAt, &sentAt); err != nil {
			return err
		}
		if sentAt == nil {
			return ErrInvitationNotSent
		}
		curator, err := curatorName(ctx, tx, actor.PersonID)
		if err != nil {
			return err
		}
		invitationIDString := invitationID.String()
		body := fmt.Sprintf("Hello %s,\n\n%s is reminding you about your Memento Invitation. Use the private link in your most recent Invitation email before %s. The offer remains single-use and Onboarding is required before Media becomes available.", current.personName, curator, expiresAt.Format(time.RFC1123))
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationManualReminder, Recipient: current.email, Subject: "Reminder about your Memento Invitation",
			Body: body, DeliverBefore: &expiresAt, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		_, err = tx.NewRaw(`UPDATE invitations SET last_manual_reminder_requested_at = ?, manual_reminder_count = manual_reminder_count + 1 WHERE id = ?`, now, invitationID).Exec(ctx)
		return err
	})
}

type liveMutation func(context.Context, bun.Tx, lockedRecipient, uuid.UUID, time.Time) error

func (s *Service) mutateLive(ctx context.Context, actor setup.CuratorSession, personID, expectedInvitationID uuid.UUID, action string, mutation liveMutation) (Recipient, error) {
	var response Recipient
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActorAndRecipient(ctx, tx, actor.PersonID, personID); err != nil {
			return err
		}
		current, err := lockPendingRecipient(ctx, tx, personID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		var invitationID uuid.UUID
		err = tx.NewRaw(`SELECT id FROM invitations WHERE recipient_access_generation_id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL AND expires_at > ? FOR UPDATE`, current.accessID, now).Scan(ctx, &invitationID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return err
		}
		if invitationID != expectedInvitationID {
			return ErrInvitationStale
		}
		if err := mutation(ctx, tx, current, invitationID, now); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, actor, personID, action, map[string]any{"invitation_id": invitationID.String(), "generation": current.generation}); err != nil {
			return err
		}
		response, err = getRecipient(ctx, tx, personID, now)
		return err
	})
	return response, err
}

// Inspect validates a live token without changing any row or authenticating the caller.
func (s *Service) Inspect(ctx context.Context, token string) (InspectResponse, error) {
	row, _, err := s.lookupToken(ctx, token, false)
	if err != nil || !s.now().UTC().Before(row.expiresAt) {
		return InspectResponse{}, ErrInvitationToken
	}
	return InspectResponse{RecipientName: row.recipientName, CuratorName: row.curatorName, ExpiresAt: row.expiresAt}, nil
}

// Accept consumes a live token and atomically establishes a restricted,
// resumable Onboarding Session. General Recipient authorization remains denied.
func (s *Service) Accept(ctx context.Context, token string) (AcceptResponse, error) {
	decoded, err := decodeToken(token)
	if err != nil || s.auth == nil {
		return AcceptResponse{}, ErrInvitationToken
	}
	var response AcceptResponse
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, expectedHash, err := lookupTokenIn(ctx, tx, decoded, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		actualHash := sha256.Sum256(decoded)
		if subtle.ConstantTimeCompare(expectedHash, actualHash[:]) != 1 || row.acceptedAt != nil || row.revokedAt != nil || row.supersededAt != nil || !now.Before(row.expiresAt) || row.accessState != "pending" {
			return ErrInvitationToken
		}
		result, err := tx.NewRaw(`UPDATE invitations SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`, now, row.invitationID).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrInvitationToken
		}
		result, err = tx.NewRaw(`UPDATE recipient_access_generations SET state = 'onboarding', updated_at = ? WHERE id = ? AND state = 'pending' AND is_current`, now, row.accessID).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrInvitationToken
		}
		if _, err := tx.NewRaw(`INSERT INTO onboarding_progress (recipient_access_generation_id, updated_at) VALUES (?, ?) ON CONFLICT DO NOTHING`, row.accessID, now).Exec(ctx); err != nil {
			return err
		}
		browserSession, sessionID, err := s.auth.NewBrowserSessionIn(ctx, tx, row.personID, row.accessID, "public", now)
		if err != nil {
			return err
		}
		// A consumed offer must not leave its scheduled Invitation reminder queued.
		if _, err := tx.NewRaw(`UPDATE email_deliveries SET status = 'cancelled', body = '' WHERE invitation_id = ? AND kind = ? AND status = 'queued'`, row.invitationID, emaildelivery.KindInvitationAutomaticReminder).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE outbox_events AS event SET delivered_at = ? FROM email_deliveries AS delivery WHERE event.aggregate_kind = 'email_delivery' AND event.aggregate_id = delivery.public_id AND delivery.invitation_id = ? AND delivery.kind = ? AND event.delivered_at IS NULL`, now, row.invitationID, emaildelivery.KindInvitationAutomaticReminder).Exec(ctx); err != nil {
			return err
		}
		request := setup.RequestMetadataFromContext(ctx)
		_, err = tx.NewRaw(`
			INSERT INTO security_audit_events (subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata)
			VALUES (?, 'invitation_accepted', 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)
		`, row.personID, request.ClientIP, request.UserAgent, sessionID, fmt.Sprintf(`{"invitation_id":%q}`, row.invitationID.String())).Exec(ctx)
		response = AcceptResponse{Status: "onboarding", session: browserSession}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrInvitationToken) || errors.Is(err, sql.ErrNoRows) {
			return AcceptResponse{}, ErrInvitationToken
		}
		return AcceptResponse{}, err
	}
	return response, nil
}

// Onboarding returns the persisted draft for a verified Onboarding Session.
func (s *Service) Onboarding(ctx context.Context, actor setup.SessionActor, csrfToken string) (OnboardingResponse, error) {
	var response OnboardingResponse
	err := s.db.NewRaw(`
		SELECT person.display_name, progress.privacy_acknowledged, progress.engagement_acknowledged,
		       progress.interest_list_acknowledged, progress.email_previews_acknowledged,
		       progress.push_guidance_acknowledged, progress.email_preference, progress.session_type
		FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id
		JOIN onboarding_progress AS progress ON progress.recipient_access_generation_id = access.id
		WHERE access.id = ? AND access.person_id = ? AND access.is_current AND access.state = 'onboarding'
	`, actor.AccessID, actor.PersonID).Scan(ctx, &response.RecipientName, &response.PrivacyAcknowledged,
		&response.EngagementAcknowledged, &response.InterestListAcknowledged,
		&response.EmailPreviewsAcknowledged, &response.PushGuidanceAcknowledged,
		&response.EmailPreference, &response.SessionType)
	if errors.Is(err, sql.ErrNoRows) {
		return OnboardingResponse{}, ErrOnboardingUnavailable
	}
	if err != nil {
		return OnboardingResponse{}, err
	}
	response.Status = "onboarding"
	response.CSRFToken = csrfToken
	return response, nil
}

// SaveOnboarding persists an incomplete draft without changing access.
func (s *Service) SaveOnboarding(ctx context.Context, actor setup.SessionActor, request OnboardingRequest, csrfToken string) (OnboardingResponse, error) {
	if !validOnboardingSelections(request) {
		return OnboardingResponse{}, ErrOnboardingChoices
	}
	now := s.now().UTC()
	result, err := s.db.NewRaw(`
		UPDATE onboarding_progress AS progress
		SET privacy_acknowledged = ?, engagement_acknowledged = ?, interest_list_acknowledged = ?,
		    email_previews_acknowledged = ?, push_guidance_acknowledged = ?, email_preference = ?,
		    session_type = ?, updated_at = ?
		FROM recipient_access_generations AS access
		WHERE progress.recipient_access_generation_id = ? AND access.id = progress.recipient_access_generation_id
		  AND access.person_id = ? AND access.is_current AND access.state = 'onboarding'
	`, request.PrivacyAcknowledged, request.EngagementAcknowledged, request.InterestListAcknowledged,
		request.EmailPreviewsAcknowledged, request.PushGuidanceAcknowledged, request.EmailPreference,
		request.SessionType, now, actor.AccessID, actor.PersonID).Exec(ctx)
	if err != nil {
		return OnboardingResponse{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return OnboardingResponse{}, err
	}
	if affected != 1 {
		return OnboardingResponse{}, ErrOnboardingUnavailable
	}
	return s.Onboarding(ctx, actor, csrfToken)
}

// CompleteOnboarding atomically records informed choices, completes the current
// generation, and rotates the Session. It never creates historical delivery.
func (s *Service) CompleteOnboarding(ctx context.Context, actor setup.SessionActor, request OnboardingRequest) (OnboardingCompleteResponse, error) {
	if !validOnboardingSelections(request) || !validSessionType(request.SessionType) ||
		!request.PrivacyAcknowledged || !request.EngagementAcknowledged || !request.InterestListAcknowledged ||
		!request.EmailPreviewsAcknowledged || !request.PushGuidanceAcknowledged {
		return OnboardingCompleteResponse{}, ErrOnboardingChoices
	}
	if s.auth == nil {
		return OnboardingCompleteResponse{}, ErrOnboardingUnavailable
	}
	var response OnboardingCompleteResponse
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var state string
		err := tx.NewRaw(`SELECT state FROM recipient_access_generations WHERE id = ? AND person_id = ? AND is_current FOR UPDATE`, actor.AccessID, actor.PersonID).Scan(ctx, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrOnboardingUnavailable
		}
		if err != nil {
			return err
		}
		if state != "onboarding" {
			return ErrOnboardingUnavailable
		}
		if err := tx.NewRaw(`SELECT id FROM sessions WHERE id = ? AND person_id = ? AND recipient_access_generation_id = ? AND revoked_at IS NULL FOR UPDATE`, actor.SessionID, actor.PersonID, actor.AccessID).Scan(ctx, &actor.SessionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrOnboardingUnavailable
			}
			return err
		}
		now := s.now().UTC()
		if _, err := tx.NewRaw(`INSERT INTO onboarding_choices (recipient_access_generation_id, privacy_acknowledged, engagement_acknowledged, interest_list_acknowledged, email_previews_acknowledged, push_guidance_acknowledged, informed_choices_version, email_preference, completed_at) VALUES (?, ?, ?, ?, ?, ?, 2, ?, ?)`, actor.AccessID, true, true, true, true, true, request.EmailPreference, now).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at) VALUES (?, ?, ?) ON CONFLICT (recipient_access_generation_id) DO UPDATE SET email_preference = EXCLUDED.email_preference, updated_at = EXCLUDED.updated_at`, actor.AccessID, request.EmailPreference, now).Exec(ctx); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE recipient_access_generations SET state = 'completed', onboarding_completed_at = ?, updated_at = ? WHERE id = ? AND state = 'onboarding' AND is_current`, now, now, actor.AccessID).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrOnboardingUnavailable
		}
		browserSession, err := s.auth.RotateBrowserSessionIn(ctx, tx, actor, request.SessionType, now)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM onboarding_progress WHERE recipient_access_generation_id = ?`, actor.AccessID).Exec(ctx); err != nil {
			return err
		}
		requestMetadata := setup.RequestMetadataFromContext(ctx)
		if _, err := tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id) VALUES (?, ?, 'onboarding_completed', 'success', NULLIF(?, '')::inet, ?, ?)`, actor.PersonID, actor.PersonID, requestMetadata.ClientIP, requestMetadata.UserAgent, actor.SessionID).Exec(ctx); err != nil {
			return err
		}
		response = OnboardingCompleteResponse{Status: "complete", CSRFToken: browserSession.CSRFToken, session: browserSession}
		return nil
	})
	return response, err
}

func validOnboardingSelections(request OnboardingRequest) bool {
	return (request.EmailPreference == "immediate" || request.EmailPreference == "weekly" || request.EmailPreference == "none") &&
		(request.SessionType == "" || validSessionType(request.SessionType))
}

func validSessionType(sessionType string) bool {
	return sessionType == "trusted" || sessionType == "public"
}

type tokenRow struct {
	invitationID  uuid.UUID
	accessID      uuid.UUID
	personID      uuid.UUID
	recipientName string
	curatorName   string
	expiresAt     time.Time
	acceptedAt    *time.Time
	revokedAt     *time.Time
	supersededAt  *time.Time
	accessState   string
}

func (s *Service) lookupToken(ctx context.Context, token string, lock bool) (tokenRow, []byte, error) {
	decoded, err := decodeToken(token)
	if err != nil {
		return tokenRow{}, nil, ErrInvitationToken
	}
	return lookupTokenIn(ctx, s.db, decoded, lock)
}

func lookupTokenIn(ctx context.Context, db bun.IDB, decoded []byte, lock bool) (tokenRow, []byte, error) {
	hash := sha256.Sum256(decoded)
	if lock {
		var accessID, personID uuid.UUID
		err := db.NewRaw(`
			SELECT invitation.recipient_access_generation_id, access.person_id
			FROM invitations AS invitation
			JOIN recipient_access_generations AS access ON access.id = invitation.recipient_access_generation_id
			WHERE invitation.token_hash = ?
		`, hash[:]).Scan(ctx, &accessID, &personID)
		if errors.Is(err, sql.ErrNoRows) {
			return tokenRow{}, nil, ErrInvitationToken
		}
		if err != nil {
			return tokenRow{}, nil, err
		}
		if err := db.NewRaw(`SELECT id FROM people WHERE id = ? FOR NO KEY UPDATE`, personID).Scan(ctx, &personID); err != nil {
			return tokenRow{}, nil, err
		}
		if err := db.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? AND person_id = ? FOR UPDATE`, accessID, personID).Scan(ctx, &accessID); err != nil {
			return tokenRow{}, nil, err
		}
	}
	query := `
		SELECT invitation.id, invitation.token_hash, invitation.expires_at, invitation.accepted_at,
		       invitation.revoked_at, invitation.superseded_at, access.id, access.state,
		       person.id, person.display_name,
		       (SELECT display_name FROM people JOIN person_roles ON person_roles.person_id = people.id WHERE person_roles.role = 'curator')
		FROM invitations AS invitation
		JOIN recipient_access_generations AS access ON access.id = invitation.recipient_access_generation_id AND access.is_current
		JOIN recipient_emails AS email ON email.id = invitation.recipient_email_id AND email.recipient_access_generation_id = access.id AND email.is_current
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		WHERE invitation.token_hash = ?`
	if lock {
		query += ` FOR UPDATE OF invitation`
	}
	var row tokenRow
	var expected []byte
	err := db.NewRaw(query, hash[:]).Scan(ctx, &row.invitationID, &expected, &row.expiresAt, &row.acceptedAt, &row.revokedAt, &row.supersededAt,
		&row.accessID, &row.accessState, &row.personID, &row.recipientName, &row.curatorName)
	if errors.Is(err, sql.ErrNoRows) {
		return tokenRow{}, nil, ErrInvitationToken
	}
	if err != nil {
		return tokenRow{}, nil, err
	}
	if subtle.ConstantTimeCompare(expected, hash[:]) != 1 || row.acceptedAt != nil || row.revokedAt != nil || row.supersededAt != nil || row.accessState != "pending" {
		return tokenRow{}, nil, ErrInvitationToken
	}
	return row, expected, nil
}

func decodeToken(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvitationToken
	}
	return decoded, nil
}

func lockActorAndRecipient(ctx context.Context, tx bun.Tx, actorID, recipientID uuid.UUID) error {
	_, err := tx.NewRaw(`SELECT id FROM people WHERE id IN (?, ?) ORDER BY id FOR NO KEY UPDATE`, actorID, recipientID).Exec(ctx)
	return err
}

func curatorName(ctx context.Context, db bun.IDB, actorID uuid.UUID) (string, error) {
	var name string
	err := db.NewRaw(`SELECT person.display_name FROM people AS person JOIN person_roles AS role ON role.person_id = person.id AND role.role = 'curator' WHERE person.id = ?`, actorID).Scan(ctx, &name)
	return name, err
}

func appendAudit(ctx context.Context, tx bun.Tx, actor setup.CuratorSession, subject uuid.UUID, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	request := setup.RequestMetadataFromContext(ctx)
	_, err = tx.NewRaw(`
		INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, client_ip, user_agent, session_id, metadata)
		VALUES (?, ?, ?, 'success', NULLIF(?, '')::inet, ?, ?, ?::jsonb)
	`, actor.PersonID, subject, action, request.ClientIP, request.UserAgent, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
