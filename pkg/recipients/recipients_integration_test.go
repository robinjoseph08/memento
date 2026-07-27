//go:build integration

package recipients

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	"github.com/robinjoseph08/memento/pkg/visibility"
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

type rejectingSender struct{}

func (rejectingSender) Send(context.Context, mementosmtp.Message) error {
	return &mementosmtp.DeliveryError{Diagnostic: "recipient_rejected", Temporary: false}
}

type blockingSender struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (sender *blockingSender) Send(ctx context.Context, _ mementosmtp.Message) error {
	sender.once.Do(func() { close(sender.started) })
	select {
	case <-sender.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	auth := setup.New(db, delivery, config.SecurityConfig{Secret: secret})
	service := New(db, delivery, "https://memento.example", auth)
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
	request.Header.Set("User-Agent", "memento-integration-test")
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: fixture.credential})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func currentInvitationBody(t *testing.T, fixture recipientFixture) string {
	t.Helper()
	recipient, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	require.NotNil(t, recipient.Invitation)
	return `{"invitation_id":"` + recipient.Invitation.ID + `"}`
}

func TestRecipientMutationsRequireSessionBoundCSRF(t *testing.T) {
	tests := []struct {
		name   string
		path   func(recipientFixture) string
		body   func(*testing.T, recipientFixture) string
		setup  func(*testing.T, recipientFixture)
		status int
	}{
		{"designate", func(f recipientFixture) string { return "/api/recipients/" + f.personID.String() + "/designate" }, func(*testing.T, recipientFixture) string { return `{"email":"alex@example.com"}` }, func(*testing.T, recipientFixture) {}, http.StatusCreated},
		{"send", func(f recipientFixture) string { return "/api/recipients/" + f.personID.String() + "/invitation/send" }, func(*testing.T, recipientFixture) string { return `{}` }, func(t *testing.T, f recipientFixture) { designate(t, f) }, http.StatusOK},
		{"revoke", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/revoke"
		}, currentInvitationBody, func(t *testing.T, f recipientFixture) { designate(t, f); deterministicIssue(t, f, 0x31) }, http.StatusOK},
		{"reissue", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/reissue"
		}, currentInvitationBody, func(t *testing.T, f recipientFixture) {
			designate(t, f)
			deterministicIssue(t, f, 0x32)
			f.service.random = bytes.NewReader(bytes.Repeat([]byte{0x33}, 48))
		}, http.StatusOK},
		{"remind", func(f recipientFixture) string {
			return "/api/recipients/" + f.personID.String() + "/invitation/remind"
		}, currentInvitationBody, func(t *testing.T, f recipientFixture) {
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
			body := test.body(t, fixture)
			withoutCSRF := serveRecipient(fixture, e, test.path(fixture), body, "")
			assert.Equal(t, http.StatusForbidden, withoutCSRF.Code)
			withCSRF := serveRecipient(fixture, e, test.path(fixture), body, fixture.csrf)
			assert.Equal(t, test.status, withCSRF.Code, withCSRF.Body.String())
		})
	}
}

func TestRecipientRoutesAttributeSecurityAuditRequests(t *testing.T) {
	fixture := newRecipientFixture(t)
	e := recipientHTTP(t, fixture)

	designated := serveRecipient(fixture, e, "/api/recipients/"+fixture.personID.String()+"/designate", `{"email":"alex@example.com"}`, fixture.csrf)
	require.Equal(t, http.StatusCreated, designated.Code, designated.Body.String())
	_, token := deterministicIssue(t, fixture, 0x35)
	accepted := serveRecipient(fixture, e, "/api/auth/invitations/accept", `{"token":"`+token+`"}`, "")
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	cookies := accepted.Result().Cookies()
	require.Len(t, cookies, 1)
	onboardingCookie := cookies[0]
	assert.Equal(t, setup.CookieName, onboardingCookie.Name)
	assert.True(t, onboardingCookie.Secure)
	assert.True(t, onboardingCookie.HttpOnly)
	assert.Zero(t, onboardingCookie.MaxAge, "acceptance starts with a non-persistent restricted Session")
	onboardingSession, err := fixture.auth.Session(context.Background(), onboardingCookie.Value)
	require.NoError(t, err)
	assert.True(t, onboardingSession.OnboardingRequired)

	readRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/onboarding", nil)
	readRequest.AddCookie(onboardingCookie)
	readResponse := httptest.NewRecorder()
	e.ServeHTTP(readResponse, readRequest)
	require.Equal(t, http.StatusOK, readResponse.Code, readResponse.Body.String())
	withoutCSRF := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/onboarding", strings.NewReader(`{"email_preference":"weekly","session_type":"public"}`))
	withoutCSRF.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	withoutCSRF.AddCookie(onboardingCookie)
	denied := httptest.NewRecorder()
	e.ServeHTTP(denied, withoutCSRF)
	assert.Equal(t, http.StatusForbidden, denied.Code)

	for _, action := range []string{"pending_recipient_designated", "invitation_accepted"} {
		var clientIP, userAgent string
		require.NoError(t, fixture.db.NewRaw(`SELECT client_ip::text, user_agent FROM security_audit_events WHERE action = ?`, action).Scan(context.Background(), &clientIP, &userAgent))
		assert.Equal(t, "192.0.2.1/32", clientIP, action)
		assert.Equal(t, "memento-integration-test", userAgent, action)
	}
}

func TestInvitationActionRejectsAnInspectedInvitationAfterReplacement(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	inspected, _ := deterministicIssue(t, fixture, 0x36)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x37}, 48))
	replacement, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(inspected.Invitation.ID))
	require.NoError(t, err)

	e := recipientHTTP(t, fixture)
	response := serveRecipient(fixture, e, "/api/recipients/"+fixture.personID.String()+"/invitation/revoke", `{"invitation_id":"`+inspected.Invitation.ID+`"}`, fixture.csrf)
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), "Invitation changed")
	current, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, replacement.Invitation.ID, current.Invitation.ID)
	assert.Equal(t, "active", current.Invitation.Status)
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
	message := fixture.sender.message(0)
	assert.Equal(t, "Alex@example.com", message.To)
	assert.Equal(t, "Robin invited you to Memento", message.Subject)
	assert.Contains(t, message.Body, "Hello Alex")
	assert.Contains(t, message.Body, "Robin invited you")
	assert.Contains(t, message.Body, "private family photo and video archive")
	assert.Contains(t, message.Body, "only for your login email")
	assert.Contains(t, message.Body, "can be used once")
	assert.Contains(t, message.Body, "complete Onboarding before any Media becomes available")
	assert.Contains(t, message.Body, "https://memento.example/invitation?token="+token)
	assert.Contains(t, message.Body, result.Invitation.ExpiresAt.Format(time.RFC1123))
}

func TestInspectIsReadOnlyAndAcceptIsSingleUseWithRestrictedOnboardingSession(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x37)
	before := *invitation.Invitation
	snapshot := func() string {
		var value string
		require.NoError(t, fixture.db.NewRaw(`
			SELECT jsonb_build_object(
				'invitation', (SELECT to_jsonb(row) FROM invitations AS row WHERE row.id = ?),
				'access', (SELECT to_jsonb(row) FROM recipient_access_generations AS row WHERE row.id = ?),
				'email', (SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at) FROM recipient_emails AS row WHERE row.recipient_access_generation_id = ?),
				'audits', (SELECT count(*) FROM security_audit_events),
				'sessions', (SELECT count(*) FROM sessions),
				'deliveries', (SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id) FROM email_deliveries AS row WHERE row.invitation_id = ?),
				'jobs', (SELECT count(*) FROM jobs)
			)::text
		`, invitation.Invitation.ID, invitation.Access.ID, invitation.Access.ID, invitation.Invitation.ID).Scan(context.Background(), &value))
		return value
	}
	beforeInspect := snapshot()

	inspected, err := fixture.service.Inspect(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "Alex", inspected.RecipientName)
	assert.Equal(t, "Robin", inspected.CuratorName)
	afterInspect, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, before, *afterInspect.Invitation)
	assert.Equal(t, beforeInspect, snapshot(), "inspection must not mutate hidden state")

	accepted, err := fixture.service.Accept(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "onboarding", accepted.Status)
	require.NotEmpty(t, accepted.session.Credential)
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
	assert.Equal(t, 1, sessions)
	restored, err := fixture.auth.Session(context.Background(), accepted.session.Credential)
	require.NoError(t, err)
	assert.True(t, restored.OnboardingRequired)
	_, err = fixture.auth.AuthorizeSession(context.Background(), accepted.session.Credential, "", false)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated, "general Recipient policies stay blocked")
	actor, err := fixture.auth.AuthorizeInterestSession(context.Background(), accepted.session.Credential, "", false)
	require.NoError(t, err)
	assert.Equal(t, fixture.personID, actor.PersonID)
	var queuedReminder int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE invitation_id = ? AND kind = ? AND status = 'queued'`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Scan(context.Background(), &queuedReminder))
	assert.Zero(t, queuedReminder)
}

func TestAcceptanceSessionFailureLeavesTheInvitationLiveAndRetryable(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	_, token := deterministicIssue(t, fixture, 0x66)
	_, err := fixture.db.ExecContext(context.Background(), `CREATE FUNCTION reject_onboarding_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'session rejected'; END $$; CREATE TRIGGER reject_onboarding_session BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION reject_onboarding_session()`)
	require.NoError(t, err)
	_, err = fixture.service.Accept(context.Background(), token)
	require.ErrorContains(t, err, "session rejected")
	inspected, err := fixture.service.Inspect(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "Alex", inspected.RecipientName)
	current, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	assert.Equal(t, "pending", current.Access.State)
	assert.Equal(t, "active", current.Invitation.Status)
}

func TestResumableOnboardingPersistsInterestsAndAtomicallyUnlocksCurrentAccessWithoutDeliveryBacklog(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x67)
	accepted, err := fixture.service.Accept(context.Background(), token)
	require.NoError(t, err)
	actor, err := fixture.auth.AuthorizeOnboardingSession(context.Background(), accepted.session.Credential, accepted.session.CSRFToken, true)
	require.NoError(t, err)

	partial := OnboardingRequest{
		PrivacyAcknowledged: true, EmailPreference: "weekly", SessionType: "public",
	}
	saved, err := fixture.service.SaveOnboarding(context.Background(), actor, partial, accepted.session.CSRFToken)
	require.NoError(t, err)
	assert.True(t, saved.PrivacyAcknowledged)
	assert.False(t, saved.EngagementAcknowledged)
	assert.Equal(t, "weekly", saved.EmailPreference)

	restoredSession, err := fixture.auth.Session(context.Background(), accepted.session.Credential)
	require.NoError(t, err)
	assert.True(t, restoredSession.OnboardingRequired)
	restored, err := fixture.service.Onboarding(context.Background(), actor, restoredSession.CSRFToken)
	require.NoError(t, err)
	assert.Equal(t, saved, restored)

	selectedID := uuid.New()
	circleID := uuid.New()
	_, err = fixture.db.NewRaw(`INSERT INTO people (id, display_name, sort_name) VALUES (?, 'Blair', 'blair'); INSERT INTO visibility_circles (id, name) VALUES (?, 'Onboarding family'); INSERT INTO visibility_circle_members (circle_id, person_id) VALUES (?, ?), (?, ?)`, selectedID, circleID, circleID, fixture.personID, circleID, selectedID).Exec(context.Background())
	require.NoError(t, err)
	interestService := visibility.New(fixture.db)
	interest, err := interestService.MutateInterest(context.Background(), actor, fixture.personID, selectedID, true, 0)
	require.NoError(t, err)
	require.Len(t, interest.Entries, 1)
	assert.Equal(t, selectedID.String(), interest.Entries[0].Person.ID)

	_, err = fixture.service.CompleteOnboarding(context.Background(), actor, partial)
	assert.ErrorIs(t, err, ErrOnboardingChoices)
	_, err = fixture.auth.AuthorizeSession(context.Background(), accepted.session.Credential, "", false)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated)

	completeRequest := OnboardingRequest{
		PrivacyAcknowledged: true, EngagementAcknowledged: true,
		InterestListAcknowledged: true, EmailPreviewsAcknowledged: true,
		PushGuidanceAcknowledged: true, EmailPreference: "weekly", SessionType: "trusted",
	}
	_, err = fixture.db.ExecContext(context.Background(), `CREATE FUNCTION reject_onboarding_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state = 'completed' AND OLD.state = 'onboarding' THEN RAISE EXCEPTION 'completion rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_onboarding_completion BEFORE UPDATE ON recipient_access_generations FOR EACH ROW EXECUTE FUNCTION reject_onboarding_completion()`)
	require.NoError(t, err)
	_, err = fixture.service.CompleteOnboarding(context.Background(), actor, completeRequest)
	require.ErrorContains(t, err, "completion rejected")
	var state string
	var choices int
	require.NoError(t, fixture.db.NewRaw(`SELECT state FROM recipient_access_generations WHERE id = ?`, actor.AccessID).Scan(context.Background(), &state))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM onboarding_choices WHERE recipient_access_generation_id = ?`, actor.AccessID).Scan(context.Background(), &choices))
	assert.Equal(t, "onboarding", state)
	assert.Zero(t, choices, "all completion writes must roll back together")
	_, err = fixture.auth.AuthorizeOnboardingSession(context.Background(), accepted.session.Credential, accepted.session.CSRFToken, true)
	require.NoError(t, err, "the original restricted Session survives a rolled-back completion")
	_, err = fixture.db.ExecContext(context.Background(), `DROP TRIGGER reject_onboarding_completion ON recipient_access_generations; DROP FUNCTION reject_onboarding_completion()`)
	require.NoError(t, err)

	var deliveriesBefore, outboxBefore int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries`).Scan(context.Background(), &deliveriesBefore))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(context.Background(), &outboxBefore))
	completed, err := fixture.service.CompleteOnboarding(context.Background(), actor, completeRequest)
	require.NoError(t, err)
	assert.Equal(t, "complete", completed.Status)
	assert.NotEqual(t, accepted.session.CSRFToken, completed.CSRFToken)
	_, err = fixture.auth.Session(context.Background(), accepted.session.Credential)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated, "completion rotates the credential")
	currentSession, err := fixture.auth.Session(context.Background(), completed.session.Credential)
	require.NoError(t, err)
	assert.False(t, currentSession.OnboardingRequired)
	assert.Equal(t, "trusted", currentSession.SessionType)
	_, err = fixture.auth.AuthorizeSession(context.Background(), completed.session.Credential, completed.CSRFToken, false)
	require.NoError(t, err, "completed access is immediately usable by current authorization policies")
	_, err = fixture.auth.AuthorizeOnboardingSession(context.Background(), completed.session.Credential, completed.CSRFToken, false)
	assert.ErrorIs(t, err, setup.ErrUnauthenticated)
	_, err = fixture.service.Onboarding(context.Background(), actor, completed.CSRFToken)
	assert.ErrorIs(t, err, ErrOnboardingUnavailable)

	completedActor, err := fixture.auth.AuthorizeInterestSession(context.Background(), completed.session.Credential, "", false)
	require.NoError(t, err)
	persistedInterest, err := interestService.InterestList(context.Background(), completedActor, fixture.personID, visibility.HistoryPageRequest{})
	require.NoError(t, err)
	require.Len(t, persistedInterest.Entries, 1)
	assert.Equal(t, selectedID.String(), persistedInterest.Entries[0].Person.ID)
	var informed, completedAt bool
	require.NoError(t, fixture.db.NewRaw(`SELECT privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged AND email_previews_acknowledged AND push_guidance_acknowledged, completed_at IS NOT NULL FROM onboarding_choices WHERE recipient_access_generation_id = ?`, actor.AccessID).Scan(context.Background(), &informed, &completedAt))
	assert.True(t, informed)
	assert.True(t, completedAt)
	var deliveriesAfter, outboxAfter, queuedReminder int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries`).Scan(context.Background(), &deliveriesAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM outbox_events`).Scan(context.Background(), &outboxAfter))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE invitation_id = ? AND kind = ? AND status = 'queued'`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Scan(context.Background(), &queuedReminder))
	assert.Equal(t, deliveriesBefore, deliveriesAfter)
	assert.Equal(t, outboxBefore, outboxAfter)
	assert.Zero(t, queuedReminder)
}

func TestRevocationExpiryAndReissueInvalidateOldTokens(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	oldInvitation, oldToken := deterministicIssue(t, fixture, 0x21)

	revoked, err := fixture.service.Revoke(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(oldInvitation.Invitation.ID))
	require.NoError(t, err)
	assert.Equal(t, "revoked", revoked.Invitation.Status)
	_, err = fixture.service.Inspect(context.Background(), oldToken)
	assert.ErrorIs(t, err, ErrInvitationToken)

	data := bytes.Repeat([]byte{0x58}, 48)
	fixture.service.random = bytes.NewReader(data)
	reissued, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(revoked.Invitation.ID))
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
	_, err = fixture.service.Remind(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(reissued.Invitation.ID))
	assert.ErrorIs(t, err, ErrInvitationNotFound)
}

func TestActiveReissueSupersedesOldTokenAndLeavesOneLiveInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	oldInvitation, oldToken := deterministicIssue(t, fixture, 0xf1)
	data := bytes.Repeat([]byte{0x01}, 48)
	fixture.service.random = bytes.NewReader(data)
	reissued, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(oldInvitation.Invitation.ID))
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
	for index, kind := range []string{emaildelivery.KindInvitationInitial, emaildelivery.KindInvitationAutomaticReminder} {
		require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, kind, fmt.Sprintf("changed-email-%d", index)))
		assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, kind)
	}
	assert.Zero(t, fixture.sender.count(), "queued Invitation mail must not go to the former login email")
}

func claimDelivery(t *testing.T, fixture recipientFixture, invitationID, kind, owner string) worker.Job {
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
	return job
}

func dispatchDelivery(t *testing.T, fixture recipientFixture, invitationID, kind, owner string) error {
	t.Helper()
	return fixture.delivery.Handle(context.Background(), claimDelivery(t, fixture, invitationID, kind, owner))
}

func assertDeliveryCancelled(t *testing.T, fixture recipientFixture, invitationID, kind string) {
	t.Helper()
	var status string
	var problems int
	require.NoError(t, fixture.db.NewRaw(`SELECT status FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, invitationID, kind).Scan(context.Background(), &status))
	require.NoError(t, fixture.db.NewRaw(`
		SELECT count(*) FROM delivery_problems AS problem
		JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id
		WHERE delivery.invitation_id = ? AND delivery.kind = ?
	`, invitationID, kind).Scan(context.Background(), &problems))
	assert.Equal(t, "cancelled", status)
	assert.Zero(t, problems)
}

func TestAllInvitationDeliveryKindsCompleteAndRecordResults(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x51)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "initial-success"))
	_, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(invitation.Invitation.ID))
	require.NoError(t, err)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationManualReminder, "manual-success"))
	_, err = fixture.db.NewRaw(`UPDATE email_deliveries SET available_at = now() WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder, "automatic-success"))
	assert.Equal(t, 3, fixture.sender.count())
	assert.Equal(t, "Reminder: your Memento Invitation", fixture.sender.message(2).Subject)
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

func TestCuratorInspectionReportsPermanentInvitationDeliveryFailure(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x52)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "permanent-failure")
	secret := "integration-security-secret-at-least-32-bytes"
	fixture.delivery = emaildelivery.New(fixture.db, config.SMTPConfig{Enabled: true, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: 24 * time.Hour}, rejectingSender{}, secret)

	require.Error(t, fixture.delivery.Handle(context.Background(), job))
	inspected, err := fixture.service.Get(context.Background(), fixture.personID)
	require.NoError(t, err)
	require.NotNil(t, inspected.Invitation.InitialDelivery)
	assert.Equal(t, "failed", inspected.Invitation.InitialDelivery.Status)
	assert.Equal(t, 1, inspected.Invitation.InitialDelivery.Attempts)
	require.NotNil(t, inspected.Invitation.InitialDelivery.Failure)
	assert.Equal(t, "recipient_rejected", *inspected.Invitation.InitialDelivery.Failure)
}

func TestInvitationDeliveryCommitsBeforeSMTP(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x47)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "committed-before-smtp")

	blocking := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	secret := "integration-security-secret-at-least-32-bytes"
	fixture.delivery = emaildelivery.New(fixture.db, config.SMTPConfig{Enabled: true, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: 24 * time.Hour}, blocking, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	delivered := make(chan error, 1)
	go func() { delivered <- fixture.delivery.Handle(ctx, job) }()
	select {
	case <-blocking.started:
	case <-ctx.Done():
		require.FailNow(t, "SMTP delivery did not start", ctx.Err())
	}

	revoked := make(chan error, 1)
	go func() {
		_, err := fixture.service.Revoke(ctx, fixture.actor, fixture.personID, uuid.MustParse(invitation.Invitation.ID))
		revoked <- err
	}()
	select {
	case err := <-revoked:
		require.NoError(t, err, "revocation must not wait on external SMTP work")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "revocation was blocked by SMTP delivery")
	}

	close(blocking.release)
	require.NoError(t, <-delivered)
	var sentAt, revokedAt *time.Time
	require.NoError(t, fixture.db.NewRaw(`SELECT sent_at, revoked_at FROM invitations WHERE id = ?`, invitation.Invitation.ID).Scan(ctx, &sentAt, &revokedAt))
	require.NotNil(t, sentAt)
	require.NotNil(t, revokedAt)
}

func TestTerminalDeliveryLocksInvitationBeforeDelivery(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x50)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "terminal-lock-order")

	blocking := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	secret := "integration-security-secret-at-least-32-bytes"
	fixture.delivery = emaildelivery.New(fixture.db, config.SMTPConfig{Enabled: true, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: 24 * time.Hour}, blocking, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	delivered := make(chan error, 1)
	go func() { delivered <- fixture.delivery.Handle(ctx, job) }()
	select {
	case <-blocking.started:
	case <-ctx.Done():
		require.FailNow(t, "SMTP delivery did not start", ctx.Err())
	}

	invitationTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = invitationTx.Rollback() }()
	_, err = invitationTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, invitationTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	close(blocking.release)
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM invitations WHERE id =%FOR UPDATE%`)
	_, err = invitationTx.NewRaw(`
		SELECT id FROM email_deliveries WHERE invitation_id = ? AND kind = ? FOR UPDATE NOWAIT
	`, invitation.Invitation.ID, emaildelivery.KindInvitationInitial).Exec(ctx)
	require.NoError(t, err, "terminal recording must not lock delivery before its Invitation")
	require.NoError(t, invitationTx.Rollback())
	require.NoError(t, <-delivered)
}

func TestDeliveryRechecksObsolescenceAfterWaitingForInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x48)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "recheck-obsolescence")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	terminalTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = terminalTx.Rollback() }()
	_, err = terminalTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = terminalTx.NewRaw(`UPDATE invitations SET revoked_at = ? WHERE id = ?`, fixture.now, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, terminalTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	delivered := make(chan error, 1)
	go func() { delivered <- fixture.delivery.Handle(ctx, job) }()
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM invitations WHERE id =%FOR UPDATE%`)
	require.NoError(t, terminalTx.Commit())
	require.NoError(t, <-delivered)
	assert.Zero(t, fixture.sender.count())
	assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial)
	require.NoError(t, fixture.delivery.Handle(ctx, job), "cancelled delivery retries must be idempotent")
}

func TestObsoleteDeliveryDoesNotCancelAfterLosingItsLease(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x49)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "obsolete-lease-owner")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	terminalTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = terminalTx.Rollback() }()
	_, err = terminalTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = terminalTx.NewRaw(`UPDATE invitations SET revoked_at = ? WHERE id = ?`, fixture.now, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, terminalTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	delivered := make(chan error, 1)
	go func() { delivered <- fixture.delivery.Handle(ctx, job) }()
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM invitations WHERE id =%FOR UPDATE%`)
	_, err = fixture.db.NewRaw(`UPDATE jobs SET lease_owner = 'replacement-owner', lease_expires_at = now() + interval '1 minute' WHERE id = ?`, job.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, terminalTx.Commit())
	require.EqualError(t, <-delivered, "email delivery job lease lost")

	var status string
	var problems int
	require.NoError(t, fixture.db.NewRaw(`SELECT status FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationInitial).Scan(ctx, &status))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM delivery_problems AS problem JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id WHERE delivery.invitation_id = ? AND delivery.kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationInitial).Scan(ctx, &problems))
	assert.Equal(t, "queued", status)
	assert.Zero(t, problems)
}

func TestDeliveryUsesWallClockWhenInvitationExpiresWhileWaitingForLock(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x4a)
	job := claimDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial, "expiry-recheck")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	expiryTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = expiryTx.Rollback() }()
	_, err = expiryTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = expiryTx.NewRaw(`
		UPDATE invitations AS invitation
		SET issued_at = boundary.expires_at - interval '14 days',
		    expires_at = boundary.expires_at,
		    automatic_reminder_scheduled_at = boundary.expires_at - interval '7 days'
		FROM (SELECT clock_timestamp() + interval '500 milliseconds' AS expires_at) AS boundary
		WHERE invitation.id = ?
	`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, expiryTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	delivered := make(chan error, 1)
	go func() { delivered <- fixture.delivery.Handle(ctx, job) }()
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM invitations WHERE id =%FOR UPDATE%`)
	time.Sleep(600 * time.Millisecond)
	require.NoError(t, expiryTx.Commit())
	require.NoError(t, <-delivered)
	assert.Zero(t, fixture.sender.count())
	assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationInitial)
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
	require.NoError(t, err)
	assert.Zero(t, fixture.sender.count())
	assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder)
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
				_, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(invitation.Invitation.ID))
				require.NoError(t, err)
			case "expired":
				_, err := fixture.db.NewRaw(`UPDATE invitations SET issued_at = now() - interval '15 days', expires_at = now() - interval '1 day', automatic_reminder_scheduled_at = now() - interval '8 days' WHERE id = ?`, invitation.Invitation.ID).Exec(context.Background())
				require.NoError(t, err)
			}
			if terminal != "accepted" {
				_, err := fixture.db.NewRaw(`UPDATE email_deliveries SET available_at = now() WHERE invitation_id = ? AND kind = ?`, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder).Exec(context.Background())
				require.NoError(t, err)
				err = dispatchDelivery(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder, "stale-"+terminal)
				require.NoError(t, err)
			}
			assert.Zero(t, fixture.sender.count())
			assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder)
		})
	}
}

func TestRevokedInvitationCancelsItsScheduledAutomaticReminderAtDelivery(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, _ := deterministicIssue(t, fixture, 0x72)
	_, err := fixture.service.Revoke(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(invitation.Invitation.ID))
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
	require.NoError(t, err)
	assert.Equal(t, 0, fixture.sender.count())
	assertDeliveryCancelled(t, fixture, invitation.Invitation.ID, emaildelivery.KindInvitationAutomaticReminder)
}

func TestManualReminderIsDurableAndDoesNotExtendInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	sent, _ := deterministicIssue(t, fixture, 0x63)
	originalExpiry := sent.Invitation.ExpiresAt

	_, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(sent.Invitation.ID))
	require.ErrorIs(t, err, ErrInvitationNotSent)
	_, err = fixture.db.NewRaw(`UPDATE invitations SET sent_at = ? WHERE id = ?`, fixture.now, sent.Invitation.ID).Exec(context.Background())
	require.NoError(t, err)
	reminded, err := fixture.service.Remind(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(sent.Invitation.ID))
	require.NoError(t, err)
	assert.Equal(t, originalExpiry, reminded.Invitation.ExpiresAt)
	assert.Equal(t, 1, reminded.Invitation.ManualReminderCount)
	assert.Equal(t, fixture.now, *reminded.Invitation.LastManualReminderRequestedAt)
	var deliveryCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM email_deliveries WHERE invitation_id = ? AND kind = ?`, reminded.Invitation.ID, emaildelivery.KindInvitationManualReminder).Scan(context.Background(), &deliveryCount))
	assert.Equal(t, 1, deliveryCount)
}

func waitForBlockedQuery(t *testing.T, ctx context.Context, db *bun.DB, blockerPID int, pattern string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		var waiting bool
		err := db.NewRaw(`
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'
				  AND ? = ANY(pg_blocking_pids(pid)) AND query LIKE ?
			)
		`, blockerPID, pattern).Scan(ctx, &waiting)
		require.NoError(t, err)
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "expected query did not wait for the controlled lock", pattern)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestArchivePersonLockAllowsConcurrentSessionAuditToFinish(t *testing.T) {
	fixture := newRecipientFixture(t)
	recipient := designate(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var epoch []byte
	require.NoError(t, fixture.db.NewRaw(`SELECT security_epoch FROM system_settings WHERE id = 1`).Scan(ctx, &epoch))
	sessionID := uuid.New()
	credentialHash := sha256.Sum256(bytes.Repeat([]byte{0x49}, 32))
	_, err := fixture.db.NewRaw(`
		INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
		VALUES (?, ?, ?, ?, ?, 'trusted', now() + interval '1 hour')
	`, sessionID, credentialHash[:], fixture.personID, recipient.Access.ID, epoch).Exec(ctx)
	require.NoError(t, err)

	logoutTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = logoutTx.Rollback() }()
	_, err = logoutTx.NewRaw(`UPDATE sessions SET revoked_at = now() WHERE id = ?`, sessionID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, logoutTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	archived := make(chan error, 1)
	go func() {
		_, err := people.New(fixture.db).Archive(ctx, fixture.actor, fixture.personID, 1)
		archived <- err
	}()
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%UPDATE sessions SET revoked_at%`)

	_, err = logoutTx.NewRaw(`
		INSERT INTO security_audit_events (actor_person_id, subject_person_id, action, outcome, session_id)
		VALUES (?, ?, 'session_signed_out', 'success', ?)
	`, fixture.personID, fixture.personID, sessionID).Exec(ctx)
	require.NoError(t, err, "Person NO KEY UPDATE locks must remain compatible with audit foreign-key checks")
	require.NoError(t, logoutTx.Commit())
	require.NoError(t, <-archived)
}

func TestDesignateRechecksPersonAfterWaitingForArchive(t *testing.T) {
	fixture := newRecipientFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	archiveTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = archiveTx.Rollback() }()
	_, err = archiveTx.NewRaw(`SELECT id FROM people WHERE id = ? FOR NO KEY UPDATE`, fixture.personID).Exec(ctx)
	require.NoError(t, err)
	_, err = archiveTx.NewRaw(`UPDATE people SET archived_at = ?, updated_at = ? WHERE id = ?`, fixture.now, fixture.now, fixture.personID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, archiveTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	designated := make(chan error, 1)
	go func() {
		_, err := fixture.service.Designate(ctx, fixture.actor, fixture.personID, DesignateRequest{Email: "alex@example.com"})
		designated <- err
	}()
	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM people WHERE id IN%ORDER BY id FOR NO KEY UPDATE%`)
	require.NoError(t, archiveTx.Commit())
	require.ErrorIs(t, <-designated, ErrPersonUnavailable)
	var accessCount int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM recipient_access_generations WHERE person_id = ?`, fixture.personID).Scan(ctx, &accessCount))
	assert.Zero(t, accessCount)
}

func TestInvitationActionsLockPeopleBeforeRecipientState(t *testing.T) {
	actions := map[string]func(context.Context, recipientFixture, uuid.UUID) error{
		"revoke": func(ctx context.Context, fixture recipientFixture, invitationID uuid.UUID) error {
			_, err := fixture.service.Revoke(ctx, fixture.actor, fixture.personID, invitationID)
			return err
		},
		"reissue": func(ctx context.Context, fixture recipientFixture, invitationID uuid.UUID) error {
			_, err := fixture.service.Reissue(ctx, fixture.actor, fixture.personID, invitationID)
			return err
		},
		"remind": func(ctx context.Context, fixture recipientFixture, invitationID uuid.UUID) error {
			_, err := fixture.service.Remind(ctx, fixture.actor, fixture.personID, invitationID)
			return err
		},
	}
	for name, action := range actions {
		t.Run(name, func(t *testing.T) {
			fixture := newRecipientFixture(t)
			designate(t, fixture)
			invitation, _ := deterministicIssue(t, fixture, 0x44)
			fixture.service.random = randReader{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			personTx, err := fixture.db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = personTx.Rollback() }()
			_, err = personTx.NewRaw(`SELECT id FROM people WHERE id = ? FOR NO KEY UPDATE`, fixture.personID).Exec(ctx)
			require.NoError(t, err)
			var blockerPID int
			require.NoError(t, personTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

			finished := make(chan error, 1)
			go func() { finished <- action(ctx, fixture, uuid.MustParse(invitation.Invitation.ID)) }()
			waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM people WHERE id IN%ORDER BY id FOR NO KEY UPDATE%`)
			_, err = personTx.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR UPDATE NOWAIT`, invitation.Access.ID).Exec(ctx)
			require.NoError(t, err, "Invitation action must not lock Recipient access before its Person")
			_, err = personTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE NOWAIT`, invitation.Invitation.ID).Exec(ctx)
			require.NoError(t, err, "Invitation action must not lock Invitation before its Person")
			require.NoError(t, personTx.Rollback())
			actionErr := <-finished
			if name == "remind" {
				require.ErrorIs(t, actionErr, ErrInvitationNotSent)
			} else {
				require.NoError(t, actionErr)
			}
		})
	}
}

func TestAcceptLocksPersonBeforeRecipientAccess(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x45)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	archiveTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = archiveTx.Rollback() }()
	_, err = archiveTx.NewRaw(`SELECT id FROM people WHERE id = ? FOR NO KEY UPDATE`, fixture.personID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, archiveTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	accepted := make(chan error, 1)
	go func() {
		_, err := fixture.service.Accept(ctx, token)
		accepted <- err
	}()

	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM people WHERE id =%FOR NO KEY UPDATE%`)
	_, err = archiveTx.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR UPDATE NOWAIT`, invitation.Access.ID).Exec(ctx)
	require.NoError(t, err, "acceptance must not lock Recipient access before its Person")
	require.NoError(t, archiveTx.Rollback())
	require.NoError(t, <-accepted)
}

func TestAcceptLocksRecipientAccessBeforeInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	invitation, token := deterministicIssue(t, fixture, 0x46)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	accessTx, err := fixture.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = accessTx.Rollback() }()
	_, err = accessTx.NewRaw(`SELECT id FROM recipient_access_generations WHERE id = ? FOR UPDATE`, invitation.Access.ID).Exec(ctx)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, accessTx.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID))

	accepted := make(chan error, 1)
	go func() {
		_, err := fixture.service.Accept(ctx, token)
		accepted <- err
	}()

	waitForBlockedQuery(t, ctx, fixture.db, blockerPID, `%SELECT id FROM recipient_access_generations WHERE id =%FOR UPDATE%`)
	_, err = accessTx.NewRaw(`SELECT id FROM invitations WHERE id = ? FOR UPDATE NOWAIT`, invitation.Invitation.ID).Exec(ctx)
	require.NoError(t, err, "acceptance must not lock its Invitation before Recipient access")
	require.NoError(t, accessTx.Rollback())
	require.NoError(t, <-accepted)
}

func TestConcurrentReissueAllowsOnlyTheActionTargetingTheCurrentInvitation(t *testing.T) {
	fixture := newRecipientFixture(t)
	designate(t, fixture)
	current, _ := deterministicIssue(t, fixture, 0x71)
	fixture.service.random = randReader{}

	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.service.Reissue(context.Background(), fixture.actor, fixture.personID, uuid.MustParse(current.Invitation.ID))
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	var successes, stale int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInvitationStale) {
			stale++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, stale)
	var live int
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM invitations WHERE accepted_at IS NULL AND revoked_at IS NULL AND superseded_at IS NULL`).Scan(context.Background(), &live))
	assert.Equal(t, 1, live)
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
