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
	ErrPersonNotFound     = errors.New("person not found")
	ErrPersonUnavailable  = errors.New("person cannot become a Pending Recipient")
	ErrAlreadyRecipient   = errors.New("person already has current Recipient access")
	ErrEmailInvalid       = errors.New("login email is invalid")
	ErrEmailInUse         = errors.New("login email is already in use")
	ErrRecipientNotFound  = errors.New("pending Recipient not found")
	ErrInvitationExists   = errors.New("a live Invitation already exists")
	ErrInvitationNotFound = errors.New("Invitation not found")
	ErrInvitationNotLive  = errors.New("Invitation is not live")
	ErrInvitationToken    = errors.New("Invitation is invalid")
	ErrInvitationState    = errors.New("Recipient state does not permit this Invitation action")
	errGenerateToken      = errors.New("generate Invitation token")
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

// Invitation is the Curator-safe Invitation status. It never contains a token.
type Invitation struct {
	ID                            string     `json:"id"`
	Status                        string     `json:"status"`
	IssuedAt                      time.Time  `json:"issued_at"`
	ExpiresAt                     time.Time  `json:"expires_at"`
	SentAt                        *time.Time `json:"sent_at,omitempty"`
	AcceptedAt                    *time.Time `json:"accepted_at,omitempty"`
	RevokedAt                     *time.Time `json:"revoked_at,omitempty"`
	SupersededAt                  *time.Time `json:"superseded_at,omitempty"`
	AutomaticReminderScheduledAt  time.Time  `json:"automatic_reminder_scheduled_at"`
	AutomaticRemindedAt           *time.Time `json:"automatic_reminded_at,omitempty"`
	LastManualReminderRequestedAt *time.Time `json:"last_manual_reminder_requested_at,omitempty"`
	LastManualRemindedAt          *time.Time `json:"last_manual_reminded_at,omitempty"`
	ManualReminderCount           int        `json:"manual_reminder_count"`
}

// Recipient is the Curator's current Recipient administration view.
type Recipient struct {
	PersonID   string      `json:"person_id"`
	PersonName string      `json:"person_name"`
	Email      string      `json:"email"`
	Access     Access      `json:"access"`
	Invitation *Invitation `json:"invitation,omitempty"`
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

// AcceptResponse confirms the explicit exchange without creating a Session.
type AcceptResponse struct {
	Status string `json:"status"`
}

type Service struct {
	db       *bun.DB
	delivery *emaildelivery.Service
	now      func() time.Time
	random   io.Reader
}

func New(db *bun.DB, delivery *emaildelivery.Service) *Service {
	return &Service{db: db, delivery: delivery, now: time.Now, random: rand.Reader}
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
		var archivedAt, mergedAt *time.Time
		err := tx.NewRaw(`SELECT archived_at, merged_at FROM people WHERE id = ? FOR UPDATE`, personID).Scan(ctx, &archivedAt, &mergedAt)
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
		FROM invitations WHERE recipient_access_generation_id = ? ORDER BY issued_at DESC, id DESC LIMIT 1
	`, accessID).Scan(ctx, &row.ID, &row.IssuedAt, &row.ExpiresAt, &row.SentAt, &row.AcceptedAt, &row.RevokedAt, &row.SupersededAt,
		&row.AutomaticReminderScheduledAt, &row.AutomaticRemindedAt, &row.LastManualReminderRequestedAt, &row.LastManualRemindedAt, &row.ManualReminderCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Recipient{}, err
	}
	if err == nil {
		invitation := row.public(now)
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
	return s.issue(ctx, actor, personID, false)
}

// Reissue supersedes any unaccepted offer and creates a fresh token and expiry.
func (s *Service) Reissue(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID) (Recipient, error) {
	return s.issue(ctx, actor, personID, true)
}

func (s *Service) issue(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, reissue bool) (Recipient, error) {
	invitationID, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return Recipient{}, errGenerateToken
	}
	token := make([]byte, 32)
	if _, err := io.ReadFull(s.random, token); err != nil {
		return Recipient{}, errGenerateToken
	}
	now := s.now().UTC()
	expiresAt := now.Add(invitationLifetime)
	reminderAt := now.Add(reminderDelay)
	var result Recipient
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current, err := lockPendingRecipient(ctx, tx, personID)
		if err != nil {
			return err
		}
		var liveCount, historyCount int
		if err := tx.NewRaw(`SELECT count(*) FILTER (WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL), count(*) FROM invitations WHERE recipient_access_generation_id = ?`, current.accessID).Scan(ctx, &liveCount, &historyCount); err != nil {
			return err
		}
		if !reissue && historyCount > 0 {
			return ErrInvitationExists
		}
		if !reissue && liveCount > 0 {
			return ErrInvitationExists
		}
		if reissue && historyCount == 0 {
			return ErrInvitationNotFound
		}
		if reissue {
			if _, err := tx.NewRaw(`UPDATE invitations SET superseded_at = ? WHERE recipient_access_generation_id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`, now, current.accessID).Exec(ctx); err != nil {
				return err
			}
		}
		hash := sha256.Sum256(token)
		if _, err := tx.NewRaw(`
			INSERT INTO invitations (id, recipient_access_generation_id, token_hash, issued_at, expires_at, automatic_reminder_scheduled_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, invitationID, current.accessID, hash[:], now, expiresAt, reminderAt).Exec(ctx); err != nil {
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
		link := "/invitation?token=" + rawToken
		invitationIDString := invitationID.String()
		body := fmt.Sprintf("Hello %s,\n\n%s invited you to Memento, a private family photo and video archive. This personalized offer is only for your login email and can be used once. Open %s within 14 days, then explicitly accept and complete Onboarding before any Media becomes available. Do not forward this private link.", current.personName, curatorName, link)
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationInitial, Recipient: current.email, Subject: curatorName + " invited you to Memento",
			Body: body, DeliverBefore: &expiresAt, AvailableAt: &now, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		reminderBody := fmt.Sprintf("Hello %s,\n\nThis is the one automatic reminder that %s invited you to the private Memento family archive. Your single-use Invitation expires in seven days. Open %s and complete Onboarding before Media becomes available.", current.personName, curatorName, link)
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationAutomaticReminder, Recipient: current.email, Subject: "Your Memento Invitation expires in seven days",
			Body: reminderBody, DeliverBefore: &expiresAt, AvailableAt: &reminderAt, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		action := "invitation_sent"
		if reissue {
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
	generation int
	personName string
	email      string
}

func lockPendingRecipient(ctx context.Context, tx bun.Tx, personID uuid.UUID) (lockedRecipient, error) {
	var result lockedRecipient
	err := tx.NewRaw(`
		SELECT access.id, access.generation, person.display_name, email.email
		FROM recipient_access_generations AS access
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
		WHERE access.person_id = ? AND access.is_current
		FOR UPDATE OF access
	`, personID).Scan(ctx, &result.accessID, &result.generation, &result.personName, &result.email)
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
func (s *Service) Revoke(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID) (Recipient, error) {
	return s.mutateLive(ctx, actor, personID, "invitation_revoked", func(ctx context.Context, tx bun.Tx, _ lockedRecipient, invitationID uuid.UUID, now time.Time) error {
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
func (s *Service) Remind(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID) (Recipient, error) {
	return s.mutateLive(ctx, actor, personID, "invitation_manual_reminder_requested", func(ctx context.Context, tx bun.Tx, current lockedRecipient, invitationID uuid.UUID, now time.Time) error {
		var expiresAt time.Time
		if err := tx.NewRaw(`SELECT expires_at FROM invitations WHERE id = ?`, invitationID).Scan(ctx, &expiresAt); err != nil {
			return err
		}
		curator, err := curatorName(ctx, tx, actor.PersonID)
		if err != nil {
			return err
		}
		invitationIDString := invitationID.String()
		body := fmt.Sprintf("Hello %s,\n\n%s is reminding you about your Memento Invitation. Use the private link in your most recent Invitation email before it expires. The offer remains single-use and Onboarding is required before Media becomes available.", current.personName, curator)
		if _, _, err := s.delivery.QueueRequired(ctx, tx, emaildelivery.RequiredMessage{
			Kind: emaildelivery.KindInvitationManualReminder, Recipient: current.email, Subject: "Reminder about your Memento Invitation",
			Body: body, DeliverBefore: &expiresAt, AvailableAt: &now, InvitationID: &invitationIDString,
		}); err != nil {
			return err
		}
		_, err = tx.NewRaw(`UPDATE invitations SET last_manual_reminder_requested_at = ?, manual_reminder_count = manual_reminder_count + 1 WHERE id = ?`, now, invitationID).Exec(ctx)
		return err
	})
}

type liveMutation func(context.Context, bun.Tx, lockedRecipient, uuid.UUID, time.Time) error

func (s *Service) mutateLive(ctx context.Context, actor setup.CuratorSession, personID uuid.UUID, action string, mutation liveMutation) (Recipient, error) {
	now := s.now().UTC()
	var response Recipient
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current, err := lockPendingRecipient(ctx, tx, personID)
		if err != nil {
			return err
		}
		var invitationID uuid.UUID
		err = tx.NewRaw(`SELECT id FROM invitations WHERE recipient_access_generation_id = ? AND accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL AND expires_at > ? FOR UPDATE`, current.accessID, now).Scan(ctx, &invitationID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return err
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

// Accept consumes a live token and starts resumable Onboarding. It does not create a Session.
func (s *Service) Accept(ctx context.Context, token string) (AcceptResponse, error) {
	decoded, err := decodeToken(token)
	if err != nil {
		return AcceptResponse{}, ErrInvitationToken
	}
	now := s.now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, expectedHash, err := lookupTokenIn(ctx, tx, decoded, true)
		if err != nil {
			return err
		}
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
		_, err = tx.NewRaw(`INSERT INTO security_audit_events (subject_person_id, action, outcome, metadata) VALUES (?, 'invitation_accepted', 'success', ?::jsonb)`, row.personID, fmt.Sprintf(`{"invitation_id":%q}`, row.invitationID.String())).Exec(ctx)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrInvitationToken) || errors.Is(err, sql.ErrNoRows) {
			return AcceptResponse{}, ErrInvitationToken
		}
		return AcceptResponse{}, err
	}
	return AcceptResponse{Status: "onboarding"}, nil
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
	query := `
		SELECT invitation.id, invitation.token_hash, invitation.expires_at, invitation.accepted_at,
		       invitation.revoked_at, invitation.superseded_at, access.id, access.state,
		       person.id, person.display_name,
		       (SELECT display_name FROM people JOIN person_roles ON person_roles.person_id = people.id WHERE person_roles.role = 'curator')
		FROM invitations AS invitation
		JOIN recipient_access_generations AS access ON access.id = invitation.recipient_access_generation_id AND access.is_current
		JOIN people AS person ON person.id = access.person_id
		WHERE invitation.token_hash = ?`
	if lock {
		query += ` FOR UPDATE OF invitation, access`
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
	_, err = tx.NewRaw(`INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id, metadata) VALUES (?, ?, ?, 'success', ?, ?::jsonb)`, actor.PersonID, subject, action, actor.SessionID, string(encoded)).Exec(ctx)
	return err
}
