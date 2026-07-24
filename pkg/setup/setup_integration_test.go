//go:build integration

package setup

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
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
	"github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

const integrationSecret = "test-only-security-secret-32-bytes"

type acceptingSender struct {
	messages chan smtp.Message
}

func (sender *acceptingSender) Send(_ context.Context, message smtp.Message) error {
	sender.messages <- message
	return nil
}

var (
	setupSenders sync.Map
	setupCodes   sync.Map
)

func newSetupService(t *testing.T) (*bun.DB, *Service) {
	t.Helper()
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	sender := &acceptingSender{messages: make(chan smtp.Message, 4)}
	setupSenders.Store(db, sender)
	t.Cleanup(func() {
		setupSenders.Delete(db)
		setupCodes.Delete(db)
	})
	delivery := emaildelivery.New(db, config.SMTPConfig{Enabled: true, RetryWindow: time.Hour}, sender, integrationSecret)
	return db, New(db, delivery, testSecurityConfig())
}

func newSetupHTTP(t *testing.T, service *Service) *echo.Echo {
	t.Helper()
	e := echo.New()
	requestBinder, err := binder.New()
	require.NoError(t, err)
	e.Binder = requestBinder
	RegisterRoutes(e, NewHandler(service))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func requestedChallenge(t *testing.T, db *bun.DB, service *Service, name, email string) RequestCodeResponse {
	t.Helper()
	response, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: name, Email: email})
	require.NoError(t, err)
	var persistedBody string
	require.NoError(t, db.NewRaw(`SELECT body FROM email_deliveries ORDER BY id DESC LIMIT 1`).Scan(context.Background(), &persistedBody))
	assert.True(t, strings.HasPrefix(persistedBody, "v1:"), "setup code email must be encrypted at rest")
	deliverLatestCode(t, db, service.delivery)
	return response
}

func deliverLatestCode(t *testing.T, db *bun.DB, delivery *emaildelivery.Service) {
	t.Helper()
	ctx := context.Background()
	dispatched, err := outbox.New(db).Dispatch(ctx, "setup-test-dispatcher", time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)
	var job worker.Job
	err = db.NewRaw(`
		WITH candidate AS (
			SELECT id FROM jobs WHERE status = 'pending' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE jobs AS job SET status = 'running', lease_owner = 'setup-test-worker',
			lease_expires_at = now() + interval '1 minute'
		FROM candidate WHERE job.id = candidate.id
		RETURNING job.id, job.kind, job.payload, job.attempts
	`).Scan(ctx, &job.ID, &job.Kind, &job.Payload, &job.Attempts)
	require.NoError(t, err)
	job.LeaseOwner = "setup-test-worker"
	require.NoError(t, delivery.Handle(ctx, job))
	var persistedBody string
	require.NoError(t, db.NewRaw(`SELECT body FROM email_deliveries ORDER BY id DESC LIMIT 1`).Scan(ctx, &persistedBody))
	assert.Empty(t, persistedBody, "sensitive body must be erased after terminal delivery")
	_, err = db.NewRaw(`
		UPDATE jobs SET status = 'completed', lease_owner = NULL, lease_expires_at = NULL WHERE id = ?
	`, job.ID).Exec(ctx)
	require.NoError(t, err)

	senderValue, ok := setupSenders.Load(db)
	require.True(t, ok)
	message := <-senderValue.(*acceptingSender).messages
	code := regexp.MustCompile(`\b[0-9]{8}\b`).FindString(message.Body)
	require.Len(t, code, 8)
	setupCodes.Store(db, code)
}

func latestCode(t *testing.T, db *bun.DB) string {
	t.Helper()
	code, ok := setupCodes.Load(db)
	require.True(t, ok)
	return code.(string)
}

func verifiedChallenge(t *testing.T, db *bun.DB, service *Service, name, email string) VerifyCodeResponse {
	t.Helper()
	challenge := requestedChallenge(t, db, service, name, email)
	response, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.NoError(t, err)
	return response
}

func completionRequest(token, sessionType string) CompleteRequest {
	return CompleteRequest{
		VerificationToken:        token,
		PrivacyAcknowledged:      true,
		EngagementAcknowledged:   true,
		InterestListAcknowledged: true,
		EmailPreference:          "weekly",
		SessionType:              sessionType,
	}
}

func performJSON(e *echo.Echo, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.TLS = new(tls.ConnectionState)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestCodeRequestRollsBackWhenRequiredEmailIsUnavailable(t *testing.T) {
	db := testdb.Open(t)
	require.NoError(t, migrations.Apply(context.Background(), db))
	service := New(db, emaildelivery.New(db, config.SMTPConfig{}, nil), testSecurityConfig())

	_, err := service.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Robin", Email: "robin@example.com"})
	require.ErrorIs(t, err, emaildelivery.ErrNotConfigured)
	for _, table := range []string{"email_deliveries", "outbox_events", "login_challenges"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count, table)
	}
}

func TestExpiredSetupCodeEmailIsNeverDelivered(t *testing.T) {
	db, service := newSetupService(t)
	_, err := service.RequestCode(context.Background(), RequestCodeRequest{
		DisplayName: "Expired Delivery", Email: "expired-delivery@example.com",
	})
	require.NoError(t, err)
	_, err = db.NewRaw(`UPDATE email_deliveries SET deliver_before = now() - interval '1 second'`).Exec(context.Background())
	require.NoError(t, err)
	dispatched, err := outbox.New(db).Dispatch(context.Background(), "expired-dispatcher", time.Minute)
	require.NoError(t, err)
	require.True(t, dispatched)
	var job worker.Job
	err = db.NewRaw(`
		WITH candidate AS (SELECT id FROM jobs WHERE status = 'pending' ORDER BY id LIMIT 1)
		UPDATE jobs AS job SET status = 'running', lease_owner = 'expired-worker',
			lease_expires_at = now() + interval '1 minute'
		FROM candidate WHERE job.id = candidate.id
		RETURNING job.id, job.kind, job.payload, job.attempts
	`).Scan(context.Background(), &job.ID, &job.Kind, &job.Payload, &job.Attempts)
	require.NoError(t, err)
	job.LeaseOwner = "expired-worker"
	err = service.delivery.Handle(context.Background(), job)
	require.ErrorContains(t, err, "delivery_expired")

	senderValue, ok := setupSenders.Load(db)
	require.True(t, ok)
	assert.Empty(t, senderValue.(*acceptingSender).messages)
	var status, body string
	require.NoError(t, db.NewRaw(`SELECT status, body FROM email_deliveries`).Scan(context.Background(), &status, &body))
	assert.Equal(t, "failed", status)
	assert.Empty(t, body)
}

func TestCodeRequestAndVerificationCreateNoIdentityOrSession(t *testing.T) {
	db, service := newSetupService(t)
	challenge := requestedChallenge(t, db, service, "Robin Joseph", "Robin@Example.com")
	assert.Equal(t, "queued", challenge.Status)
	assert.Len(t, challenge.ChallengeID, 64)

	verified, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.NoError(t, err)
	assert.Equal(t, "verified", verified.Status)
	assert.Len(t, verified.VerificationToken, 64)

	for _, table := range []string{"people", "person_roles", "recipient_access_generations", "sessions"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count, table)
	}
	_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: latestCode(t, db)})
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestLockWaitCannotExtendChallengeOrVerificationLifetime(t *testing.T) {
	t.Run("code verification", func(t *testing.T) {
		db, service := newSetupService(t)
		challenge := requestedChallenge(t, db, service, "Delayed Code", "delayed-code@example.com")
		code := latestCode(t, db)
		_, err := db.NewRaw(`UPDATE login_challenges SET expires_at = now() + interval '100 milliseconds'`).Exec(context.Background())
		require.NoError(t, err)
		lock, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		_, err = lock.NewRaw(`SELECT id FROM system_settings WHERE id = 1 FOR UPDATE`).Exec(context.Background())
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() {
			_, verifyErr := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: code})
			result <- verifyErr
		}()
		time.Sleep(200 * time.Millisecond)
		require.NoError(t, lock.Commit())
		require.ErrorIs(t, <-result, ErrInvalidCode)
	})

	t.Run("final completion", func(t *testing.T) {
		db, service := newSetupService(t)
		verified := verifiedChallenge(t, db, service, "Delayed Completion", "delayed-completion@example.com")
		_, err := db.NewRaw(`UPDATE login_challenges SET verification_expires_at = now() + interval '100 milliseconds'`).Exec(context.Background())
		require.NoError(t, err)
		lock, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		_, err = lock.NewRaw(`SELECT id FROM system_settings WHERE id = 1 FOR UPDATE`).Exec(context.Background())
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() {
			_, completeErr := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
			result <- completeErr
		}()
		time.Sleep(200 * time.Millisecond)
		require.NoError(t, lock.Commit())
		require.ErrorIs(t, <-result, ErrInvalidToken)
		var people int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
		assert.Zero(t, people)
	})
}

func TestCodeExpiryAndFiveAttemptLimit(t *testing.T) {
	t.Run("expired code", func(t *testing.T) {
		db, service := newSetupService(t)
		challenge := requestedChallenge(t, db, service, "Expired Person", "expired@example.com")
		code := latestCode(t, db)
		_, err := db.NewRaw(`UPDATE login_challenges SET expires_at = now() - interval '1 second'`).Exec(context.Background())
		require.NoError(t, err)

		_, err = service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: code})
		require.ErrorIs(t, err, ErrInvalidCode)
	})

	t.Run("verified setup uses its independent completion lifetime", func(t *testing.T) {
		db, service := newSetupService(t)
		verified := verifiedChallenge(t, db, service, "Independent Completion", "independent-completion@example.com")
		_, err := db.NewRaw(`UPDATE login_challenges SET expires_at = now() - interval '1 second'`).Exec(context.Background())
		require.NoError(t, err)

		_, err = service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
		require.NoError(t, err)
	})

	t.Run("verified challenge expires before completion", func(t *testing.T) {
		db, service := newSetupService(t)
		verified := verifiedChallenge(t, db, service, "Expired Completion", "expired-completion@example.com")
		_, err := db.NewRaw(`UPDATE login_challenges SET verification_expires_at = now() - interval '1 second'`).Exec(context.Background())
		require.NoError(t, err)

		_, err = service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
		require.ErrorIs(t, err, ErrInvalidToken)
		var people, sessions int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
		assert.Zero(t, people)
		assert.Zero(t, sessions)
	})

	t.Run("attempt limit", func(t *testing.T) {
		db, service := newSetupService(t)
		challenge := requestedChallenge(t, db, service, "Limited Person", "limited@example.com")
		correctCode := latestCode(t, db)
		invalidCode := "00000000"
		if correctCode == invalidCode {
			invalidCode = "11111111"
		}
		for range 5 {
			_, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: invalidCode})
			require.ErrorIs(t, err, ErrInvalidCode)
		}
		_, err := service.VerifyCode(context.Background(), VerifyCodeRequest{ChallengeID: challenge.ChallengeID, Code: correctCode})
		require.ErrorIs(t, err, ErrInvalidCode)
		var attempts int
		require.NoError(t, db.NewRaw(`SELECT attempts FROM login_challenges`).Scan(context.Background(), &attempts))
		assert.Equal(t, 5, attempts)
	})
}

func TestFinalSetupStoresCompletedIdentityChoicesAndOpaqueSession(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Robin Joseph", "Robin@Example.com")
	completed, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	assert.Len(t, completed.Credential, 64)
	assert.Len(t, completed.CSRFToken, 64)

	var people, roles, generations, emails, choices, preferences, sessions int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM person_roles`).Scan(context.Background(), &roles))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM recipient_access_generations WHERE state = 'completed' AND onboarding_completed_at IS NOT NULL`).Scan(context.Background(), &generations))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM recipient_emails WHERE normalized_email = 'robin@example.com' AND is_current`).Scan(context.Background(), &emails))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM onboarding_choices WHERE privacy_acknowledged AND engagement_acknowledged AND interest_list_acknowledged AND email_preference = 'weekly'`).Scan(context.Background(), &choices))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM notification_preferences WHERE email_preference = 'weekly'`).Scan(context.Background(), &preferences))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE session_type = 'trusted' AND credential_hash IS NOT NULL`).Scan(context.Background(), &sessions))
	assert.Equal(t, 1, people)
	assert.Equal(t, 2, roles)
	assert.Equal(t, 1, generations)
	assert.Equal(t, 1, emails)
	assert.Equal(t, 1, choices)
	assert.Equal(t, 1, preferences)
	assert.Equal(t, 1, sessions)

	var auditActions []string
	require.NoError(t, db.NewRaw(`SELECT action FROM security_audit_events ORDER BY id`).Scan(context.Background(), &auditActions))
	assert.Equal(t, []string{"setup_code_requested", "setup_code_verified", "setup_completed", "session_created"}, auditActions)

	var storedCredential bool
	require.NoError(t, db.NewRaw(`SELECT EXISTS (SELECT 1 FROM sessions WHERE encode(credential_hash, 'hex') = ?)`, completed.Credential).Scan(context.Background(), &storedCredential))
	assert.False(t, storedCredential, "the browser credential must never be stored directly")
}

func TestLateFinalSetupFailureRollsBackEveryWrite(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Rollback Person", "rollback@example.com")
	_, err := db.ExecContext(context.Background(), `
		CREATE FUNCTION reject_setup_completion_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.action = 'setup_completed' THEN
				RAISE EXCEPTION 'deterministic late setup failure';
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER reject_setup_completion_audit
		BEFORE INSERT ON security_audit_events
		FOR EACH ROW EXECUTE FUNCTION reject_setup_completion_audit();
	`)
	require.NoError(t, err)

	_, err = service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.ErrorContains(t, err, "complete setup")
	for _, table := range []string{
		"people", "person_roles", "recipient_access_generations", "recipient_emails",
		"onboarding_choices", "notification_preferences", "sessions",
	} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count, table)
	}
	var complete, consumed bool
	require.NoError(t, db.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1`).Scan(context.Background(), &complete))
	require.NoError(t, db.NewRaw(`SELECT consumed_at IS NOT NULL FROM login_challenges WHERE verification_token_hash IS NOT NULL`).Scan(context.Background(), &consumed))
	assert.False(t, complete)
	assert.False(t, consumed)
	var completionAudits int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action IN ('setup_completed', 'session_created')`).Scan(context.Background(), &completionAudits))
	assert.Zero(t, completionAudits)
}

func TestConcurrentFinalSetupHasOneWinnerAndOneConflict(t *testing.T) {
	db, service := newSetupService(t)
	first := verifiedChallenge(t, db, service, "First Browser", "first@example.com")
	second := verifiedChallenge(t, db, service, "Second Browser", "second@example.com")
	e := newSetupHTTP(t, service)
	requests := []struct {
		name    string
		request CompleteRequest
	}{
		{name: "First Browser", request: completionRequest(first.VerificationToken, "trusted")},
		{name: "Second Browser", request: completionRequest(second.VerificationToken, "trusted")},
	}
	type namedResponse struct {
		name     string
		response *httptest.ResponseRecorder
	}
	start := make(chan struct{})
	responses := make(chan namedResponse, 2)
	var wait sync.WaitGroup
	for _, request := range requests {
		payload, err := json.Marshal(request.request)
		require.NoError(t, err)
		wait.Add(1)
		go func(name, body string) {
			defer wait.Done()
			<-start
			responses <- namedResponse{name: name, response: performJSON(e, http.MethodPost, "/api/setup/complete", body, nil)}
		}(request.name, string(payload))
	}
	close(start)
	wait.Wait()
	close(responses)
	statuses := make([]int, 0, 2)
	var winner, loser namedResponse
	for result := range responses {
		statuses = append(statuses, result.response.Code)
		if result.response.Code == http.StatusCreated {
			winner = result
		} else {
			loser = result
		}
	}
	assert.ElementsMatch(t, []int{http.StatusCreated, http.StatusConflict}, statuses)
	require.NotNil(t, winner.response)
	require.NotNil(t, loser.response)
	winnerCookies := winner.response.Result().Cookies()
	require.Len(t, winnerCookies, 1)
	winnerSession, err := service.Session(context.Background(), winnerCookies[0].Value)
	require.NoError(t, err)
	assert.Equal(t, winner.name, winnerSession.DisplayName)
	var storedName, storedEmail string
	require.NoError(t, db.NewRaw(`
		SELECT person.display_name, email.normalized_email
		FROM people AS person
		JOIN recipient_access_generations AS access ON access.person_id = person.id AND access.is_current
		JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
	`).Scan(context.Background(), &storedName, &storedEmail))
	assert.Equal(t, winner.name, storedName)
	assert.Equal(t, strings.ToLower(strings.Split(winner.name, " ")[0])+"@example.com", storedEmail)

	for table, expected := range map[string]int{
		"people": 1, "person_roles": 2, "recipient_access_generations": 1,
		"recipient_emails": 1, "onboarding_choices": 1, "notification_preferences": 1, "sessions": 1,
	} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Equal(t, expected, count, table)
	}
	var consumed int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM login_challenges WHERE consumed_at IS NOT NULL`).Scan(context.Background(), &consumed))
	assert.Equal(t, 1, consumed)
	var loserUnconsumed bool
	loserEmail := strings.ToLower(strings.Split(loser.name, " ")[0]) + "@example.com"
	require.NoError(t, db.NewRaw(`
		SELECT consumed_at IS NULL FROM login_challenges WHERE normalized_email = ?
	`, loserEmail).Scan(context.Background(), &loserUnconsumed))
	assert.True(t, loserUnconsumed)
}

func TestSecureCookiesAndSessionBoundCSRF(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Cookie Person", "cookie@example.com")
	e := newSetupHTTP(t, service)
	payload, err := json.Marshal(completionRequest(verified.VerificationToken, "public"))
	require.NoError(t, err)
	completeResponse := performJSON(e, http.MethodPost, "/api/setup/complete", string(payload), nil)
	require.Equal(t, http.StatusCreated, completeResponse.Code)
	assert.Equal(t, "no-store", completeResponse.Header().Get(echo.HeaderCacheControl))
	var completed CompleteResponse
	require.NoError(t, json.Unmarshal(completeResponse.Body.Bytes(), &completed))
	cookies := completeResponse.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, CookieName, cookie.Name)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Zero(t, cookie.MaxAge, "a Public-computer Session must use a browser-session cookie")
	session, err := service.Session(context.Background(), cookie.Value)
	require.NoError(t, err)
	assert.Equal(t, "Cookie Person", session.DisplayName)
	assert.Equal(t, "public", session.SessionType)
	assert.Equal(t, completed.CSRFToken, session.CSRFToken)

	withoutCSRF := performJSON(e, http.MethodPost, "/api/session/logout", "", nil, cookie)
	assert.Equal(t, http.StatusForbidden, withoutCSRF.Code)
	var active int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(context.Background(), &active))
	assert.Equal(t, 1, active)

	withCSRF := performJSON(e, http.MethodPost, "/api/session/logout", "", map[string]string{CSRFHeader: completed.CSRFToken}, cookie)
	assert.Equal(t, http.StatusNoContent, withCSRF.Code)
	cleared := withCSRF.Result().Cookies()
	require.Len(t, cleared, 1)
	assert.Equal(t, -1, cleared[0].MaxAge)
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(context.Background(), &active))
	assert.Zero(t, active)
	_, err = service.Session(context.Background(), cookie.Value)
	require.ErrorIs(t, err, ErrUnauthenticated)
	var signedOutAudit int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM security_audit_events WHERE action = 'session_signed_out' AND outcome = 'success'`).Scan(context.Background(), &signedOutAudit))
	assert.Equal(t, 1, signedOutAudit)
}

func TestTrustedSessionRefreshExtendsInactivityWithoutMutatingGET(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Trusted Person", "trusted@example.com")
	e := newSetupHTTP(t, service)
	payload, err := json.Marshal(completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	completeResponse := performJSON(e, http.MethodPost, "/api/setup/complete", string(payload), nil)
	require.Equal(t, http.StatusCreated, completeResponse.Code)
	cookie := completeResponse.Result().Cookies()[0]
	var completed CompleteResponse
	require.NoError(t, json.Unmarshal(completeResponse.Body.Bytes(), &completed))

	var beforeActivity, beforeExpiry time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at, idle_expires_at FROM sessions`).Scan(context.Background(), &beforeActivity, &beforeExpiry))
	getSession := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	e.ServeHTTP(getSession, request)
	require.Equal(t, http.StatusOK, getSession.Code)
	var afterGET time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at FROM sessions`).Scan(context.Background(), &afterGET))
	assert.Equal(t, beforeActivity, afterGET, "safe GET must not refresh Session activity")

	refreshTime := beforeActivity.Add(24 * time.Hour)
	service.now = func() time.Time { return refreshTime }
	refreshed := performJSON(
		e, http.MethodPost, "/api/session/refresh", "",
		map[string]string{CSRFHeader: completed.CSRFToken}, cookie,
	)
	require.Equal(t, http.StatusNoContent, refreshed.Code)
	cookies := refreshed.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Greater(t, cookies[0].MaxAge, 0)
	assert.WithinDuration(t, refreshTime.Add(trustedLifetime), cookies[0].Expires, time.Second)

	var afterActivity, afterExpiry time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at, idle_expires_at FROM sessions`).Scan(context.Background(), &afterActivity, &afterExpiry))
	assert.Equal(t, refreshTime, afterActivity)
	assert.Greater(t, afterExpiry, beforeExpiry)
}

func TestExpiredEpochAndAccessInvalidSessionsAcrossRoutes(t *testing.T) {
	tests := []struct {
		name        string
		sessionType string
		invalidate  func(*testing.T, *bun.DB, *Service, time.Time)
	}{
		{
			name: "Public-computer Session at twelve-hour boundary", sessionType: "public",
			invalidate: func(_ *testing.T, _ *bun.DB, service *Service, createdAt time.Time) {
				service.now = func() time.Time { return createdAt.Add(12 * time.Hour) }
			},
		},
		{
			name: "Trusted-device Session at one-year boundary", sessionType: "trusted",
			invalidate: func(_ *testing.T, _ *bun.DB, service *Service, createdAt time.Time) {
				service.now = func() time.Time { return createdAt.Add(365 * 24 * time.Hour) }
			},
		},
		{
			name: "rotated security epoch", sessionType: "trusted",
			invalidate: func(t *testing.T, db *bun.DB, _ *Service, _ time.Time) {
				_, err := db.NewRaw(`UPDATE system_settings SET security_epoch = decode(repeat('ab', 32), 'hex')`).Exec(context.Background())
				require.NoError(t, err)
			},
		},
		{
			name: "suspended access generation", sessionType: "trusted",
			invalidate: func(t *testing.T, db *bun.DB, _ *Service, _ time.Time) {
				_, err := db.NewRaw(`UPDATE recipient_access_generations SET state = 'suspended'`).Exec(context.Background())
				require.NoError(t, err)
			},
		},
		{
			name: "noncurrent access generation", sessionType: "trusted",
			invalidate: func(t *testing.T, db *bun.DB, _ *Service, _ time.Time) {
				_, err := db.NewRaw(`UPDATE recipient_access_generations SET is_current = false`).Exec(context.Background())
				require.NoError(t, err)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, service := newSetupService(t)
			createdAt := time.Now().UTC().Truncate(time.Microsecond)
			service.now = func() time.Time { return createdAt }
			verified := verifiedChallenge(t, db, service, "Invalid Session", "invalid-session@example.com")
			completed, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, test.sessionType))
			require.NoError(t, err)
			test.invalidate(t, db, service, createdAt)
			e := newSetupHTTP(t, service)
			cookie := &http.Cookie{Name: CookieName, Value: completed.Credential}

			getResponse := httptest.NewRecorder()
			getRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/session", nil)
			getRequest.AddCookie(cookie)
			e.ServeHTTP(getResponse, getRequest)
			assert.Equal(t, http.StatusUnauthorized, getResponse.Code)
			refreshResponse := performJSON(e, http.MethodPost, "/api/session/refresh", "", map[string]string{CSRFHeader: completed.CSRFToken}, cookie)
			assert.Equal(t, http.StatusUnauthorized, refreshResponse.Code)
			logoutResponse := performJSON(e, http.MethodPost, "/api/session/logout", "", map[string]string{CSRFHeader: completed.CSRFToken}, cookie)
			assert.Equal(t, http.StatusUnauthorized, logoutResponse.Code)
		})
	}
}

func TestSessionBoundCSRFCannotCrossSessions(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "CSRF Person", "csrf@example.com")
	first, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)

	var personID, accessID uuid.UUID
	var securityEpoch []byte
	require.NoError(t, db.NewRaw(`SELECT person_id, recipient_access_generation_id, security_epoch FROM sessions`).Scan(
		context.Background(), &personID, &accessID, &securityEpoch,
	))
	secondRaw := bytes.Repeat([]byte{0x42}, 32)
	secondCredential := hex.EncodeToString(secondRaw)
	secondSessionID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	createdAt := time.Now().UTC()
	_, err = db.NewRaw(`
		INSERT INTO sessions (
			id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
			session_type, created_at, last_activity_at, idle_expires_at
		) VALUES (?, ?, ?, ?, ?, 'trusted', ?, ?, ?)
	`, secondSessionID, tokenHash(secondRaw), personID, accessID, securityEpoch, createdAt, createdAt, createdAt.Add(365*24*time.Hour)).Exec(context.Background())
	require.NoError(t, err)
	secondCSRF := service.csrfToken(secondRaw)

	_, err = service.refresh(context.Background(), secondCredential, first.CSRFToken)
	require.ErrorIs(t, err, ErrCSRF)
	require.ErrorIs(t, service.Logout(context.Background(), secondCredential, first.CSRFToken), ErrCSRF)
	_, err = service.refresh(context.Background(), first.Credential, secondCSRF)
	require.ErrorIs(t, err, ErrCSRF)

	var secondActivity time.Time
	require.NoError(t, db.NewRaw(`SELECT last_activity_at FROM sessions WHERE id = ?`, secondSessionID).Scan(context.Background(), &secondActivity))
	assert.Equal(t, createdAt.Truncate(time.Microsecond), secondActivity)
}

func TestAuthorizeCuratorRequiresCurrentRoleAndSessionBoundCSRFForMutations(t *testing.T) {
	db, service := newSetupService(t)
	verified := verifiedChallenge(t, db, service, "Source Curator", "source-curator@example.com")
	completed, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)

	_, err = service.AuthorizeCurator(context.Background(), completed.Credential, "", false)
	require.NoError(t, err)
	_, err = service.AuthorizeCurator(context.Background(), completed.Credential, completed.CSRFToken, true)
	require.NoError(t, err)
	_, err = service.AuthorizeCurator(context.Background(), completed.Credential, "wrong", true)
	require.ErrorIs(t, err, ErrCSRF)
	_, err = service.AuthorizeCurator(context.Background(), "invalid", completed.CSRFToken, true)
	require.ErrorIs(t, err, ErrUnauthenticated)

	_, err = db.ExecContext(context.Background(), `DELETE FROM person_roles WHERE role = 'curator'`)
	require.NoError(t, err)
	_, err = service.AuthorizeCurator(context.Background(), completed.Credential, "", false)
	require.ErrorIs(t, err, ErrNotCurator)
}

func TestSetupRateLimitsAreNonEnumeratingAcrossMutations(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  string
		limit func(*Service)
	}{
		{
			name: "code request by normalized email", path: "/api/setup/code",
			body:  `{"display_name":"Rate Limited","email":"rate@example.com"}`,
			limit: func(service *Service) { service.security.SetupEmailLimit = 1 },
		},
		{
			name: "verification by IP", path: "/api/setup/verify",
			body:  `{"challenge_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","code":"12345678"}`,
			limit: func(service *Service) { service.security.SetupIPLimit = 1 },
		},
		{
			name: "completion by IP", path: "/api/setup/complete",
			body:  `{"verification_token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","privacy_acknowledged":true,"engagement_acknowledged":true,"interest_list_acknowledged":true,"email_preference":"immediate","session_type":"trusted"}`,
			limit: func(service *Service) { service.security.SetupIPLimit = 1 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, service := newSetupService(t)
			test.limit(service)
			e := newSetupHTTP(t, service)
			first := performJSON(e, http.MethodPost, test.path, test.body, nil)
			assert.NotEqual(t, http.StatusTooManyRequests, first.Code)
			second := performJSON(e, http.MethodPost, test.path, test.body, nil)
			assert.Equal(t, http.StatusTooManyRequests, second.Code)
			assert.Contains(t, second.Body.String(), "Too many setup attempts")
			assert.NotContains(t, strings.ToLower(second.Body.String()), "rate@example.com")
			var people int
			require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
			assert.Zero(t, people)
		})
	}
}

func TestSetupClosureSurvivesNewServiceAndSafeGETsCreateNothing(t *testing.T) {
	db, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	before := httptest.NewRecorder()
	e.ServeHTTP(before, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/setup", nil))
	require.Equal(t, http.StatusOK, before.Code)
	assert.Equal(t, "no-store", before.Header().Get(echo.HeaderCacheControl))
	for _, table := range []string{"people", "sessions"} {
		var count int
		require.NoError(t, db.NewRaw(`SELECT count(*) FROM `+table).Scan(context.Background(), &count))
		assert.Zero(t, count)
	}

	verified := verifiedChallenge(t, db, service, "Permanent Curator", "permanent@example.com")
	_, err := service.complete(context.Background(), completionRequest(verified.VerificationToken, "trusted"))
	require.NoError(t, err)
	restartedSender := &acceptingSender{messages: make(chan smtp.Message, 1)}
	restarted := New(
		db,
		emaildelivery.New(db, config.SMTPConfig{Enabled: true}, restartedSender, integrationSecret),
		testSecurityConfig(),
	)
	restartedHTTP := newSetupHTTP(t, restarted)

	after := httptest.NewRecorder()
	restartedHTTP.ServeHTTP(after, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/setup", nil))
	assert.Equal(t, http.StatusNotFound, after.Code)
	unauthenticatedSession := httptest.NewRecorder()
	restartedHTTP.ServeHTTP(unauthenticatedSession, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/session", nil))
	assert.Equal(t, http.StatusUnauthorized, unauthenticatedSession.Code)
	_, err = restarted.RequestCode(context.Background(), RequestCodeRequest{DisplayName: "Other", Email: "other@example.com"})
	require.ErrorIs(t, err, ErrSetupComplete)

	var people, sessions int
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM people`).Scan(context.Background(), &people))
	require.NoError(t, db.NewRaw(`SELECT count(*) FROM sessions`).Scan(context.Background(), &sessions))
	assert.Equal(t, 1, people)
	assert.Equal(t, 1, sessions)
}

func TestSetupCompletionRejectsInsecureNonLoopbackRequest(t *testing.T) {
	db, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	body := `{"verification_token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","privacy_acknowledged":true,"engagement_acknowledged":true,"interest_list_acknowledged":true,"email_preference":"immediate","session_type":"trusted"}`
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://memento.example/api/setup/complete", strings.NewReader(body))
	request.RemoteAddr = "203.0.113.7:1234"
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "requires HTTPS")
	var complete bool
	require.NoError(t, db.NewRaw(`SELECT setup_complete FROM system_settings WHERE id = 1`).Scan(context.Background(), &complete))
	assert.False(t, complete)
}

func TestSetupMutationRequiresJSON(t *testing.T) {
	_, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/setup/code", strings.NewReader("display_name=Cross+Site&email=cross%40example.com"))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
}

func TestInvalidOrMissingSetupRecordsDoNotBecomeInternalDetails(t *testing.T) {
	_, service := newSetupService(t)
	e := newSetupHTTP(t, service)
	response := performJSON(e, http.MethodPost, "/api/setup/verify", `{"challenge_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","code":"12345678"}`, nil)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "invalid or expired")
	assert.NotContains(t, response.Body.String(), "challenge_hash")
}
