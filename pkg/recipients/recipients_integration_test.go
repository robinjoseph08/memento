//go:build integration

package recipients

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
	mementosmtp "github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type acceptingSender struct {
	mu       sync.Mutex
	messages []mementosmtp.Message
}

func (sender *acceptingSender) Send(_ context.Context, message mementosmtp.Message) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.messages = append(sender.messages, message)
	return nil
}

func (sender *acceptingSender) count() int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return len(sender.messages)
}

func (sender *acceptingSender) message(index int) mementosmtp.Message {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.messages[index]
}

type recipientFixture struct {
	db         *bun.DB
	service    *Service
	delivery   *emaildelivery.Service
	sender     *acceptingSender
	actor      setup.CuratorSession
	auth       *setup.Service
	credential string
	csrf       string
	personID   uuid.UUID
	now        time.Time
}

func newRecipientFixture(t *testing.T) recipientFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(ctx, db))
	curatorID := uuid.New()
	personID := uuid.New()
	_, err := db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Robin', 'robin'), (?, 'Alex', 'alex')`, curatorID, personID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient')`, curatorID, curatorID).Exec(ctx)
	require.NoError(t, err)
	accessID := uuid.New()
	_, err = db.NewRaw(`INSERT INTO recipient_access_generations (id, person_id, generation, state, is_current, onboarding_completed_at) VALUES (?, ?, 1, 'completed', true, now())`, accessID, curatorID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email) VALUES (?, ?, 'robin@example.com', 'robin@example.com')`, uuid.New(), accessID).Exec(ctx)
	require.NoError(t, err)
	var epoch []byte
	require.NoError(t, db.NewRaw(`UPDATE system_settings SET setup_complete = true WHERE id = 1 RETURNING security_epoch`).Scan(ctx, &epoch))
	sessionID := uuid.New()
	credentialRaw := bytes.Repeat([]byte{0x11}, 32)
	credentialHash := sha256.Sum256(credentialRaw)
	_, err = db.NewRaw(`INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at) VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 hour')`, sessionID, credentialHash[:], curatorID, accessID, epoch).Exec(ctx)
	require.NoError(t, err)

	smtpConfig := config.SMTPConfig{Enabled: true, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: 24 * time.Hour}
	sender := &acceptingSender{}
	secret := "integration-security-secret-at-least-32-bytes"
	delivery := emaildelivery.New(db, smtpConfig, sender, secret)
	service := New(db, delivery, "https://memento.example")
	auth := setup.New(db, delivery, config.SecurityConfig{Secret: secret})
	credential := hex.EncodeToString(credentialRaw)
	session, err := auth.Session(ctx, credential)
	require.NoError(t, err)
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	return recipientFixture{db: db, service: service, delivery: delivery, sender: sender, actor: setup.CuratorSession{PersonID: curatorID, SessionID: sessionID}, auth: auth, credential: credential, csrf: session.CSRFToken, personID: personID, now: now}
}

func designate(t *testing.T, fixture recipientFixture) Recipient {
	t.Helper()
	result, err := fixture.service.Designate(context.Background(), fixture.actor, fixture.personID, DesignateRequest{Email: "Alex@example.com"})
	require.NoError(t, err)
	return result
}

func deterministicIssue(t *testing.T, fixture recipientFixture, fill byte) (Recipient, string) {
	t.Helper()
	data := bytes.Repeat([]byte{fill}, 48)
	fixture.service.random = bytes.NewReader(data)
	result, err := fixture.service.Send(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	return result, hex.EncodeToString(data[16:48])
}

func recipientHTTP(t *testing.T, fixture recipientFixture) *echo.Echo {
	t.Helper()
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(fixture.service, fixture.auth))
	return e
}

func serveRecipient(fixture recipientFixture, e *echo.Echo, path, body, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestRecipientMutationsRequireSessionBoundCSRF(t *testing.T) {
	tests := []struct {
		name   string
		path   func(recipientFixture) string
		body   string
		setup  func(*testing.T, recipientFixture)
		status int
	}{
		{"designate", func(f recipientFixture) string { return "/api/recipients/" + f.personID.String() + "/designate" }, `{"email":"alex@example.com"}`, func(*testing.T, recipientFixture) {}, http.StatusCreated},
		{"send", func(f recipientFixture) string { return "/api/recipients/" + f.personID.String() + "/invitation/send" }, `{}`, func(t *testing.T, f recipientFixture) { designate(t, f) }, http.StatusOK},
		{"revoke", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/revoke"
		}, `{}`, func(t *testing.T, f recipientFixture) { designate(t, f); deterministicIssue(t, f, 0x31) }, http.StatusOK},
		{"reissue", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/reissue"
		}, `{}`, func(t *testing.T, f recipientFixture) {
			designate(t, f)
			deterministicIssue(t, f, 0x32)
			f.service.random = bytes.NewReader(bytes.Repeat([]byte{0x33}, 48))
		}, http.StatusOK},
		{"remind", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/remind"
		}, `{}`, func(t *testing.T, f recipientFixture) {
			designate(t, f)
			issued, _ := deterministicIssue(t, f, 0x34)
			_, err := f.db.NewRaw(`UPDATE invitations SET sent_at = ? WHERE id = ?`, f.now, issued.Invitation.ID).Exec(context.Background())
			require.NoError(t, err)
		}, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecipientFixture(t)
			test.setup(t, fixture)
			e := recipientHTTP(t, fixture)
			withoutCSRF := serveRecipient(fixture, e, test.path(fixture), test.body, "")
			assert.Equal(t, http.StatusForbidden, withoutCSRF.Code)
			withCSRF := serveRecipient(fixture, e, test.path(fixture), test.body, fixture.csrf)
			assert.Equal(t, test.status, withCSRF.Code, withCSRF.Body.String())
		})
	}
}

func TestDesignateCreatesPendingGenerationWithoutDeliveryOrAccessSession(t *testing.T) {
	fixture := newRecipientFixture(t)
	result := designate(t, fixture)
	assert.Equal(t, "pending", result.Access.State)
	assert.Equal(t, 1, result.Access.Generation)
	assert.Nil(t, result.Invitation)
	assert.Equal(t, "Alex@example.com", result.Email)

	var invitations, deliveries, outbox, sessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations`).Scan(context.Background(), &invitations))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE kind LIKE 'invitation_%'`).Scan(context.Background(), &deliveries))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE kind = ?`, emaildelivery.JobKind).Scan(context.Background(), &outbox))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &sessions))
	assert.Zero(t, invitations)
	assert.Zero(t, deliveries)
	assert.Zero(t, outbox)
	assert.Zero(t, sessions)

	_, err := fixture.service.Designate(context.Background(), fixture.actor, fixture.personID, DesignateRequest{Email: "other@example.com"})
	assert.ErrorIs(t, err, ErrAlreadyRecipient)
}

func TestSendPersistsOnlyTokenHashAndDurableInitialAndSevenDayReminder(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	result, token := deterministicIssue(t, fixture, 0x42)
	require.NotNil(t, result.Invitation)
	assert.Equal(t, "active", result.Invitation.Status)
	assert.Equal(t, fixture.now.Add(14*24*time.Hour), result.Invitation.ExpiresAt)
	assert.Equal(t, fixture.now.Add(7*24*time.Hour), result.Invitation.AutomaticReminderScheduledAt)

	var storedHash []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT token_hash FROM invitations WHERE id = ?`, result.Invitation.ID).Scan(context.Background(), &storedHash))
	raw, err := hex.DecodeString(token)
	require.NoError(t, err)
	expected := sha256.Sum256(raw)
	assert.Equal(t, expected[:], storedHash)

	type deliveryRow struct {
		Kind        string    `bun:"kind"`
		Recipient   string    `bun:"recipient"`
		AvailableAt time.Time `bun:"available_at"`
		Body        string    `bun:"body"`
	}
	rows := make([]deliveryRow, 0)
	require.NoError(t, fixture.db.NewRaw(`SELECT kind, recipient, available_at, body FROM email_deliveries WHERE invitation_id = ? ORDER BY available_at`, result.Invitation.ID).Scan(context.Background(), &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, emaildelivery.KindInvitationInitial, rows[0].Kind)
	assert.WithinDuration(t, time.Now().UTC(), rows[0].AvailableAt, 5*time.Second)
	assert.Equal(t, emaildelivery.KindInvitationAutomaticReminder, rows[1].Kind)
	assert.Equal(t, fixture.now.Add(7*24*time.Hour), rows[1].AvailableAt)
	for _, row := range rows {
		assert.Equal(t, "Alex@example.com", row.Recipient)
		assert.NotContains(t, row.Body, token)
		assert.True(t, len(row.Body) > 3 && row.Body[:3] == "v1:")
	}
	var outboxCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events WHERE aggregate_kind = 'email_delivery' AND delivered_at IS NULL`).Scan(context.Background(), &outboxCount))
	assert.Equal(t, 2, outboxCount)

	_, err = fixture.db.NewRaw(`UPDATE outbox_events AS event SET available_at = now() FROM email_deliveries AS delivery WHERE event.aggregate_id = delivery.public_id AND delivery.kind = ?`, emaildelivery.KindInvitationInitial).Exec(context.Background())
	require.NoError(t, err)
	dispatcher := outbox.New(fixture.db)
	dispatched, err := dispatcher.Dispatch(context.Background(), "invitation-test", time.Minute)
	require.NoError(t, err)
	assert.True(t, dispatched)
	dispatched, err = dispatcher.Dispatch(context.Background(), "invitation-test", time.Minute)
	require.NoError(t, err)
	assert.False(t, dispatched, "the seven-day reminder must not become claimable early")
	_, err = fixture.db.NewRaw(`UPDATE outbox_events AS event SET available_at = now() FROM email_deliveries AS delivery WHERE event.aggregate_id = delivery.public_id AND delivery.kind = ?`, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
	require.NoError(t, err)
	dispatched, err = dispatcher.Dispatch(context.Background(), "invitation-test", time.Minute)
	require.NoError(t, err)
	assert.True(t, dispatched)
	var jobs int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM jobs WHERE kind = ?`, emaildelivery.JobKind).Scan(context.Background(), &jobs))
	assert.Equal(t, 2, jobs)

	var job worker.Job
	require.NoError(t, fixture.db.NewRaw(`
		UPDATE jobs AS job SET status = 'running', lease_owner = 'invitation-link-test', lease_expires_at = now() + interval '1 minute'
		FROM email_deliveries AS delivery
		WHERE (job.payload ->> 'delivery_id')::bigint = delivery.id AND delivery.invitation_id = ? AND delivery.kind = ?
		RETURNING job.id, job.kind, job.payload
	`, result.Invitation.ID, emaildelivery.KindInvitationInitial).Scan(context.Background(), &job.ID, &job.Kind, &job.Payload))
	job.LeaseOwner = "invitation-link-test"
	require.NoError(t, fixture.delivery.Handle(context.Background(), job))
	require.Equal(t, 1, fixture.sender.count())
	assert.Equal(t, "Alex@example.com", fixture.sender.message(0).To)
	assert.Contains(t, fixture.sender.message(0).Body, "https://memento.example/invitation?token="+token)
	assert.Contains(t, fixture.sender.message(0).Body, result.Invitation.ExpiresAt.Format(time.RFC1123))
}

func TestInspectIsReadOnlyAndAcceptIsSingleUseWithoutCreatingSession(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x37)
	before := *invitation.Invitation

	inspected, err := fixture.service.Inspect(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "Alex", inspected.RecipientName)
	assert.Equal(t, "Robin", inspected.CuratorName)
	afterInspect, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, before, *afterInspect.Invitation)

	accepted, err := fixture.service.Accept(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "onboarding", accepted.Status)
	_, err = fixture.service.Inspect(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Accept(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)

	current, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, "onboarding", current.Access.State)
	assert.Equal(t, "accepted", current.Invitation.Status)
	var sessions int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM sessions WHERE person_id = ?`, fixture.personID).Scan(context.Background(), &sessions))
	assert.Zero(t, sessions)
}

func TestRevocationExpiryAndReissueInvalidateOldTokens(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	_, oldToken := deterministicIssue(t, fixture, 0x21)

	revoked, err := fixture.service.Revoke(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", revoked.Invitation.Status)
	_, err = fixture.service.Inspect(context.Background(), oldToken)
	assert.ErrorIs(t, err, ErrInvitationToken)

	data := bytes.Repeat([]byte{0x58}, 48)
	fixture.service.random = bytes.NewReader(data)
	reissued, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	newToken := hex.EncodeToString(data[16:48])
	assert.Equal(t, "active", reissued.Invitation.Status)
	_, err = fixture.service.Inspect(context.Background(), oldToken)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Inspect(context.Background(), newToken)
	require.NoError(t, err)

	fixture.service.now = func() time.Time { return fixture.now.Add(15 * 24 * time.Hour) }
	_, err = fixture.service.Inspect(context.Background(), newToken)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Accept(context.Background(), newToken)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Remind(context.Background(), fixture.actor, fixture.personID)
	assert.ErrorIs(t, err, ErrInvitationNotFound)
}

func TestActiveReissueSupersedesOldTokenAndLeavesOneLiveInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	oldInvitation, oldToken := deterministicIssue(t, fixture, 0xf1)
	data := bytes.Repeat([]byte{0x01}, 48)
	fixture.service.random = bytes.NewReader(data)
	reissued, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	newToken := hex.EncodeToString(data[16:48])
	assert.NotEqual(t, oldInvitation.Invitation.ID, reissued.Invitation.ID)
	_, err = fixture.service.Inspect(context.Background(), oldToken)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Accept(context.Background(), oldToken)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Inspect(context.Background(), newToken)
	require.NoError(t, err)
	var superseded bool
	require.NoError(t, fixture.db.NewRaw(`SELECT superseded_at IS NOT NULL FROM invitations WHERE id = ?`, oldInvitation.Invitation.ID).Scan(context.Background(), &superseded))
	assert.True(t, superseded)
	var live int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`).Scan(context.Background(), &live))
	assert.Equal(t, 1, live)
}

func TestInvitationTokenIsBoundToItsCurrentLoginEmail(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x43)
	_, err := fixture.db.NewRaw(`UPDATE recipient_emails SET is_current = false, ended_at = ? WHERE recipient_access_generation_id = ? AND is_current`, fixture.now, invitation.Access.ID).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email, is_current, created_at) VALUES (?, ?, 'new@example.com', 'new@example.com', true, ?)`, uuid.New(), invitation.Access.ID, fixture.now).Exec(context.Background())
	require.NoError(t, err)
	_, err = fixture.service.Inspect(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Accept(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)
}

func dispatchDelivery(t *testing.T, fixture recipientFixture, invitationID, kind, owner string) error {
	t.Helper()
	ctx := context.Background()
	_, err := fixture.db.NewRaw(`
		UPDATE outbox_events SET available_at = now() + interval '1 day'
		WHERE aggregate_kind = 'email_delivery' AND delivered_at IS NULL
	`).Exec(ctx)
	require.NoError(t, err)
	_, err = fixture.db.NewRaw(`
		UPDATE outbox_events AS event SET available_at = now()
		FROM email_deliveries AS delivery
		WHERE event.aggregate_id = delivery.public_id AND delivery.invitation_id = ? AND delivery.kind = ? AND event.delivered_at IS NULL
	`, invitationID, kind).Exec(ctx)
	require.NoError(t, err)
	dispatched, err := outbox.New(fixture.db).Dispatch(ctx, owner, time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)
	var job worker.Job
	require.NoError(t, fixture.db.NewRaw(`
		UPDATE jobs AS job SET status = 'running', lease_owner = ?, lease_expires_at = now() + interval '1 minute'
		FROM email_deliveries AS delivery
		WHERE (job.payload ->> 'delivery_id')::bigint = delivery.id AND delivery.invitation_id = ? AND delivery.kind = ?
		RETURNING job.id, job.kind, job.payload
	`, owner, invitationID, kind).Scan(ctx, &job.ID, &job.Kind, &job.Payload))
	job.LeaseOwner = owner
	return fixture.delivery.Handle(ctx, job)
}

func TestAllInvitationDeliveryKindsCompleteAndRecordResults(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x51)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "initial-success"))
	_, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationManualReminder, "manual-success"))
	_, err = fixture.db.NewRaw(`UPDATE email_deliveries SET available_at = now() WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder, "automatic-success"))
	assert.Equal(t, 3, fixture.sender.count())
	for index := range 3 {
		assert.Equal(t, "Alex@example.com", fixture.sender.message(index).To)
		assert.Contains(t, fixture.sender.message(index).Body, invitation.Invitation.ExpiresAt.Format(time.RFC1123))
	}
	var sentAt, automaticAt, manualAt *time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT sent_at, automatic_reminded_at, last_manual_reminded_at FROM invitations WHERE id = ?`, invitation.Invitation.ID).Scan(context.Background(), &sentAt, &automaticAt, &manualAt))
	assert.NotNil(t, sentAt)
	assert.NotNil(t, automaticAt)
	assert.NotNil(t, manualAt)
	var uncleared int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE invitation_id = ? AND body <> ''`, invitation.Invitation.ID).Scan(context.Background(), &uncleared))
	assert.Zero(t, uncleared)
}

func TestArchivingPendingRecipientRevokesAccessInvitationAndDelivery(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x54)
	_, err := people.New(fixture.db).Archive(context.Background(), fixture.actor, fixture.personID, 1)
	require.NoError(t, err)
	_, err = fixture.service.Inspect(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)
	_, err = fixture.service.Accept(context.Background(), token)
	assert.ErrorIs(t, err, ErrInvitationToken)
	var state string
	var current bool
	require.NoError(t, fixture.db.NewRaw(`SELECT state, is_current FROM recipient_access_generations WHERE id = ?`, invitation.Access.ID).Scan(context.Background(), &state, &current))
	assert.Equal(t, "revoked", state)
	assert.False(t, current)
	var invitationRevoked, emailCurrent bool
	require.NoError(t, fixture.db.NewRaw(`SELECT revoked_at IS NOT NULL FROM invitations WHERE id = ?`, invitation.Invitation.ID).Scan(context.Background(), &invitationRevoked))
	require.NoError(t, fixture.db.NewRaw(`SELECT is_current FROM recipient_emails WHERE recipient_access_generation_id = ?`, invitation.Access.ID).Scan(context.Background(), &emailCurrent))
	assert.True(t, invitationRevoked)
	assert.False(t, emailCurrent)
	_, err = fixture.db.NewRaw(`UPDATE email_deliveries SET available_at = now() WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
	require.NoError(t, err)
	err = dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder, "archived-recipient")
	require.Error(t, err)
	assert.Zero(t, fixture.sender.count())
}

func TestAcceptedSupersededAndExpiredInvitationsCancelDelivery(t *testing.T) {
	for _, terminal := range []string{"accepted", "superseded", "expired"} {
		t.Run(terminal, func(t *testing.T) {
			fixture := newRecipientFixture(t)
			designate(t, fixture)
			invitation, token := deterministicIssue(t, fixture, 0x52)
			switch terminal {
			case "accepted":
				_, err := fixture.service.Accept(context.Background(), token)
				require.NoError(t, err)
			case "superseded":
				fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x53}, 48))
				_, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID)
				require.NoError(t, err)
			case "expired":
				_, err := fixture.db.NewRaw(`UPDATE invitations SET issued_at = now() - interval '15 days', expires_at = now() - interval '1 day', automatic_reminder_scheduled_at = now() - interval '8 days' WHERE id = ?`, invitation.Invitation.ID).Exec(context.Background())
				require.NoError(t, err)
			}
			_, err := fixture.db.NewRaw(`UPDATE email_deliveries SET available_at = now() WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
			require.NoError(t, err)
			err = dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder, "stale-"+terminal)
			require.Error(t, err)
			assert.Zero(t, fixture.sender.count())
			var status, diagnostic string
			require.NoError(t, fixture.db.NewRaw(`SELECT status, last_safe_error FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Scan(context.Background(), &status, &diagnostic))
			assert.Equal(t, "failed", status)
			assert.Equal(t, "delivery_expired", diagnostic)
		})
	}
}

func TestRevokedInvitationCancelsItsScheduledAutomaticReminderAtDelivery(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x72)
	_, err := fixture.service.Revoke(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)

	_, err = fixture.db.NewRaw(`UPDATE outbox_events AS event SET delivered_at = CASE WHEN delivery.kind = ? THEN now() ELSE event.delivered_at END, available_at = CASE WHEN delivery.kind = ? THEN now() ELSE event.available_at END FROM email_deliveries AS delivery WHERE event.aggregate_id = delivery.public_id AND delivery.invitation_id = ?`, emaildelivery.KindInvitationInitial, emaildelivery.KindInvitationAutomaticReminder, invitation.Invitation.ID).Exec(context.Background())
	require.NoError(t, err)
	dispatched, err := outbox.New(fixture.db).Dispatch(context.Background(), "revoked-reminder", time.Minute)
	require.NoError(t, err)
	assert.True(t, dispatched)
	var job worker.Job
	require.NoError(t, fixture.db.NewRaw(`
		UPDATE jobs AS job SET status = 'running', lease_owner = 'revoked-reminder', lease_expires_at = now() + interval '1 minute'
		FROM email_deliveries AS delivery
		WHERE (job.payload ->> 'delivery_id')::bigint = delivery.id AND delivery.invitation_id = ? AND delivery.kind = ?
		RETURNING job.id, job.kind, job.payload
	`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Scan(context.Background(), &job.ID, &job.Kind, &job.Payload))
	job.LeaseOwner = "revoked-reminder"
	err = fixture.delivery.Handle(context.Background(), job)
	require.Error(t, err)
	assert.Equal(t, 0, fixture.sender.count())
	var status, diagnostic string
	require.NoError(t, fixture.db.NewRaw(`SELECT status, last_safe_error FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Scan(context.Background(), &status, &diagnostic))
	assert.Equal(t, "failed", status)
	assert.Equal(t, "delivery_expired", diagnostic)
}

func TestManualReminderIsDurableAndDoesNotExtendInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	sent, _ := deterministicIssue(t, fixture, 0x63)
	originalExpiry := sent.Invitation.ExpiresAt

	_, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID)
	require.ErrorIs(t, err, ErrInvitationNotSent)
	_, err = fixture.db.NewRaw(`UPDATE invitations SET sent_at = ? WHERE id = ?`, fixture.now, sent.Invitation.ID).Exec(context.Background())
	require.NoError(t, err)
	reminded, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, originalExpiry, reminded.Invitation.ExpiresAt)
	assert.Equal(t, 1, reminded.Invitation.ManualReminderCount)
	assert.Equal(t, fixture.now, *reminded.Invitation.LastManualReminderRequestedAt)
	var deliveryCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, reminded.Invitation.ID, emaildelivery.KindInvitationManualReminder).Scan(context.Background(), &deliveryCount))
	assert.Equal(t, 1, deliveryCount)
}

func TestConcurrentSendLeavesOneLiveInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	fixture.service.random = randReader{}

	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.service.Send(context.Background(), fixture.actor, fixture.personID)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInvitationExists) {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	var live int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`).Scan(context.Background(), &live))
	assert.Equal(t, 1, live)
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return rand.Read(p) }
